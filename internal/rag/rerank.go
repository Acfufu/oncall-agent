// Package rag 追加：LLM-as-rerank（v0.2 试水，仅评测链路）。
//
// Ranker 经 OpenAI 兼容 /chat/completions 单 prompt 给候选打分 0-10，
// 解析排序后保持 Result 形状返回。解析失败 / 远端异常一律原序返回，不炸。
// 仅标准库。线上 /chat 不调用。
package rag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// RerankDefaults 为本地 LM Studio 默认值（thinking 模型，需 max_tokens 足够大）。
const (
	RerankBaseURL  = "http://localhost:1234/v1"
	RerankModel    = "qwen/qwen3-vl-8b"
	RerankMaxToken = 2048
	rerankTimeout  = 300 * time.Second
	rerankSnippetN = 300 // prompt 内单候选 snippet 截断（rune）
)

// Ranker 经 OpenAI 兼容 chat 接口做 listwise 打分。
type Ranker struct {
	BaseURL string
	Model   string
	APIKey  string // 本地无鉴权，可空；非空则带 Authorization
	Client  *http.Client
}

// NewRanker 构造 Ranker，空值填本地默认。Key 空或 "lm-studio" 均可。
func NewRanker(baseURL, model, key string) *Ranker {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = RerankBaseURL
	}
	if strings.TrimSpace(model) == "" {
		model = RerankModel
	}
	return &Ranker{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		APIKey:  key,
		Client:  &http.Client{Timeout: rerankTimeout},
	}
}

// Rerank 一次列出全部候选，要求模型只回 JSON 数组 [{id,score}]，
// 按 score 降序重排（Result.Doc/Snippet 不变，Score 置为 rerank 分）。
// 任何失败（网络/空 content/解析无有效分）返回原序 + 非 nil error，调用方可照常用原序。
func (rk *Ranker) Rerank(query string, cands []Result, topK int) ([]Result, error) {
	out := append([]Result(nil), cands...)
	if len(out) == 0 {
		return out, nil
	}
	if strings.TrimSpace(query) == "" {
		return out, fmt.Errorf("rerank: empty query")
	}
	scores, err := rk.scoreRemote(query, out)
	if err != nil {
		return out, err
	}
	// 无有效分则原序。
	hit := false
	for i := range out {
		if s, ok := scores[i]; ok {
			hit = true
			out[i].Score = s
		}
	}
	if !hit {
		return append([]Result(nil), cands...), fmt.Errorf("rerank: no valid scores, keep order")
	}
	// 稳定排序：同分保持原相对序（sort.SliceStable + 原索引）。
	idx := make([]int, len(out))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return out[idx[a]].Score > out[idx[b]].Score })
	sorted := make([]Result, len(out))
	for i, j := range idx {
		sorted[i] = out[j]
	}
	if topK > 0 && len(sorted) > topK {
		sorted = sorted[:topK]
	}
	return sorted, nil
}

// scoreRemote 组 prompt、调 chat、解析 [{id,score}]，返回 map[候选下标]分数。
func (rk *Ranker) scoreRemote(query string, cands []Result) (map[int]float32, error) {
	var sb strings.Builder
	sb.WriteString("你是故障知识库排序器。对下列候选按与用户问题的相关度打分 0-10（10=直接命中故障）。\n")
	sb.WriteString("只返回 JSON 数组，不要其他文字，格式：[{\"id\":0,\"score\":8.5},...]。\n")
	sb.WriteString("问题：" + strings.TrimSpace(query) + "\n")
	for i, c := range cands {
		sb.WriteString(fmt.Sprintf("--- 候选 id=%d doc=%s ---\n%s\n", i, c.Doc, truncateRunes(c.Snippet, rerankSnippetN)))
	}
	reqBody, _ := json.Marshal(map[string]any{
		"model":      rk.Model,
		"messages":   []map[string]string{{"role": "user", "content": sb.String()}},
		"max_tokens": RerankMaxToken,
	})
	client := rk.Client
	if client == nil {
		client = &http.Client{Timeout: rerankTimeout}
	}
	req, err := http.NewRequest("POST", rk.BaseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(rk.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(rk.APIKey))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var rsp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rsp); err != nil {
		return nil, err
	}
	if len(rsp.Choices) == 0 {
		return nil, fmt.Errorf("rerank: empty choices")
	}
	return parseRerankScores(rsp.Choices[0].Message.Content, len(cands))
}

// parseRerankScores 从模型回复中提取 JSON 数组 [{id,score}]。
// 兼容 thinking 模型前后夹杂文字：取首个 '[' 到末个 ']' 切片再解析；
// id 越界 / score 非数跳过；score 钳到 [0,10]。
func parseRerankScores(content string, n int) (map[int]float32, error) {
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("rerank: no json array in reply")
	}
	var arr []struct {
		ID    int     `json:"id"`
		Score float64 `json:"score"`
	}
	dec := json.NewDecoder(strings.NewReader(content[start : end+1]))
	if err := dec.Decode(&arr); err != nil {
		return nil, fmt.Errorf("rerank: decode: %w", err)
	}
	m := make(map[int]float32, len(arr))
	for _, e := range arr {
		if e.ID < 0 || e.ID >= n {
			continue
		}
		s := e.Score
		if s != s { // NaN
			continue
		}
		if s < 0 {
			s = 0
		}
		if s > 10 {
			s = 10
		}
		if _, dup := m[e.ID]; !dup {
			m[e.ID] = float32(s)
		}
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("rerank: zero valid scores")
	}
	return m, nil
}

// RerankFused RRF 融合 hybrid 序与 LLM 序（护栏：单边误排不直接丢档，缺席边记 0）。
func RerankFused(hybrid, llm []Result, topK int) []Result {
	key := func(r Result) string { return r.Doc + "\x00" + r.Snippet }
	rankOf := func(rs []Result) map[string]int {
		m := make(map[string]int, len(rs))
		for i, r := range rs {
			if _, ok := m[key(r)]; !ok {
				m[key(r)] = i + 1
			}
		}
		return m
	}
	rH, rL := rankOf(hybrid), rankOf(llm)
	union := make([]Result, 0, len(hybrid)+len(llm))
	seen := make(map[string]bool)
	for _, rs := range [][]Result{hybrid, llm} {
		for _, r := range rs {
			if k := key(r); !seen[k] {
				seen[k] = true
				union = append(union, r)
			}
		}
	}
	type fs struct {
		r Result
		s float64
	}
	scored := make([]fs, 0, len(union))
	for _, r := range union {
		s := 0.0
		if rh, ok := rH[key(r)]; ok {
			s += 1 / (RRFK + float64(rh))
		}
		if rl, ok := rL[key(r)]; ok {
			s += 1 / (RRFK + float64(rl))
		}
		r.Score = float32(s)
		scored = append(scored, fs{r, s})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].s > scored[j].s })
	out := make([]Result, 0, len(scored))
	for _, x := range scored {
		out = append(out, x.r)
	}
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
