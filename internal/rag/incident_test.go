package rag

import (
	"strings"
	"testing"

	"oncall-agent/internal/store"
)

// v0.4 事件沉淀回归（ADR-0005）：source 透出、incident 降权、同题覆盖。
func TestIncidentNoteDownweight(t *testing.T) {
	s := store.NewMemoryVector()
	r := New(s, nil) // IncidentWeight 默认 0.5

	demo := "# CPU 高负载处置\n## 现象\nCPU 使用率持续大于 90%\n## 处置\n限流扩容"
	if err := r.AddDoc("cpu_high_usage.md", demo, "demo"); err != nil {
		t.Fatal(err)
	}
	incident := "# CPUHighUsage 事件沉淀\n## 告警\n- alertname: CPUHighUsage\n## 诊断\nCPU 使用率持续大于 90%，按 runbook 限流扩容"
	if err := r.IngestIncident("CPUHighUsage.incident.md", incident); err != nil {
		t.Fatal(err)
	}

	hits, err := r.Search("CPU 使用率持续大于 90%", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search failed: hits=%v err=%v", hits, err)
	}
	if hits[0].Doc != "cpu_high_usage.md" {
		t.Fatalf("incident should rank below human runbook, got top=%+v", hits[0])
	}
	bySource := map[string]bool{}
	for _, h := range hits {
		bySource[h.Source] = true
	}
	if !bySource["demo"] || !bySource["incident"] {
		t.Fatalf("source not carried through: %+v", hits)
	}

	// 关闭降权（权重>=1）后，沉淀凭标题加权可反超。
	r.IncidentWeight = 1
	hits, err = r.Search("CPUHighUsage", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search failed: hits=%v err=%v", hits, err)
	}
	if hits[0].Doc != "CPUHighUsage.incident.md" {
		t.Fatalf("weight=1 should let incident top with title boost, got %+v", hits)
	}
}

func TestIncidentOverwriteSameTitle(t *testing.T) {
	s := store.NewMemoryVector()
	r := New(s, nil)
	v1 := "# MQBacklog 事件沉淀\n## 诊断\n堆积 10 万条，扩容消费者到 8"
	if err := r.IngestIncident("MQBacklog.incident.md", v1); err != nil {
		t.Fatal(err)
	}
	v2 := "# MQBacklog 事件沉淀\n## 诊断\n堆积 30 万条，紧急扩容消费者到 32 并清空死信"
	if err := r.IngestIncident("MQBacklog.incident.md", v2); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"堆积 10 万条", "消费者到 8"} {
		hits, err := r.Search(q, 5)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hits {
			// 同题命中允许（分词重叠），但 v1 独有内容必须不可检出。
			if h.Doc == "MQBacklog.incident.md" && strings.Contains(h.Snippet, "10 万条") {
				t.Fatalf("stale incident v1 content retrievable (q=%s): %+v", q, h)
			}
		}
	}
	hits, err := r.Search("堆积 30 万条", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("incident v2 lost: hits=%v err=%v", hits, err)
	}

	// 诊断 eval 防自证路径：按 source 清沉淀。
	if err := r.DeleteSource("incident"); err != nil {
		t.Fatal(err)
	}
	hits, err = r.Search("堆积 30 万条", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Source == "incident" {
			t.Fatalf("incident not cleared by source: %+v", h)
		}
	}
}
