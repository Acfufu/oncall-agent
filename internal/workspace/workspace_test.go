package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
)

func fixtureService(t *testing.T) (*Service, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "facts.sqlite")
	d, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	r := rag.New(store.NewMemoryVector(), rag.HashEmbedder{})
	r.SetEmbeddingSpace("test/hash/64", 64)
	s := &Service{DB: d, RAG: r, SpaceID: "test/hash/64"}
	if e = s.RestoreProjection(context.Background()); e != nil {
		t.Fatal(e)
	}
	return s, p
}
func observations() []Observation {
	return []Observation{{Name: "CPUHigh", Status: "firing", StartsAt: "2026-10-06T00:00:00Z", Labels: map[string]string{"alertname": "CPUHigh", "environment": "test", "service": "api", "severity": "critical"}}}
}
func TestConcurrentAdmissionPersistentIdentity(t *testing.T) {
	s, path := fixtureService(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make(chan string, 50)
	errs := make(chan error, 50)
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _, e := s.DB.Admit(ctx, observations(), true)
			if e != nil {
				errs <- e
				return
			}
			ids <- r.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	first := ""
	for id := range ids {
		if first != "" && id != first {
			t.Fatal("duplicate run")
		}
		first = id
	}
	var n int
	s.DB.SQL.QueryRow("SELECT count(*) FROM outbox").Scan(&n)
	if n != 1 {
		t.Fatalf("outbox count %d", n)
	}
	s.DB.Close()
	d, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	r, dup, e := d.Admit(ctx, observations(), false)
	if e != nil || !dup || r.ID != first {
		t.Fatalf("restart duplicate: %v %+v", e, r)
	}
}
func TestVersionActivationHistoryAndTombstone(t *testing.T) {
	s, path := fixtureService(t)
	ctx := context.Background()
	d, v, e := s.PutDocument(ctx, "", "CPUHigh", "# CPUHigh\n## Long\nold historical remedy", "upload", "test", "", "key1")
	if e != nil {
		t.Fatal(e)
	}
	run, _, e := s.DB.Admit(ctx, observations(), true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessAlertDiagnosis(ctx, run.ID, nil); e != nil {
		t.Fatal(e)
	}
	r, e := s.DB.Run(ctx, run.ID)
	if e != nil || r.EvidenceStatus != "has_citations" {
		t.Fatalf("%+v %v", r, e)
	}
	if len(r.CitationIDs) == 0 {
		t.Fatal("missing citation")
	}
	ev, e := s.DB.Evidence(ctx, r.CitationIDs[0])
	if e != nil || *ev.VersionID != v.ID {
		t.Fatal("citation version")
	}
	d, v2, e := s.PutDocument(ctx, d.ID, d.Title, "# CPUHigh\nshort new content", "upload", "test", v.ID, "key2")
	if e != nil {
		t.Fatal(e)
	}
	hits, e := s.RAG.SearchWithContext(ctx, "CPUHigh", 5)
	if e != nil {
		t.Fatal(e)
	}
	for _, h := range hits {
		if h.VersionID != v2.ID {
			t.Fatal("superseded hit")
		}
	}
	_, e = s.DeleteDocument(ctx, d.ID, v2.ID)
	if e != nil {
		t.Fatal(e)
	}
	hits, e = s.RAG.SearchWithContext(ctx, "CPUHigh", 5)
	if e != nil || len(hits) != 0 {
		t.Fatalf("tombstone result %v %v", hits, e)
	}
	old, e := s.DB.Evidence(ctx, r.CitationIDs[0])
	if e != nil || *old.VersionID != v.ID {
		t.Fatal("history changed")
	}
	s.DB.Close()
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	docs, e := db.Documents(ctx)
	if e != nil || len(docs) != 1 || docs[0].DeletedAt == nil {
		t.Fatal("restart document state")
	}
}

type failEmbed struct{}

func (failEmbed) Embed(string) ([]float32, error) { return nil, errors.New("offline") }
func TestIndexFailureRetainsLastActive(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	d, v, e := s.PutDocument(ctx, "", "CPUHigh", "# CPUHigh\nv1", "upload", "test", "", "")
	if e != nil {
		t.Fatal(e)
	}
	s.RAG = rag.New(store.NewMemoryVector(), failEmbed{})
	s.RAG.SetEmbeddingSpace(s.SpaceID, 64)
	_, _, e = s.PutDocument(ctx, d.ID, d.Title, "# CPUHigh\nv2", "upload", "test", v.ID, "")
	if e == nil {
		t.Fatal("index falsely succeeded")
	}
	got, e := s.DB.Document(ctx, d.ID)
	if e != nil || *got.ActiveVersionID != v.ID {
		t.Fatal("old active lost")
	}
}
func TestResolvedAndLateFiringNeverReopens(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	o := observations()
	r, _, e := s.DB.Admit(ctx, o, true)
	if e != nil {
		t.Fatal(e)
	}
	o[0].Status = "resolved"
	o[0].EndsAt = "2026-10-06T01:00:00Z"
	res, _, e := s.DB.Admit(ctx, o, false)
	if e != nil || res != nil {
		t.Fatal("resolved scheduled diagnosis")
	}
	o[0].Status = "firing"
	r2, dup, e := s.DB.Admit(ctx, o, false)
	if e != nil || !dup || r2.ID != r.ID {
		t.Fatal("late firing duplicate")
	}
	inc, e := s.DB.Incident(ctx, r.IncidentIDs[0])
	if e != nil || inc.LifecycleStatus != "resolved" {
		t.Fatal("late firing reopened")
	}
}
func TestFencedAttemptCannotPublish(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	r, _, e := s.DB.Admit(ctx, observations(), true)
	if e != nil {
		t.Fatal(e)
	}
	old, _, e := s.claim(ctx, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	newRun, ok, e := s.claim(ctx, r.ID)
	if e != nil || !ok {
		t.Fatal(e)
	}
	if e = s.finish(ctx, old, time.Now(), "succeeded", "no_evidence", "old", nil, nil); !errors.Is(e, ErrFenced) {
		t.Fatalf("late attempt wrote: %v", e)
	}
	if e = s.finish(ctx, newRun, time.Now(), "succeeded", "no_evidence", SafeReport, nil, nil); e != nil {
		t.Fatal(e)
	}
	again, ok, e := s.claim(ctx, r.ID)
	if e != nil || ok || again.Status != "succeeded" {
		t.Fatal("completed rerun")
	}
}

type fakeQueue struct {
	calls int
	fail  bool
	mu    sync.Mutex
}

func (q *fakeQueue) EnqueueAlertDiagnosis(string, []tool.Alert) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.calls++
	if q.fail {
		return errors.New("redis offline")
	}
	return nil
}
func (q *fakeQueue) EnqueueNotification(string, []byte) error { return nil }
func TestDurableOutboxRecoversAfterDispatchFailure(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	q := &fakeQueue{fail: true}
	s.Queue = q
	r, _, e := s.DB.Admit(ctx, observations(), true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	s.DB.SQL.QueryRow("SELECT state FROM outbox").Scan(&state)
	if state != "pending" {
		t.Fatal("failed dispatch lost")
	}
	q.fail = false
	s.DB.SQL.Exec("UPDATE outbox SET next_at='' ")
	if e = s.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	s.DB.SQL.QueryRow("SELECT state FROM outbox").Scan(&state)
	got, _ := s.DB.Run(ctx, r.ID)
	if state != "sent" || got.DispatchState != "enqueued" {
		t.Fatal("not recovered")
	}
}
func TestGraphScopedToIncidentAndRun(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	o := observations()
	o = append(o, Observation{Name: "Other", Status: "firing", StartsAt: o[0].StartsAt, Labels: map[string]string{"alertname": "Other"}})
	r, _, e := s.DB.Admit(ctx, o, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessAlertDiagnosis(ctx, r.ID, nil); e != nil {
		t.Fatal(e)
	}
	g, e := s.Graph(ctx, r.IncidentIDs[0], r.ID, 60, 120)
	if e != nil {
		t.Fatal(e)
	}
	visible := map[string]bool{}
	for _, n := range g.Nodes {
		visible[n.ID] = true
		if len(n.SourceRefs) == 0 {
			t.Fatal("missing source")
		}
	}
	for _, ed := range g.Edges {
		if !visible[ed.Source] || !visible[ed.Target] || len(ed.SourceRefs) == 0 {
			t.Fatal("dangling or unsourced edge")
		}
	}
	_, e = s.Graph(ctx, r.IncidentIDs[0], "other-run", 60, 120)
	if e == nil {
		t.Fatal("cross run accepted")
	}
}
func TestBackupRestoreFacts(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	r, _, e := s.DB.Admit(ctx, observations(), true)
	if e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(t.TempDir(), "backup.sqlite")
	if e = s.DB.Backup(ctx, backup); e != nil {
		t.Fatal(e)
	}
	d, e := Open(backup)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	got, e := d.Run(ctx, r.ID)
	if e != nil || got.ID != r.ID {
		t.Fatal("backup lost facts")
	}
}

func TestKnowledgeMetadataUpdateAndHistoricalVersionRemainStable(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	d, v1, e := s.PutDocument(ctx, "", "CPUHigh original", "# CPUHigh\nV1 recorded source", "upload", "test", "", "meta-create")
	if e != nil {
		t.Fatal(e)
	}
	updated, v2, e := s.PutDocument(ctx, d.ID, "CPUHigh renamed", v1.Content, "upload", "prod", v1.ID, "")
	if e != nil {
		t.Fatal(e)
	}
	if updated.Title != "CPUHigh renamed" || updated.Environment != "prod" || v2.ID == v1.ID {
		t.Fatalf("metadata silently ignored %+v %+v", updated, v2)
	}
	_, _, e = s.PutDocument(ctx, d.ID, "CPUHigh original", v1.Content, "upload", "test", v2.ID, "")
	if e != nil {
		t.Fatal(e)
	}
	historical, e := s.DB.Version(ctx, v1.ID)
	if e != nil {
		t.Fatal(e)
	}
	if historical.Content != v1.Content || historical.CreatedAt != v1.CreatedAt {
		t.Fatalf("immutable historical version overwritten: old=%+v now=%+v", v1, historical)
	}
}

func TestLegacyReportUsesRunTimeObservationAndCitationSnapshots(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	d, v, e := s.PutDocument(ctx, "", "CPUHigh original", "# CPUHigh\nOriginal immutable excerpt", "upload", "test", "", "snapshot-doc")
	if e != nil {
		t.Fatal(e)
	}
	original := observations()
	original[0].Description = "original observation"
	r, _, e := s.DB.Admit(ctx, original, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessAlertDiagnosis(ctx, r.ID, nil); e != nil {
		t.Fatal(e)
	}
	_, _, e = s.PutDocument(ctx, d.ID, "CPUHigh renamed", "# CPUHigh\nLater excerpt", "upload", "test", v.ID, "")
	if e != nil {
		t.Fatal(e)
	}
	later := observations()
	later[0].Status = "resolved"
	later[0].EndsAt = "2026-10-06T01:00:00Z"
	later[0].Description = "later resolved observation"
	if _, _, e = s.DB.Admit(ctx, later, false); e != nil {
		t.Fatal(e)
	}
	rep, e := s.LegacyReport(ctx, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	alerts := rep["alerts"].([]map[string]any)
	if len(alerts) != 1 || alerts[0]["description"] != "original observation" || alerts[0]["status"] != "firing" {
		t.Fatalf("historical alerts changed %+v", alerts)
	}
	cites := rep["citations"].([]map[string]any)
	if len(cites) == 0 || cites[0]["doc"] != "CPUHigh original" {
		t.Fatalf("historical citation title changed %+v", cites)
	}
}
