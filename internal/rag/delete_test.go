package rag

import (
	"strings"
	"testing"

	"oncall-agent/internal/store"
)

// v0.3 delete 删全回归：/delete 后该文档稠密+稀疏均检不出，
// reindex 清 demo 来源不伤上传文档，同名重灌不留陈旧 chunk。
func TestDeleteDocAndResync(t *testing.T) {
	s := store.NewMemoryVector()
	r := New(s, nil)

	demoV1 := "# 故障A\n## 现象\nCPU 打满\n## 处置\n重启进程"
	upload := "# 上传B\n## 日志\n磁盘使用率 95%\n## 处置\n扩容磁盘"
	if err := r.AddDoc("故障A", demoV1, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDoc("上传B", upload, "upload"); err != nil {
		t.Fatal(err)
	}

	if err := r.DeleteDoc("故障A"); err != nil {
		t.Fatal(err)
	}
	hits, err := r.Search("CPU 打满", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Doc == "故障A" {
			t.Fatalf("deleted doc still retrievable: %+v", h)
		}
	}
	hits, err = r.Search("磁盘 扩容", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("upload doc lost after unrelated delete: hits=%v err=%v", hits, err)
	}

	// 同名重灌换内容（模拟陈旧 chunk 场景），再删一遍必须干净。
	demoV2 := "# 故障A\n## 现象\n内存泄漏\n## 处置\n滚动重启"
	if err := r.AddDoc("故障A", demoV2, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteDoc("故障A"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CPU 打满", "内存泄漏"} {
		hits, err := r.Search(q, 5)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hits {
			if h.Doc == "故障A" {
				t.Fatalf("stale chunk of re-added doc retrievable (q=%s): %+v", q, h)
			}
		}
	}

	// reindex 同步：清 demo 来源后 demo 内容全部消失，upload 保留。
	if err := r.AddDoc("故障A", demoV2, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteSource("demo"); err != nil {
		t.Fatal(err)
	}
	hits, err = r.Search("内存泄漏", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Doc == "故障A" {
			t.Fatalf("demo source not cleared: %+v", h)
		}
	}
	hits, err = r.Search("磁盘 扩容", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("upload doc lost after demo resync: hits=%v err=%v", hits, err)
	}
	if !strings.Contains(hits[0].Doc, "上传B") {
		t.Fatalf("unexpected hit after resync: %+v", hits[0])
	}
}
