package handler

import (
	"os"
	"path/filepath"
	"testing"
)

// v0.8/ADR-0010：/reports 持久化——环变更同步原子写快照，boot 加载；
// 损坏文件自愈（告警按空启动）；容量截断；空路径=关闭。

func TestReportsPersistRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reports.json")

	r1 := &reportRing{}
	if err := r1.SetPersist(path); err != nil {
		t.Fatal(err)
	}
	r1.add(Report{ID: "a", Status: StatusDone})
	r1.add(Report{ID: "b", Status: StatusRunning})
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot not written on add: %v", err)
	}
	if !r1.update("b", func(r *Report) { r.Status = StatusDone; r.Score = 3 }) {
		t.Fatal("update missed")
	}

	r2 := &reportRing{}
	if err := r2.SetPersist(path); err != nil {
		t.Fatal(err)
	}
	if err := r2.LoadPersist(); err != nil {
		t.Fatal(err)
	}
	got := r2.snapshot()
	if len(got) != 2 || got[0].ID != "b" || got[0].Status != StatusDone || got[0].Score != 3 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if got[1].ID != "a" {
		t.Fatalf("order mismatch (want newest first): %+v", got)
	}
}

func TestReportsPersistCorruptSelfHeals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reports.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &reportRing{}
	if err := r.SetPersist(path); err != nil {
		t.Fatal(err)
	}
	if err := r.LoadPersist(); err != nil {
		t.Fatalf("corrupt file must not error at load: %v", err)
	}
	// 自愈：后续变更照常落盘覆盖坏文件。
	r.add(Report{ID: "c", Status: StatusQueued})
	r2 := &reportRing{}
	_ = r2.SetPersist(path)
	if err := r2.LoadPersist(); err != nil {
		t.Fatal(err)
	}
	if got := r2.snapshot(); len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("self-heal failed: %+v", got)
	}
}

func TestReportsPersistCapTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reports.json")
	r := &reportRing{}
	_ = r.SetPersist(path)
	for i := 0; i < reportRingCap+10; i++ {
		r.add(Report{ID: string(rune('a'+i%26)) + timeIt(i), Status: StatusDone})
	}
	r2 := &reportRing{}
	_ = r2.SetPersist(path)
	_ = r2.LoadPersist()
	if got := len(r2.snapshot()); got != reportRingCap {
		t.Fatalf("loaded %d want cap %d", got, reportRingCap)
	}
}

func TestReportsPersistEmptyPathOff(t *testing.T) {
	dir := t.TempDir()
	r := &reportRing{}
	_ = r.SetPersist("") // 关闭
	r.add(Report{ID: "x", Status: StatusDone})
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty path must not write file: %v %v", entries, err)
	}
	if err := r.LoadPersist(); err != nil {
		t.Fatalf("load with persist off must be no-op nil: %v", err)
	}
}

func timeIt(i int) string {
	return string(rune('0'+i%10)) + string(rune('a'+i/10%26))
}
