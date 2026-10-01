package tool

import (
	"encoding/json"
	"testing"

	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
)

// F15：rag_search 工具输出透传 source 信任级（demo/upload=人工审定知识，
// incident=AI 事件沉淀）。此前工具 JSON 只带 doc/snippet/score，agent 看不到
// 证据信任级——检索层与报告 citations 侧均有 source，唯独工具面断档。
func TestRagSearchToolTextCarriesSource(t *testing.T) {
	s := store.NewMemoryVector()
	r := rag.New(s, nil)
	demo := "# 故障A\n## 现象\nCPU 打满\n## 处置\n重启进程"
	upload := "# 上传B\n## 日志\n磁盘使用率 95%\n## 处置\n扩容磁盘"
	if err := r.AddDoc("故障A", demo, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDoc("上传B", upload, "upload"); err != nil {
		t.Fatal(err)
	}
	d := &RAGDeps{RAG: r}
	out, hits, err := d.RagSearchWithContext(nil, `{"query":"CPU 打满 磁盘 扩容","top_k":5}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits, got none")
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != len(hits) {
		t.Fatalf("items %d != hits %d", len(items), len(hits))
	}
	sources := map[string]bool{}
	for _, it := range items {
		src, _ := it["source"].(string)
		if src == "" {
			t.Fatalf("item missing source field: %v", it)
		}
		sources[src] = true
	}
	if !sources["demo"] || !sources["upload"] {
		t.Fatalf("want demo+upload sources, got %v (out=%s)", sources, out)
	}
}
