// Package rag 稠密召回 + 内存 BM25 经 RRF 融合的 Hybrid 检索（v0.2）+ 标题加权。
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

// DefaultFloor 为稠密余弦下限默认（0=关闭；校准值由服务显式设置）。
const DefaultFloor float32 = 0

// RAG 组合 vector store + embedder + 内存 BM25 镜像，对外提供 AddDoc / Search。
type RAG struct {
	store *store.VectorStore
	embed Embedder
	bm    *bm25Index
	// Floor 为融合后 Score 下限；低于者丢弃（真拒答）。0 关闭。
	Floor float32
}

// New 构造 RAG，embed 为 nil 时用离线 HashEmbedder。
func New(s *store.VectorStore, e Embedder) *RAG {
	if e == nil {
		e = HashEmbedder{}
	}
	return &RAG{store: s, embed: e, bm: newBM25Index()}
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

// AddDoc 切分 md 并写入 store（source 标记 demo/upload，reindex 只清 demo），
// point id 为 doc+序号 hash；同步镜像 chunk 到内存 BM25（同 ID 键覆盖）。
func (r *RAG) AddDoc(doc, md, source string) error {
	chunks := ChunkMarkdown(doc, md)
	for i, c := range chunks {
		vec, err := r.embed.Embed(c.Title + "\n" + c.Snippet)
		if err != nil {
			return err
		}
		sum := md5.Sum([]byte(fmt.Sprintf("%s#%d#%s", doc, i, c.Snippet)))
		id := fmt.Sprintf("%x", sum)
		r.bm.upsert(id, doc, c.Title, c.Snippet, source)
		if err := r.store.Upsert(store.Point{
			ID:        id,
			Title:     c.Title,
			Content:   "【" + doc + "】" + c.Snippet,
			Doc:       doc,
			Source:    source,
			Embedding: vec,
		}); err != nil {
			return err
		}
	}
	return nil
}

// DeleteDoc 删除某文档：store 按 payload.doc 过滤删点 + BM25 镜像同步清理。
// store 删除失败返回错误，调用方不应视为已删。
func (r *RAG) DeleteDoc(doc string) error {
	if err := r.store.DeleteByDoc(doc); err != nil {
		return err
	}
	r.bm.deleteDoc(doc)
	return nil
}

// DeleteSource 删除某来源（demo/upload）全部向量，reindex 同步用。
func (r *RAG) DeleteSource(source string) error {
	if err := r.store.DeleteBySource(source); err != nil {
		return err
	}
	r.bm.deleteSource(source)
	return nil
}

// Search 稠密 + BM25 经 RRF(k=60) 融合再标题加权；无匹配返回空，不编造。
// 稠密与 BM25 各取 topK*2，融合后按 RRF 分排序、TitleBoost 加权，截 topK。
func (r *RAG) Search(query string, topK int) ([]Result, error) {
	if topK <= 0 {
		topK = 5
	}
	return r.searchFused(query, topK*2, topK)
}

// SearchPool 候选池版 Search：与 Search 同一套稠密+BM25 RRF 融合逻辑，
// 仅截断数换成 poolN（供 LLM rerank 二排，评测链路用，不碰线上）。
func (r *RAG) SearchPool(query string, poolN int) ([]Result, error) {
	if poolN <= 0 {
		poolN = 8
	}
	return r.searchFused(query, poolN*2, poolN)
}

// searchFused 融合检索内核：各路取 fetchK，融合加权后截 outK。
func (r *RAG) searchFused(query string, fetchK, outK int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	qv, err := r.embed.Embed(query)
	if err != nil {
		return nil, err
	}
	hits, err := r.store.Search(qv, fetchK)
	if err != nil {
		return nil, err
	}
	// 稠密门控：最佳余弦低于 Floor 整查返回空（真拒答）。BM25 只做召回增强，
	// 不单独撑起命中（融合分不可切，7 库实测重叠）。
	if r.Floor > 0 && (len(hits) == 0 || hits[0].Score < r.Floor) {
		return nil, nil
	}
	bhits := r.bm.search(query, fetchK)
	if len(hits) == 0 && len(bhits) == 0 {
		return nil, nil
	}
	type cand struct {
		doc     string
		title   string
		snippet string
	}
	cands := make(map[string]cand, len(hits)+len(bhits))
	denseIDs := make([]string, 0, len(hits))
	for _, h := range hits {
		denseIDs = append(denseIDs, h.Point.ID)
		cands[h.Point.ID] = cand{
			doc:     docOf(h.Point.Content),
			title:   h.Point.Title,
			snippet: h.Point.Content,
		}
	}
	bmIDs := make([]string, 0, len(bhits))
	r.bm.mu.RLock()
	for _, b := range bhits {
		bmIDs = append(bmIDs, b.ID)
		if _, ok := cands[b.ID]; !ok {
			if d, ok := r.bm.docs[b.ID]; ok {
				cands[b.ID] = cand{
					doc:     d.Doc,
					title:   d.Title,
					snippet: "【" + d.Doc + "】" + d.Snippet,
				}
			}
		}
	}
	r.bm.mu.RUnlock()
	fused := rrfFuse(denseIDs, bmIDs, RRFK)
	out := make([]Result, 0, len(fused))
	for id, fs := range fused {
		c, ok := cands[id]
		if !ok {
			continue
		}
		out = append(out, Result{
			Doc:     c.doc,
			Snippet: c.snippet,
			Score:   TitleBoost(float32(fs), query, c.title),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > outK {
		out = out[:outK]
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
