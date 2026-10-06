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

// ADR-0011 replaces implicit fallback with explicit persistent failures.
func TestPersistentFailureNeverLatchesMemory(t *testing.T) {
	dead := NewVector("http://127.0.0.1:1", "col-state")
	if err := dead.EnsureCollection(3); err == nil {
		t.Fatal("collection creation failure must propagate")
	}
	if err := dead.Upsert(Point{ID: "p1", Embedding: []float32{1, 0, 0}}); err == nil {
		t.Fatal("write failure must propagate")
	}
	if _, err := dead.Search([]float32{1, 0, 0}, 5); err == nil {
		t.Fatal("search failure must propagate")
	}
	if dead.IsMemOnly() {
		t.Fatal("persistent failures must not switch storage mode")
	}
	if len(dead.mem) != 0 {
		t.Fatal("failed persistent write populated memory")
	}
}
