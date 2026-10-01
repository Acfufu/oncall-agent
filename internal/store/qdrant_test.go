package store

import (
	"math"

	"testing"
)

// R01：混维度内存搜索守卫——embedder 降级/恢复过渡期内存镜像可能 64/768 混存，
// cosine 截断到 min(len) 会对维度不符的点产出假分数。维度与查询不符的点必须
// 跳过，宁可少返回不返回垃圾。
func TestSearchMemSkipsDimensionMismatch(t *testing.T) {
	s := NewMemoryVector()

	// 同文本的 4 维与 8 维版（归一），截断余弦必为正——修前 8 维点会被假分拉进结果。
	q4 := normalize([]float32{1, 0, 0, 0})
	v4 := normalize([]float32{1, 0, 0, 0})
	v8 := normalize([]float32{1, 0, 0, 0, 1, 0, 0, 0})

	mustUpsert(t, s, Point{ID: "d4", Doc: "真四维", Embedding: v4, Content: "a"})
	mustUpsert(t, s, Point{ID: "d8", Doc: "假八维", Embedding: v8, Content: "b"})

	hits, err := s.Search(q4, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Point.ID == "d8" {
			t.Fatalf("dimension-mismatched point returned with garbage score %f (hits=%d)", h.Score, len(hits))
		}
	}
	if len(hits) != 1 || hits[0].Point.ID != "d4" {
		t.Fatalf("want only dim-matched d4, got %d hits: %+v", len(hits), hits)
	}
}

func mustUpsert(t *testing.T, s *VectorStore, p Point) {
	t.Helper()
	if err := s.Upsert(p); err != nil {
		t.Fatal(err)
	}
}

func normalize(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n == 0 {
		return v
	}
	n = math.Sqrt(n)
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
	return v
}

// R05：降级状态机——死 Qdrant 上建库/首写触发 memOnly 闩锁（离线入口语义，
// F04 可观测），闩锁后读写走内存镜像、删除如实清内存不碰 HTTP、单实例内
// 不回切（重启进程恢复）。
func TestFallbackLatchStateMachine(t *testing.T) {
	dead := NewVector("http://127.0.0.1:1", "col-state") // 死端口，传输错误
	if dead.IsMemOnly() {
		t.Fatal("fresh store must not start memonly")
	}
	// 建库失败同样闩锁且如实返 nil（离线入口）。
	if err := dead.EnsureCollection(3); err != nil {
		t.Fatalf("ensure against dead qdrant degrades with nil: %v", err)
	}
	if !dead.IsMemOnly() {
		t.Fatal("ensure failure must latch memonly")
	}
	if err := dead.Upsert(Point{ID: "p1", Doc: "d1", Content: "c1", Embedding: normalize([]float32{1, 0, 0})}); err != nil {
		t.Fatalf("degraded write goes to memory mirror: %v", err)
	}
	// 闩锁后读己之写。
	hits, err := dead.Search(normalize([]float32{1, 0, 0}), 5)
	if err != nil || len(hits) != 1 || hits[0].Point.ID != "p1" {
		t.Fatalf("memonly read-your-writes broken: hits=%d err=%v", len(hits), err)
	}
	// 闩锁后删除：内存镜像同步清、如实返回、不再碰 HTTP。
	if err := dead.DeleteByDoc("d1"); err != nil {
		t.Fatalf("memonly delete must not error: %v", err)
	}
	if hits, _ := dead.Search(normalize([]float32{1, 0, 0}), 5); len(hits) != 0 {
		t.Fatalf("memonly delete left residue: %d hits", len(hits))
	}
	// 单实例内不回切。
	if !dead.IsMemOnly() {
		t.Fatal("latch must be one-way within instance")
	}
}
