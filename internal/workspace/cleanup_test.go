package workspace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
)

func TestCleanupDurableRetryRestartAndHistoricalChunks(t *testing.T) {
	s, path := fixtureService(t)
	ctx := context.Background()
	var deletes atomic.Int32
	var failing atomic.Bool
	failing.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/delete") {
			deletes.Add(1)
			if failing.Load() {
				w.WriteHeader(503)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[],"status":"ok"}`))
	}))
	defer server.Close()
	s.RAG = rag.New(store.NewVector(server.URL, "owned-cleanup"), rag.HashEmbedder{})
	s.RAG.SetEmbeddingSpace(s.SpaceID, 64)
	d, v, e := s.PutDocument(ctx, "", "CPU", "# CPU\nfixture knowledge", "upload", "test", "", "")
	if e != nil {
		t.Fatal(e)
	}
	d, e = s.DeleteDocument(ctx, d.ID, v.ID)
	if e != nil || d.IndexStatus != "cleanup_pending" {
		t.Fatalf("delete %+v %v", d, e)
	}
	var state, next string
	var attempts int
	if e = s.DB.SQL.QueryRow("SELECT state,attempts,next_at FROM outbox WHERE kind='cleanup'").Scan(&state, &attempts, &next); e != nil {
		t.Fatal("cleanup not durable", e)
	}
	if e = s.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	s.DB.SQL.QueryRow("SELECT state,attempts,next_at FROM outbox WHERE kind='cleanup'").Scan(&state, &attempts, &next)
	if state != "pending" || attempts != 1 || next <= stamp() {
		t.Fatalf("no retry/backoff: %s %d %s", state, attempts, next)
	}
	if e = s.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	if deletes.Load() != 1 {
		t.Fatal("ignored backoff")
	}
	s.DB.Close()
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s.DB = db
	failing.Store(false)
	if _, e = db.SQL.Exec("UPDATE outbox SET next_at=? WHERE kind='cleanup'", stamp()); e != nil {
		t.Fatal(e)
	}
	if e = s.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	d, e = db.Document(ctx, d.ID)
	if e != nil || d.IndexStatus != "removed" {
		t.Fatalf("cleanup not completed %+v %v", d, e)
	}
	db.SQL.QueryRow("SELECT state,attempts FROM outbox WHERE kind='cleanup'").Scan(&state, &attempts)
	if state != "sent" || attempts != 2 || deletes.Load() != 2 {
		t.Fatalf("%s %d deletes=%d", state, attempts, deletes.Load())
	}
	old, e := db.Version(ctx, v.ID)
	if e != nil || len(old.Chunks) == 0 {
		t.Fatal("historical version lost", e)
	}
	var chunks int
	db.SQL.QueryRow("SELECT count(*) FROM chunks WHERE version_id=?", v.ID).Scan(&chunks)
	if chunks == 0 {
		t.Fatal("historical SQL chunks lost")
	}
}
func TestCleanupLateDispatchDoesNotDeleteReactivatedVersion(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	d, v, e := s.PutDocument(ctx, "", "CPU", "# CPU\nfixture guidance", "upload", "test", "", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DeleteDocument(ctx, d.ID, v.ID); e != nil {
		t.Fatal(e)
	}
	_, reactivated, e := s.PutDocument(ctx, d.ID, d.Title, v.Content, "upload", "test", v.ID, "")
	if e != nil || reactivated.ID != v.ID {
		t.Fatalf("reactivate %v %+v", e, reactivated)
	}
	if e = s.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	hits, e := s.RAG.SearchWithContext(ctx, "CPU", 5)
	if e != nil || len(hits) == 0 {
		t.Fatalf("late cleanup removed active version %v %v", hits, e)
	}
	live, e := s.DB.Document(ctx, d.ID)
	if e != nil || live.DeletedAt != nil || live.IndexStatus != "indexed" {
		t.Fatalf("live state %+v %v", live, e)
	}
	if _, e = s.DeleteDocument(ctx, d.ID, v.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	live, e = s.DB.Document(ctx, d.ID)
	if e != nil || live.IndexStatus != "removed" {
		t.Fatalf("second delete cleanup %+v %v", live, e)
	}
}
func TestDocumentRetransmissionRepairsProjectionAndRejectsMetadataBypass(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	d, v, e := s.PutDocument(ctx, "", "CPU", "# CPU\nfixture guidance", "upload", "test", "", "create-key")
	if e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"create-key", ""} {
		s.RAG.ActivateVersions(nil)
		_, _, e = s.PutDocument(ctx, "", d.Title, v.Content, "upload", "test", "", key)
		if e != nil {
			t.Fatal(e)
		}
		hits, e := s.RAG.SearchWithContext(ctx, "CPU", 5)
		if e != nil || len(hits) == 0 {
			t.Fatalf("retransmission returned success with stale projection key=%q: %v %v", key, hits, e)
		}
	}
	for _, change := range []struct{ title, env string }{{"cpu", "test"}, {"CPU", "production"}} {
		_, _, e = s.PutDocument(ctx, "", change.title, v.Content, "upload", change.env, "", "")
		if !errors.Is(e, ErrConflict) {
			t.Fatalf("POST bypass metadata CAS: %v", e)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	s.RAG.ActivateVersions(nil)
	if e = s.restorePublishedProjection(cancelled); e != nil {
		t.Fatal("post-commit recovery obeyed client cancellation", e)
	}
	hits, e := s.RAG.SearchWithContext(ctx, "CPU", 5)
	if e != nil || len(hits) == 0 {
		t.Fatal("cancel window stranded committed projection", e)
	}
	// Ensure the bounded recovery does not mutate the caller's cancellation state.
	if !errors.Is(cancelled.Err(), context.Canceled) {
		t.Fatal("caller cancellation lost")
	}
}

func TestCleanupTombstoneAndOutboxRollbackTogether(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	d, v, err := s.PutDocument(ctx, "", "CPU", "# CPU\nfixture guidance", "upload", "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.SQL.Exec("CREATE TRIGGER reject_cleanup BEFORE INSERT ON outbox WHEN NEW.kind='cleanup' BEGIN SELECT RAISE(ABORT,'fixture cleanup rejection'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteDocument(ctx, d.ID, v.ID); err == nil {
		t.Fatal("failed cleanup admission reported success")
	}
	current, err := s.DB.Document(ctx, d.ID)
	if err != nil || current.DeletedAt != nil || current.IndexStatus != "indexed" {
		t.Fatalf("partial tombstone commit %+v %v", current, err)
	}
	hits, err := s.RAG.SearchWithContext(ctx, "CPU", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("transaction rollback lost current retrieval %+v %v", hits, err)
	}
	var jobs int
	if err = s.DB.SQL.QueryRow("SELECT count(*) FROM outbox WHERE kind='cleanup'").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("unexpected cleanup jobs %d %v", jobs, err)
	}
}
