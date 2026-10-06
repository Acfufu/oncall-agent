package workspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"oncall-agent/internal/auth"
	"oncall-agent/internal/config"
	"oncall-agent/internal/handler"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
	"oncall-agent/internal/workspace"
)

type integrationQueue struct {
	mu                sync.Mutex
	diagnoses         int
	notificationCalls int
	failNotification  bool
	payload           []byte
}

func (q *integrationQueue) EnqueueAlertDiagnosis(string, []tool.Alert) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.diagnoses++
	return nil
}
func (q *integrationQueue) EnqueueNotification(_ string, p []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.notificationCalls++
	if q.failNotification {
		return errors.New("controlled queue failure")
	}
	q.payload = append([]byte(nil), p...)
	return nil
}

type m0Fixture struct {
	s    *workspace.Service
	q    *integrationQueue
	http *httptest.Server
}

func newM0Fixture(t *testing.T) *m0Fixture {
	t.Helper()
	d, err := workspace.Open(filepath.Join(t.TempDir(), "facts.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	r := rag.New(store.NewMemoryVector(), rag.HashEmbedder{})
	r.SetEmbeddingSpace("test-space", rag.Dim)
	r.ActivateVersions(nil)
	q := &integrationQueue{}
	s := &workspace.Service{DB: d, RAG: r, SpaceID: "test-space", Queue: q, QueueReady: func(context.Context) bool { return true }, SourceReady: func(context.Context) bool { return true }}
	gin.SetMode(gin.TestMode)
	e := gin.New()
	a := auth.New("console-test", "webhook-test")
	e.Use(a.Middleware())
	h := handler.RegisterWorkspace(e, s, nil, config.Default())
	e.POST("/alert", h.Alert)
	e.GET("/reports", h.Reports)
	server := httptest.NewServer(e)
	t.Cleanup(server.Close)
	return &m0Fixture{s: s, q: q, http: server}
}
func (f *m0Fixture) req(t *testing.T, method, path, token string, body []byte, headers map[string]string) (int, map[string]any) {
	t.Helper()
	r, err := http.NewRequest(method, f.http.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	res, err := f.http.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var v map[string]any
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%d %s: %v", res.StatusCode, b, err)
	}
	return res.StatusCode, v
}
func alertBody(names ...string) []byte {
	alerts := []map[string]any{}
	for _, name := range names {
		alerts = append(alerts, map[string]any{"status": "firing", "labels": map[string]string{"alertname": name, "environment": "test", "service": "api", "severity": "critical"}, "annotations": map[string]string{"description": "original description " + name, "runbook_url": "https://controlled.invalid/" + name}, "startsAt": "2026-10-06T00:00:00Z"})
	}
	b, _ := json.Marshal(map[string]any{"status": "firing", "alerts": alerts})
	return b
}
func (f *m0Fixture) admit(t *testing.T, names ...string) string {
	t.Helper()
	status, v := f.req(t, "POST", "/alert", "webhook-test", alertBody(names...), nil)
	if status != 202 {
		t.Fatalf("admit %d %+v", status, v)
	}
	return v["id"].(string)
}
func (f *m0Fixture) document(t *testing.T, name string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"title": name, "content": "# " + name + "\nimmutable remedy for " + name, "source_kind": "upload", "environment": "test"})
	status, v := f.req(t, "POST", "/api/v1/documents", "console-test", b, map[string]string{"Idempotency-Key": "create-" + name})
	if status != 201 {
		t.Fatalf("document %d %+v", status, v)
	}
	return v["data"].(map[string]any)["document"].(map[string]any)["id"].(string)
}
func TestM0IntegrationFiftyWebhookReplays(t *testing.T) {
	f := newM0Fixture(t)
	var wg sync.WaitGroup
	ids := make(chan string, 50)
	errs := make(chan error, 50)
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			names := []string{"CPUHigh", "DiskFull"}
			if i%2 == 0 {
				names[0], names[1] = names[1], names[0]
			}
			var body map[string]any
			json.Unmarshal(alertBody(names...), &body)
			body["report_id"] = fmt.Sprintf("random-report-%d", i)
			b, _ := json.Marshal(body)
			r, _ := http.NewRequest("POST", f.http.URL+"/alert", bytes.NewReader(b))
			r.Header.Set("Authorization", "Bearer webhook-test")
			r.Header.Set("Content-Type", "application/json")
			res, err := f.http.Client().Do(r)
			if err != nil {
				errs <- err
				return
			}
			defer res.Body.Close()
			var v map[string]any
			json.NewDecoder(res.Body).Decode(&v)
			if res.StatusCode != 202 {
				errs <- fmt.Errorf("status %d: %+v", res.StatusCode, v)
				return
			}
			id, ok := v["id"].(string)
			if !ok {
				errs <- fmt.Errorf("missing id: %+v", v)
				return
			}
			ids <- id
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("replay minted another run")
		}
		id = got
	}
	for table, want := range map[string]int{"diagnosis_runs": 1, "outbox": 1, "incidents": 2, "run_incidents": 2, "alert_observations": 2} {
		var n int
		if err := f.s.DB.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, n, want, err)
		}
	}
	f.q.mu.Lock()
	calls := f.q.diagnoses
	f.q.mu.Unlock()
	if calls != 1 {
		t.Fatalf("diagnosis enqueue calls=%d", calls)
	}
}
func TestM0IntegrationTwoAlertsEvidenceAndReadBoundary(t *testing.T) {
	f := newM0Fixture(t)
	docs := map[string]string{"CPUHigh": f.document(t, "CPUHigh"), "DiskFull": f.document(t, "DiskFull")}
	id := f.admit(t, "CPUHigh", "DiskFull")
	if err := f.s.ProcessAlertDiagnosis(context.Background(), id, nil); err != nil {
		t.Fatal(err)
	}
	r, err := f.s.DB.Run(context.Background(), id)
	if err != nil || r.Status != "succeeded" || r.EvidenceStatus != "has_citations" {
		t.Fatalf("run %+v %v", r, err)
	}
	for _, incID := range r.IncidentIDs {
		inc, err := f.s.DB.Incident(context.Background(), incID)
		if err != nil {
			t.Fatal(err)
		}
		var rows int
		rs, err := f.s.DB.SQL.Query("SELECT evidence_id FROM report_citations WHERE run_id=? AND incident_id=?", id, incID)
		if err != nil {
			t.Fatal(err)
		}
		evIDs := []string{}
		for rs.Next() {
			var eid string
			rs.Scan(&eid)
			evIDs = append(evIDs, eid)
		}
		rs.Close()
		for _, eid := range evIDs {
			ev, err := f.s.DB.Evidence(context.Background(), eid)
			if err != nil || ev.DocumentID == nil || *ev.DocumentID != docs[inc.Name] || ev.VersionID == nil || ev.ChunkID == nil || ev.Metadata["incident_id"] != incID || ev.Metadata["attempt"] != float64(r.CurrentAttempt) {
				t.Fatalf("cross-alert citation %+v %v", ev, err)
			}
			rows++
			for _, tc := range []struct {
				token string
				want  int
			}{{"", 401}, {"webhook-test", 403}, {"console-test", 200}} {
				status, v := f.req(t, "GET", "/api/v1/evidence/"+eid, tc.token, nil, nil)
				if status != tc.want {
					t.Fatalf("evidence scope %d %+v", status, v)
				}
			}
		}
		if rows == 0 {
			t.Fatal("incident lacks qualified citation")
		}
		status, v := f.req(t, "GET", "/api/v1/incidents/"+incID+"/graph?run_id="+id, "console-test", nil, nil)
		if status != 200 {
			t.Fatalf("graph %d %+v", status, v)
		}
	}
	events, err := f.s.DB.Events(context.Background(), id, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		if e.Sequence != i+1 {
			t.Fatalf("event sequence gap: %+v", events)
		}
	}
}
func TestM0IntegrationNoEvidenceAndSourceUnavailable(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			f := newM0Fixture(t)
			if failed {
				f.s.SourceReady = func(context.Context) bool { return false }
			}
			id := f.admit(t, "CPUHigh")
			if err := f.s.ProcessAlertDiagnosis(context.Background(), id, nil); err != nil {
				t.Fatal(err)
			}
			r, err := f.s.DB.Run(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus, wantEvidence := "succeeded", "no_evidence"
			if failed {
				wantStatus, wantEvidence = "failed", "source_unavailable"
			}
			if r.Status != wantStatus || r.EvidenceStatus != wantEvidence || len(r.CitationIDs) != 0 || r.ReportText == nil {
				t.Fatalf("misclassified %+v", r)
			}
			status, v := f.req(t, "GET", "/api/v1/runs/"+id, "console-test", nil, nil)
			if status != 200 || v["data"].(map[string]any)["evidence_status"] != wantEvidence {
				t.Fatalf("read model %d %+v", status, v)
			}
		})
	}
}
func TestM0IntegrationNotificationQueueAndHTTPRecovery(t *testing.T) {
	f := newM0Fixture(t)
	var sendCount atomic.Int32
	var receivedMu sync.Mutex
	var received [][]byte
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedMu.Lock()
		received = append(received, b)
		receivedMu.Unlock()
		if sendCount.Add(1) == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	f.s.WebhookURL = receiver.URL
	f.s.SourceReady = func(context.Context) bool { return false }
	f.q.failNotification = true
	id := f.admit(t, "CPUHigh")
	if err := f.s.ProcessAlertDiagnosis(context.Background(), id, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := f.s.DB.SQL.QueryRow("SELECT state FROM outbox WHERE kind='notification'").Scan(&state); err != nil || state != "pending" {
		t.Fatalf("queue failure not durable: %s %v", state, err)
	}
	f.q.mu.Lock()
	f.q.failNotification = false
	f.q.mu.Unlock()
	f.s.DB.SQL.Exec("UPDATE outbox SET next_at='' WHERE kind='notification'")
	if err := f.s.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.q.mu.Lock()
	payload := append([]byte(nil), f.q.payload...)
	calls := f.q.notificationCalls
	f.q.mu.Unlock()
	if calls != 2 {
		t.Fatalf("notification dispatch calls=%d", calls)
	}
	var body struct {
		ID     string                  `json:"id"`
		Alerts []workspace.Observation `json:"alerts"`
	}
	if err := json.Unmarshal(payload, &body); err != nil || body.ID != id || len(body.Alerts) != 1 {
		t.Fatalf("notification payload %s %v", payload, err)
	}
	obs := body.Alerts[0]
	if obs.Name != "CPUHigh" || obs.Labels["service"] != "api" || obs.Annotations["runbook_url"] != "https://controlled.invalid/CPUHigh" || obs.Description != "original description CPUHigh" {
		t.Fatalf("original alert missing: %+v", obs)
	}
	if err := f.s.ProcessNotification(context.Background(), id, payload); err == nil {
		t.Fatal("HTTP500 notification falsely succeeded")
	}
	r, _ := f.s.DB.Run(context.Background(), id)
	if r.NotificationStatus != "pending" || r.Status != "failed" {
		t.Fatalf("notification changed diagnosis %+v", r)
	}
	if err := f.s.ProcessNotification(context.Background(), id, payload); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ProcessNotification(context.Background(), id, payload); err != nil {
		t.Fatal(err)
	}
	r, _ = f.s.DB.Run(context.Background(), id)
	if r.NotificationStatus != "sent" || sendCount.Load() != 2 {
		t.Fatalf("notification not recovered/idempotent %+v count=%d", r, sendCount.Load())
	}
	receivedMu.Lock()
	defer receivedMu.Unlock()
	for _, sent := range received {
		if !bytes.Equal(sent, payload) {
			t.Fatalf("HTTP notification payload changed: %s", sent)
		}
	}
}
func TestM0IntegrationCancellationPersistsTerminalFailure(t *testing.T) {
	f := newM0Fixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer remote.Close()
	defer close(release)
	f.s.RAG = rag.New(store.NewMemoryVector(), rag.NewOpenAIEmbedder(remote.URL, "controlled"))
	id := f.admit(t, "CPUHigh")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.s.ProcessAlertDiagnosis(ctx, id, nil) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("retrieval never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker ignored cancellation")
	}
	r, err := f.s.DB.Run(context.Background(), id)
	if err != nil || r.Status != "failed" || r.Error == nil || r.Error.Code != "timeout" {
		t.Fatalf("cancelled attempt left nonterminal: %+v err=%v", r, err)
	}
}
func TestM0IntegrationRecoveredAttemptCannotReuseOldCitation(t *testing.T) {
	f := newM0Fixture(t)
	f.document(t, "CPUHigh")
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	judge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Write([]byte(`{"choices":[{"message":{"content":"分数: 3"}}]}`))
	}))
	defer judge.Close()
	defer once.Do(func() { close(release) })
	f.s.Judge = config.OpenAIConfig{APIBase: judge.URL, APIKey: "controlled", Model: "controlled"}
	id := f.admit(t, "CPUHigh")
	done := make(chan error, 1)
	go func() { done <- f.s.ProcessAlertDiagnosis(context.Background(), id, nil) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first attempt not at judge")
	}
	next := &workspace.Service{DB: f.s.DB, RAG: f.s.RAG, SpaceID: f.s.SpaceID, Queue: f.q, SourceReady: func(context.Context) bool { return false }}
	if err := next.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := next.ProcessAlertDiagnosis(context.Background(), id, nil); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	select {
	case err := <-done:
		if !errors.Is(err, workspace.ErrFenced) {
			t.Fatalf("late worker published: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old worker failed to stop")
	}
	r, err := f.s.DB.Run(context.Background(), id)
	if err != nil || r.EvidenceStatus != "source_unavailable" || len(r.CitationIDs) != 0 {
		t.Errorf("new attempt mixed historical citations: %+v %v", r, err)
	}
	graph, err := f.s.Graph(context.Background(), r.IncidentIDs[0], id, 60, 120)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range graph.Edges {
		if edge.Relation == "cites" {
			t.Errorf("new report cites retired attempt evidence: %+v", edge)
		}
	}
}
