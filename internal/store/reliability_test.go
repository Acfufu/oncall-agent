package store

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPersistentFailureDoesNotFallback(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer s.Close()
	v := NewVector(s.URL, "test")
	if err := v.Upsert(Point{ID: "x", Embedding: []float32{1}}); err == nil {
		t.Fatal("persistent failure falsely successful")
	}
	if v.IsMemOnly() {
		t.Fatal("persistent store became memory")
	}
}

func TestDimensionMismatchNeverDeletes(t *testing.T) {
	deletes := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes++
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":{"config":{"params":{"vectors":{"size":768}}}}}`))
	}))
	defer s.Close()
	v := NewVector(s.URL, "test")
	if err := v.EnsureCompatible(context.Background(), 64); err == nil {
		t.Fatal("dimension mismatch accepted")
	}
	if err := v.RecreateCollection(64); err == nil {
		t.Fatal("destructive recreate accepted")
	}
	if deletes != 0 {
		t.Fatal("existing collection deleted")
	}
}
func TestSpaceMismatchRejectsSameDimensionModel(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"payload":{"space_id":"old-model"}}}`))
	}))
	defer s.Close()
	if err := NewVector(s.URL, "test").EnsureSpace(context.Background(), "new-model"); err == nil {
		t.Fatal("different embedding space accepted")
	}
}
func TestCancelledPersistentWriteHasNoHTTPRequest(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := NewVector(s.URL, "test")
	if err := v.UpsertWithContext(ctx, Point{ID: "x", Embedding: []float32{1}}); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
	if calls != 0 {
		t.Fatal("cancelled operation reached upstream")
	}
}
