package workspace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
)

type readinessEmbed struct{ fail atomic.Bool }

func (e *readinessEmbed) Embed(s string) ([]float32, error) {
	return e.EmbedContext(context.Background(), s)
}
func (e *readinessEmbed) EmbedContext(ctx context.Context, s string) ([]float32, error) {
	if e.fail.Load() {
		return nil, errors.New("fixture embedding offline")
	}
	return rag.HashEmbedder{}.Embed(s)
}
func TestProbeRetrievalSourceRecoveryAndSpaceFence(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	identity := "fixture/hash"
	space := hash("space-v1:" + identity + ":64")
	vectors := store.NewMemoryVector()
	embed := &readinessEmbed{}
	s.SpaceID = space
	s.RAG = rag.New(vectors, embed)
	s.RAG.SetEmbeddingSpace(space, 64)
	d, v, err := s.PutDocument(ctx, "", "CPU", "# CPU\nfixture guidance", "upload", "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	recovered := &Service{DB: s.DB, RAG: rag.New(vectors, embed), SpaceID: "unavailable"}
	embed.fail.Store(true)
	if err = recovered.ProbeRetrieval(ctx, vectors, embed, identity); err == nil || recovered.SpaceID != "unavailable" {
		t.Fatalf("offline probe claimed ready %v %s", err, recovered.SpaceID)
	}
	embed.fail.Store(false)
	if err = recovered.ProbeRetrieval(ctx, vectors, embed, identity); err != nil || recovered.SpaceID != space {
		t.Fatalf("source recovery failed %v %s", err, recovered.SpaceID)
	}
	hits, err := recovered.RAG.SearchWithContext(ctx, "CPU", 5)
	if err != nil || len(hits) == 0 || hits[0].DocID != d.ID || hits[0].VersionID != v.ID {
		t.Fatalf("SQL projection not restored %v %v", hits, err)
	}
	if err = recovered.ProbeRetrieval(ctx, vectors, embed, "different-model"); !errors.Is(err, ErrConflict) || recovered.SpaceID != space {
		t.Fatalf("space replacement allowed %v %s", err, recovered.SpaceID)
	}
	t.Log("L1 explicit Memory/Hash: offline remains unavailable; recovery initializes fixed space + SQL active projection; different model rejected")
}
func TestProbeRetrievalBusyDoesNotWriteSpaceMarker(t *testing.T) {
	s, _ := fixtureService(t)
	s.SpaceID = "unavailable"
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT":
			writes.Add(1)
			w.Write([]byte(`{"result":{},"status":"ok"}`))
		case r.Method == "POST":
			w.Write([]byte(`{"result":{"count":0}}`))
		case r.URL.Path == "/collections/fixture":
			w.Write([]byte(`{"result":{"config":{"params":{"vectors":{"size":64}}}}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	s.knowledgeMu.Lock()
	err := s.ProbeRetrieval(context.Background(), store.NewVector(srv.URL, "fixture"), rag.HashEmbedder{}, "fixture/hash")
	s.knowledgeMu.Unlock()
	if !errors.Is(err, ErrProbeBusy) || writes.Load() != 0 || s.SpaceID != "unavailable" {
		t.Fatalf("busy probe mutated remote space: err=%v writes=%d space=%s", err, writes.Load(), s.SpaceID)
	}
	t.Log("L1 fake Qdrant HTTP busy probe: zero PUT and fixed unavailable state")
}
