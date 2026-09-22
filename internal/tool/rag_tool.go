package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"oncall-agent/internal/rag"
)

// RAGDeps 为 rag_search 依赖，由调用方注入（不改 rag 结构）。
type RAGDeps struct {
	RAG *rag.RAG
}

// RagSearch 只读检索知识库，返回 JSON 数组 [{doc,snippet,score}]。
// 无匹配返回 "[]"，不编造。
func (d *RAGDeps) RagSearch(argsJSON string) (string, []rag.Result, error) {
	var args struct {
		Query string  `json:"query"`
		TopK  float64 `json:"top_k"`
	}
	if err := argsOf(argsJSON, &args); err != nil {
		return "", nil, fmt.Errorf("rag_search bad args: %w", err)
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return "", nil, fmt.Errorf("rag_search: query required")
	}
	topK := int(args.TopK)
	if topK <= 0 {
		topK = 3
	}
	if topK > 5 {
		topK = 5
	}
	if d == nil || d.RAG == nil {
		return "[]", nil, nil
	}
	hits, err := d.RAG.Search(args.Query, topK)
	if err != nil {
		return "", nil, err
	}
	if len(hits) == 0 {
		return "[]", nil, nil
	}
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		out = append(out, map[string]any{"doc": h.Doc, "snippet": h.Snippet, "score": h.Score})
	}
	raw, _ := json.Marshal(out)
	return string(raw), hits, nil
}
