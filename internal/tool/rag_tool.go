package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"oncall-agent/internal/rag"
	"oncall-agent/internal/trace"
)

// RAGDeps 为 rag_search 依赖，由调用方注入（不改 rag 结构）。
type RAGDeps struct {
	RAG *rag.RAG
}

// RagSearch 只读检索知识库，返回 JSON 数组 [{doc,snippet,score}]。
// 无匹配返回 "[]"，不编造。无 ctx 版走 Background。
func (d *RAGDeps) RagSearch(argsJSON string) (string, []rag.Result, error) {
	return d.RagSearchWithContext(context.Background(), argsJSON)
}

// RagSearchWithContext 为 RagSearch 的 ctx 版：RAG.search 子 span 包检索。
func (d *RAGDeps) RagSearchWithContext(ctx context.Context, argsJSON string) (out string, hits []rag.Result, err error) {
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
	if ctx == nil {
		ctx = context.Background()
	}
	_, s := trace.Start(ctx, "RAG.search", map[string]string{
		"component": "RAG",
		"span.type": "search",
		"rag.query": args.Query,
	})
	defer func() {
		if err != nil {
			s.RecordError(err)
			s.SetStatus(trace.StatusError, err.Error())
		} else {
			s.SetStatus(trace.StatusOK, "")
			s.SetAttribute("rag.hits", itoa(len(hits)))
		}
		s.End()
	}()
	hits, err = d.RAG.Search(args.Query, topK)
	if err != nil {
		return "", nil, err
	}
	if len(hits) == 0 {
		return "[]", nil, nil
	}
	items := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		items = append(items, map[string]any{"doc": h.Doc, "snippet": h.Snippet, "score": h.Score})
	}
	raw, _ := json.Marshal(items)
	return string(raw), hits, nil
}
