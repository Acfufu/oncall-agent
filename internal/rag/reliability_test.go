package rag

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"oncall-agent/internal/store"
	"testing"
)

func TestRemoteEmbeddingRejectsFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"embedding":[1,2]}`))
	}))
	defer s.Close()
	_, err := NewOllamaEmbedder(s.URL, "model").Embed("x")
	if err == nil {
		t.Fatal("remote failure returned successful synthetic embedding")
	}
}
func TestRemoteEmbeddingCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewOllamaEmbedder("http://127.0.0.1:1", "model").EmbedContext(ctx, "x")
	if err != context.Canceled {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	}
}

func TestVersionStagingAndActivation(t *testing.T) {
	r := New(store.NewMemoryVector(), HashEmbedder{})
	ctx := context.Background()
	r.ActivateVersions(nil)
	cs, err := r.IndexVersion(ctx, "doc", "v1", "runbook", "# CPU\nCPU saturation recovery", "upload", "space")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := r.Search("CPU", 1)
	if err != nil || len(hits) != 0 {
		t.Fatalf("staging retrievable: %+v %v", hits, err)
	}
	r.ActivateVersions([]ActiveVersion{{DocID: "doc", VersionID: "v1", SpaceID: "space"}})
	hits, err = r.Search("CPU", 1)
	if err != nil || len(hits) != 1 || hits[0].VersionID != "v1" || hits[0].ChunkID != cs[0].ID {
		t.Fatalf("active lost: %+v %v", hits, err)
	}
	_, err = r.IndexVersion(ctx, "doc", "v2", "runbook", "# CPU\nCPU new version", "upload", "space")
	if err != nil {
		t.Fatal(err)
	}
	hits, _ = r.Search("CPU", 1)
	if len(hits) != 1 || hits[0].VersionID != "v1" {
		t.Fatalf("staging stole topK: %+v", hits)
	}
	r.ActivateVersions([]ActiveVersion{{DocID: "doc", VersionID: "v2", SpaceID: "space"}})
	hits, _ = r.Search("CPU", 1)
	if len(hits) != 1 || hits[0].VersionID != "v2" {
		t.Fatalf("old active returned: %+v", hits)
	}
}

func TestVersionSpaceDimensionAndDeletion(t *testing.T) {
	ctx := context.Background()
	r := New(store.NewMemoryVector(), HashEmbedder{})
	r.SetEmbeddingSpace("space", Dim)
	if _, err := r.IndexVersion(ctx, "d", "bad", "runbook", "# CPU\nCPU details", "upload", "other"); err == nil {
		t.Fatal("space mismatch accepted")
	}
	r.SetEmbeddingSpace("space", 3)
	if _, err := r.IndexVersion(ctx, "d", "bad", "runbook", "# CPU\nCPU details", "upload", "space"); err == nil {
		t.Fatal("dimension mismatch accepted")
	}
	r.SetEmbeddingSpace("space", Dim)
	for _, v := range []string{"v1", "v2"} {
		if _, err := r.IndexVersion(ctx, "d", v, "runbook", "# CPU\nCPU details", "upload", "space"); err != nil {
			t.Fatal(err)
		}
	}
	r.ActivateVersions([]ActiveVersion{{DocID: "d", VersionID: "v2", SpaceID: "space"}})
	if err := r.DeleteVersionWithContext(ctx, "v1"); err != nil {
		t.Fatal(err)
	}
	hits, err := r.Search("CPU", 1)
	if err != nil || len(hits) != 1 || hits[0].VersionID != "v2" {
		t.Fatalf("deleting historical version harmed active: %+v %v", hits, err)
	}
	if err := r.DeleteVersionWithContext(ctx, "v2"); err != nil {
		t.Fatal(err)
	}
	hits, err = r.Search("CPU", 1)
	if err != nil || len(hits) != 0 {
		t.Fatalf("deleted projection remained: %+v %v", hits, err)
	}
}
func TestRestoreChunksRestoresSparseProjection(t *testing.T) {
	r := New(store.NewMemoryVector(), HashEmbedder{})
	r.RestoreChunks([]VersionChunk{{ID: "c", DocID: "d", VersionID: "v", Title: "CPU", Snippet: "CPU saturation", Source: "upload", SpaceID: "s"}})
	r.ActivateVersions([]ActiveVersion{{DocID: "d", VersionID: "v", SpaceID: "s"}})
	hits, err := r.Search("CPU", 1)
	if err != nil || len(hits) != 1 || hits[0].BM25Score == nil || hits[0].DenseScore != nil {
		t.Fatalf("sparse restart projection not restored: %+v %v", hits, err)
	}
}
