// Package rag 单路稠密召回 + 标题加权（v0.1）。无 Hybrid/BM25/Rerank。
package rag

import (
	"crypto/md5"
	"fmt"
	"sort"
	"strings"

	"oncall-agent/internal/store"
)

// TitleBoostFactor 标题命中加权系数。
const TitleBoostFactor = 1.5

// Chunk 为 md 切分单元：Doc 为文档名，Title 为所属标题，Snippet 为引用片段。
type Chunk struct {
	Doc       string
	Title     string
	Snippet   string
	Embedding []float32
}

// Result 为检索返回：{doc,snippet,score}。
type Result struct {
	Doc     string  `json:"doc"`
	Snippet string  `json:"snippet"`
	Score   float32 `json:"score"`
}

// RAG 组合 vector store + embedder，对外提供 AddDoc / Search。
type RAG struct {
	store *store.VectorStore
	embed Embedder
}

// New 构造 RAG，embed 为 nil 时用离线 HashEmbedder。
func New(s *store.VectorStore, e Embedder) *RAG {
	if e == nil {
		e = HashEmbedder{}
	}
	return &RAG{store: s, embed: e}
}

// ChunkMarkdown 按 md 标题切分：一级标题为文档标题（故障名），
// 每个二级标题节为一 chunk；无标题时整篇一 chunk。Snippet 保留标题行以便引用。
func ChunkMarkdown(doc, md string) []Chunk {
	lines := strings.Split(md, "\n")
	docTitle := ""
	for _, ln := range lines {
		if t := parseHeading(ln); t != "" {
			docTitle = t
			break
		}
	}
	if docTitle == "" {
		docTitle = doc
	}

	var chunks []Chunk
	var curTitle string
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		title := curTitle
		if title == "" {
			title = docTitle
		}
		snip := strings.TrimSpace(strings.Join(cur, "\n"))
		if snip != "" {
			chunks = append(chunks, Chunk{Doc: doc, Title: title, Snippet: snip})
		}
		cur = nil
	}
	for _, ln := range lines {
		if lvl, t := headingLevel(ln); lvl == 2 && t != "" {
			flush()
			curTitle = t
			cur = append(cur, ln)
			continue
		}
		cur = append(cur, ln)
	}
	flush()
	if len(chunks) == 0 && strings.TrimSpace(md) != "" {
		chunks = append(chunks, Chunk{Doc: doc, Title: docTitle, Snippet: strings.TrimSpace(md)})
	}
	return chunks
}

func parseHeading(line string) string {
	_, t := headingLevel(line)
	return t
}

func headingLevel(line string) (int, string) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "#") {
		return 0, ""
	}
	lvl := 0
	for lvl < len(s) && s[lvl] == '#' {
		lvl++
	}
	t := strings.TrimSpace(s[lvl:])
	return lvl, t
}

// TitleBoost 标题命中则 score*1.5：query 含标题或标题含 query（大小写不敏感）。
func TitleBoost(score float32, query, title string) float32 {
	q := strings.ToLower(strings.TrimSpace(query))
	t := strings.ToLower(strings.TrimSpace(title))
	if q == "" || t == "" {
		return score
	}
	if strings.Contains(q, t) || strings.Contains(t, q) {
		return score * TitleBoostFactor
	}
	// 兜底：标题分词命中一半以上也算命中。
	parts := strings.Fields(t)
	if len(parts) == 0 {
		return score
	}
	hit := 0
	for _, p := range parts {
		if len(p) > 1 && strings.Contains(q, p) {
			hit++
		}
	}
	if hit*2 >= len(parts) {
		return score * TitleBoostFactor
	}
	return score
}

// AddDoc 切分 md 并写入 store，point id 为 doc+序号 hash。
func (r *RAG) AddDoc(doc, md string) error {
	chunks := ChunkMarkdown(doc, md)
	for i, c := range chunks {
		vec, err := r.embed.Embed(c.Title + "\n" + c.Snippet)
		if err != nil {
			return err
		}
		sum := md5.Sum([]byte(fmt.Sprintf("%s#%d#%s", doc, i, c.Snippet)))
		if err := r.store.Upsert(store.Point{
			ID:        fmt.Sprintf("%x", sum),
			Title:     c.Title,
			Content:   "【" + doc + "】" + c.Snippet,
			Embedding: vec,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Search 单路稠密召回 + 标题加权；无匹配返回空，不编造。
func (r *RAG) Search(query string, topK int) ([]Result, error) {
	if topK <= 0 {
		topK = 5
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	qv, err := r.embed.Embed(query)
	if err != nil {
		return nil, err
	}
	hits, err := r.store.Search(qv, topK*2)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}
	out := make([]Result, 0, len(hits))
	for _, h := range hits {
		doc := docOf(h.Point.Content)
		out = append(out, Result{
			Doc:     doc,
			Snippet: h.Point.Content,
			Score:   TitleBoost(h.Score, query, h.Point.Title),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

func docOf(content string) string {
	if strings.HasPrefix(content, "【") {
		if end := strings.Index(content, "】"); end > 0 {
			return content[len("【"):end]
		}
	}
	return ""
}
