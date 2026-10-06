package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"oncall-agent/internal/auth"
	"oncall-agent/internal/config"
	"oncall-agent/internal/queue"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
	"oncall-agent/internal/workspace"
)

// Explicit L2 profile uses real dependencies and only a declared test HashEmbedder.
func TestWorkspaceL2(t *testing.T) {
	if os.Getenv("ONCALL_L2") != "1" {
		t.Skip("requires ONCALL_L2=1 scripts/workspace-l2.sh")
	}
	ctx := context.Background()
	owner := os.Getenv("ONCALL_L2_OWNER")
	if !strings.HasPrefix(owner, "oncall-l2-") {
		t.Fatal("missing isolated owner")
	}
	var manifest struct {
		Owner      string `json:"owner"`
		Containers map[string]struct {
			ID string `json:"id"`
		} `json:"containers"`
	}
	b, err := os.ReadFile(os.Getenv("ONCALL_L2_MANIFEST"))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(b, &manifest) != nil || manifest.Owner != owner {
		t.Fatal("owner manifest mismatch")
	}
	stopContainer := func(name string) {
		t.Helper()
		id := manifest.Containers[name].ID
		label, e := exec.Command("docker", "inspect", "--format", `{{index .Config.Labels "oncall.l2.owner"}}`, id).Output()
		if e != nil || strings.TrimSpace(string(label)) != owner {
			t.Fatal("owner mismatch", e)
		}
		if e = exec.Command("docker", "stop", id).Run(); e != nil {
			t.Fatal(e)
		}
	}
	path := filepath.Join(t.TempDir(), "facts.sqlite")
	db, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	vectors := store.NewVector(os.Getenv("ONCALL_L2_QDRANT"), "isolated_l2")
	if err = vectors.EnsureCompatible(ctx, rag.Dim); err != nil {
		t.Fatal(err)
	}

	r := rag.New(vectors, rag.HashEmbedder{})
	r.SetEmbeddingSpace("L2-test-hash/64", rag.Dim)
	r.ActivateVersions(nil)
	q := queue.NewWorkspaceClient(os.Getenv("ONCALL_L2_REDIS"), owner, 0)
	defer q.Close()
	var notifications atomic.Int32
	notify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var report map[string]any
		if json.Unmarshal(payload, &report) != nil || report["id"] == nil {
			w.WriteHeader(400)
			return
		}
		for _, field := range []string{"alerts", "diagnosis", "citations", "status", "evidence_status", "notification_status", "received_at"} {
			if report[field] == nil {
				w.WriteHeader(400)
				return
			}
		}
		if report["diagnosis"] == "" || len(report["alerts"].([]any)) == 0 {
			w.WriteHeader(400)
			return
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("ONCALL_L2_MANIFEST")), "notification-payload.json"), payload, 0600); err != nil {
			w.WriteHeader(500)
			return
		}
		if notifications.Add(1) == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(204)
	}))
	defer notify.Close()
	s := &workspace.Service{DB: db, RAG: r, SpaceID: "unavailable", Queue: q, QueueReady: func(context.Context) bool { return q.Ping() == nil }, WebhookURL: notify.URL}
	if err = s.ProbeRetrieval(ctx, vectors, rag.HashEmbedder{}, "test:hash"); err != nil {
		t.Fatal(err)
	}

	var sourceHealthy atomic.Bool
	sourceHealthy.Store(true)
	s.SourceReady = func(context.Context) bool { return sourceHealthy.Load() }

	worker := queue.NewWorkspaceServer(os.Getenv("ONCALL_L2_REDIS"), owner, 0, s)
	if err = worker.Start(); err != nil {
		t.Fatal(err)
	}
	defer worker.Shutdown()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(auth.New("l2-console", "l2-webhook").Middleware())
	cfg := config.Default()
	_, src, _, _ := runtime.Caller(0)
	cfg.Metrics.TemplatesFile = filepath.Join(filepath.Dir(src), "../../config/metric_templates.json")
	cfg.Topology.File = ""
	h := RegisterWorkspace(e, s, tool.NewPromClient(os.Getenv("ONCALL_L2_PROM")), cfg)
	if err = h.RegisterM2(e, cfg); err != nil {
		t.Fatal(err)
	}
	e.POST("/alert", func(c *gin.Context) {
		raw, er := io.ReadAll(c.Request.Body)
		if er != nil {
			c.JSON(400, gin.H{"error": "read body"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		var packet map[string]any
		if json.Unmarshal(raw, &packet) == nil {
			if alerts, ok := packet["alerts"].([]any); ok && len(alerts) > 0 {
				first, _ := alerts[0].(map[string]any)
				labels, _ := first["labels"].(map[string]any)
				if labels["alertname"] == "AMCallbackTest" {
					status, _ := packet["status"].(string)
					if status != "firing" && status != "resolved" {
						status = "other"
					}
					if er = os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("ONCALL_L2_MANIFEST")), "am-callback-"+status+".json"), raw, 0600); er != nil {
						c.JSON(500, gin.H{"error": "capture artifact"})
						return
					}
					t.Log("actual AM callback status", status, "individual status", first["status"])
				}
			}
		}
		h.Alert(c)
	})
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(e)
	server.Listener = listener
	server.Start()
	server.URL = fmt.Sprintf("http://127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port)
	defer server.Close()
	currentVersion := ""
	request := func(method, path, token, key string, body any) (int, map[string]any) {
		t.Helper()
		payload, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		if method == "PUT" || method == "DELETE" {
			req.Header.Set("If-Match", currentVersion)
		}
		res, er := server.Client().Do(req)
		if er != nil {
			t.Fatal(er)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var out map[string]any
		if json.Unmarshal(raw, &out) != nil {
			t.Fatalf("non JSON %s", raw)
		}
		return res.StatusCode, out
	}
	docBody := map[string]string{"title": "CPUHigh", "content": "# CPUHigh\nL2 isolated test knowledge v1. Inspect original CPUHigh metrics manually.", "source_kind": "upload", "environment": "test"}
	status, docResp := request("POST", "/api/v1/documents", "l2-console", "l2-create", docBody)
	if status != 201 {
		t.Fatal(status, docResp)
	}
	doc := docResp["data"].(map[string]any)["document"].(map[string]any)
	docID := doc["id"].(string)
	version := docResp["data"].(map[string]any)["version"].(map[string]any)
	v1 := version["id"].(string)
	currentVersion = v1
	alert := func(name string) map[string]any {
		return map[string]any{"status": "firing", "alerts": []any{map[string]any{"status": "firing", "labels": map[string]string{"alertname": name, "environment": "test", "service": "api", "severity": "critical"}, "startsAt": "2026-10-06T00:00:00Z"}}}
	}
	runID := ""
	for i := 0; i < 50; i++ {
		st, out := request("POST", "/alert", "l2-webhook", "", alert("CPUHigh"))
		if st != 202 {
			t.Fatal(st, out)
		}
		id := out["id"].(string)
		if runID != "" && id != runID {
			t.Fatal("replay produced new run")
		}
		runID = id
	}
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			_ = s.Dispatch(ctx)
			if check() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		incs, _ := db.Incidents(ctx)
		runs, _ := db.Runs(ctx)
		snapshot, _ := json.MarshalIndent(map[string]any{"incidents": incs, "runs": runs}, "", "  ")
		_ = os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("ONCALL_L2_MANIFEST")), "timeout-sql-snapshot.json"), snapshot, 0600)
		t.Fatal("observable timed out; captured timeout-sql-snapshot.json")
	}
	wait(func() bool { run, _ := db.Run(ctx, runID); return run.Status == "succeeded" })
	first, _ := db.Run(ctx, runID)
	if len(first.CitationIDs) == 0 {
		t.Fatal("real Qdrant diagnosis has no citation")
	}
	var count int
	if err = db.SQL.QueryRow("SELECT count(*) FROM diagnosis_runs").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	t.Log("50 real HTTP webhook replays => one SQL run; real asynq worker succeeded with Qdrant citations", runID)
	docBody["content"] = "# CPUHigh\nL2 isolated test knowledge v2, revised verification. CPUHigh original metrics."
	status, out := request("PUT", "/api/v1/documents/"+docID, "l2-console", "l2-v2", docBody)
	if status != 200 {
		t.Fatal(status, out)
	}
	v2 := out["data"].(map[string]any)["version"].(map[string]any)["id"].(string)
	currentVersion = v2
	if v2 == v1 {
		t.Fatal("not new version")
	}
	db.Close()
	db, err = workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.DB = db
	r = rag.New(vectors, rag.HashEmbedder{})
	r.SetEmbeddingSpace(s.SpaceID, rag.Dim)
	s.RAG = r
	if err = s.RestoreProjection(ctx); err != nil {
		t.Fatal(err)
	}
	hits, err := r.SearchWithContext(ctx, "CPUHigh", 5)
	if err != nil || len(hits) == 0 {
		t.Fatal(hits, err)
	}
	for _, hit := range hits {
		if hit.VersionID != v2 {
			t.Fatalf("stale/unqualified real Qdrant result: %+v; active=%s", hit, v2)
		}
	}
	status, out = request("DELETE", "/api/v1/documents/"+docID, "l2-console", "l2-delete", nil)
	if status != 202 && status != 200 {
		t.Fatal(status, out)
	}
	hits, err = r.SearchWithContext(ctx, "CPUHigh", 5)
	if err != nil || len(hits) != 0 {
		t.Fatal("withdrawn hits", hits, err)
	}
	status, out = request("GET", "/api/v1/documents/"+docID+"/versions/"+v1, "l2-console", "", nil)
	if status != 200 {
		t.Fatal("historical version lost", status, out)
	}
	status, out = request("GET", "/api/v1/evidence/"+first.CitationIDs[0], "l2-console", "", nil)
	if status != 200 {
		t.Fatal("historical citation lost", status, out)
	}
	t.Log("SQL reopen RestoreProjection activates V2 only; withdrawal removes search while V1 and original evidence stay readable")
	// Generate real Prometheus HTTP counter samples across multiple scrapes.
	time.Sleep(3 * time.Second)
	for i := 0; i < 3; i++ {
		res, er := http.Get(os.Getenv("ONCALL_L2_PROM") + "/api/v1/query?query=up")
		if er != nil {
			t.Fatal(er)
		}
		res.Body.Close()
		time.Sleep(time.Second)
	}
	now := time.Now().UTC().Truncate(time.Second)
	metricPath := "/api/v1/incidents/" + first.IncidentIDs[0] + "/metrics?metric_id=request_rate&start=" + now.Add(-60*time.Second).Format(time.RFC3339) + "&end=" + now.Format(time.RFC3339) + "&step=15"
	status, out = request("GET", metricPath, "l2-console", "", nil)
	if status != 200 {
		t.Fatal(status, out)
	}
	series := out["data"].(map[string]any)["series"].([]any)
	if len(series) == 0 {
		t.Fatal("real Prom query empty", out)
	}
	metricBytes, _ := json.MarshalIndent(out, "", "  ")
	if err = os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("ONCALL_L2_MANIFEST")), "metrics-response.json"), metricBytes, 0600); err != nil {
		t.Fatal(err)
	}
	finite := 0
	for _, rawSeries := range series {
		for _, rawPair := range rawSeries.(map[string]any)["values"].([]any) {
			pair := rawPair.([]any)
			if pair[1] != nil {
				finite++
			}
		}
	}
	if finite == 0 {
		t.Fatal("real Prometheus returned no finite samples", out)
	}
	t.Log("real Prometheus range query returned series and finite points", len(series), finite)
	status, out = request("POST", "/alert", "l2-webhook", "", alert("NoMatchingKnowledge"))
	if status != 202 {
		t.Fatal(status, out)
	}
	failedID := out["id"].(string)
	wait(func() bool { run, _ := db.Run(ctx, failedID); return run.EvidenceStatus == "no_evidence" })
	noEvidence, _ := db.Run(ctx, failedID)
	if noEvidence.ReportText == nil || !strings.HasSuffix(strings.TrimSpace(*noEvidence.ReportText), workspace.SafeReport) || len(noEvidence.CitationIDs) != 0 {
		t.Fatalf("unsafe no evidence report text=%v %+v", noEvidence.ReportText, noEvidence)
	}
	t.Log("no knowledge => safe no_evidence via real worker")
	// Actual Alertmanager callback carries the isolated webhook credential and resolved lifecycle.
	amConfig := fmt.Sprintf(`route:
  receiver: isolated-go
  group_by: [alertname]
  group_wait: 1s
  group_interval: 1s
  repeat_interval: 10s
receivers:
- name: isolated-go
  webhook_configs:
  - url: http://host.docker.internal:%d/alert
    send_resolved: true
    http_config:
      authorization:
        type: Bearer
        credentials: l2-webhook
`, listener.Addr().(*net.TCPAddr).Port)
	configPath := filepath.Join(t.TempDir(), "alertmanager.yml")
	if err = os.WriteFile(configPath, []byte(amConfig), 0644); err != nil {
		t.Fatal(err)
	}
	if raw, er := exec.Command("docker", "cp", configPath, manifest.Containers["alertmanager"].ID+":/etc/alertmanager/alertmanager.yml").CombinedOutput(); er != nil {
		t.Fatal(er, string(raw))
	}
	reload, er := http.Post(os.Getenv("ONCALL_L2_AM")+"/-/reload", "application/json", nil)
	if er != nil {
		t.Fatal(er)
	}
	reload.Body.Close()
	if reload.StatusCode != 200 {
		t.Fatal("AM reload", reload.StatusCode)
	}
	amDate, er := http.ParseTime(reload.Header.Get("Date"))
	if er != nil {
		t.Fatal("missing Alertmanager HTTP Date", er)
	}
	hostNow := time.Now().UTC()
	clockEvidence := map[string]any{"alertmanager_http_date": amDate, "host_now": hostNow, "offset_ms": amDate.Sub(hostNow).Milliseconds()}
	amStarted := amDate.Add(-time.Minute)
	amEnd := amDate.Add(time.Minute)
	amSend := func(end time.Time) {
		t.Helper()
		body, _ := json.Marshal([]any{map[string]any{"labels": map[string]string{"alertname": "AMCallbackTest", "environment": "test", "service": "api", "severity": "critical"}, "startsAt": amStarted.Format(time.RFC3339Nano), "endsAt": end.Format(time.RFC3339Nano), "annotations": map[string]string{"description": "actual isolated Alertmanager callback"}}})
		mode := "firing"
		if end.Before(time.Now().UTC()) {
			mode = "resolved"
		}
		if er := os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("ONCALL_L2_MANIFEST")), "am-input-"+mode+".json"), body, 0600); er != nil {
			t.Fatal(er)
		}
		t.Log("actual Alertmanager input", mode, "starts", amStarted, "ends", end)
		res, er := http.Post(os.Getenv("ONCALL_L2_AM")+"/api/v2/alerts", "application/json", bytes.NewReader(body))
		if er != nil {
			t.Fatal(er)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			raw, _ := io.ReadAll(res.Body)
			t.Fatal("AM submit", res.StatusCode, string(raw))
		}
	}
	amSend(amEnd)
	findAM := func() (workspace.Incident, bool) {
		incs, er := db.Incidents(ctx)
		if er != nil {
			t.Fatal(er)
		}
		for _, inc := range incs {
			if inc.Name == "AMCallbackTest" {
				return inc, true
			}
		}
		return workspace.Incident{}, false
	}
	wait(func() bool { inc, ok := findAM(); return ok && inc.LifecycleStatus == "active" })
	beforeResolve, _ := findAM()
	var countBeforeResolve int
	if err = db.SQL.QueryRow("SELECT count(*) FROM diagnosis_runs").Scan(&countBeforeResolve); err != nil {
		t.Fatal(err)
	}
	clockResponse, er := http.Get(os.Getenv("ONCALL_L2_AM") + "/api/v2/status")
	if er != nil {
		t.Fatal(er)
	}
	clockResponse.Body.Close()
	resolveDate, er := http.ParseTime(clockResponse.Header.Get("Date"))
	if er != nil {
		t.Fatal(er)
	}
	resolvedEnd := resolveDate.Add(-5 * time.Second)
	clockEvidence["resolved_alertmanager_http_date"] = resolveDate
	clockEvidence["resolved_host_now"] = time.Now().UTC()
	clockEvidence["resolved_offset_ms"] = resolveDate.Sub(time.Now().UTC()).Milliseconds()
	clockRaw, _ := json.MarshalIndent(clockEvidence, "", "  ")
	if err = os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("ONCALL_L2_MANIFEST")), "alertmanager-clock.json"), clockRaw, 0600); err != nil {
		t.Fatal(err)
	}
	amSend(resolvedEnd)
	wait(func() bool { inc, ok := findAM(); return ok && inc.LifecycleStatus == "resolved" })
	inc, _ := findAM()
	var countAfterResolve int
	if err = db.SQL.QueryRow("SELECT count(*) FROM diagnosis_runs").Scan(&countAfterResolve); err != nil {
		t.Fatal(err)
	}
	if countAfterResolve != countBeforeResolve || inc.LatestRunID == nil || beforeResolve.LatestRunID == nil || *inc.LatestRunID != *beforeResolve.LatestRunID {
		t.Fatal("resolved callback created diagnosis run", countBeforeResolve, countAfterResolve)
	}
	rawResolved, er := os.ReadFile(filepath.Join(filepath.Dir(os.Getenv("ONCALL_L2_MANIFEST")), "am-callback-resolved.json"))
	if er != nil {
		t.Fatal(er)
	}
	var resolvedPacket map[string]any
	if er = json.Unmarshal(rawResolved, &resolvedPacket); er != nil {
		t.Fatal(er)
	}
	packetAlerts := resolvedPacket["alerts"].([]any)
	packetAlert := packetAlerts[0].(map[string]any)
	packetEnd, er := time.Parse(time.RFC3339Nano, packetAlert["endsAt"].(string))
	if er != nil || !packetEnd.Equal(resolvedEnd) {
		t.Fatal("resolved callback did not reflect submitted past EndsAt", packetAlert["endsAt"], resolvedEnd, er)
	}
	t.Log("resolution mode: submitted past EndsAt using Alertmanager HTTP Date; SQL run count unchanged", countAfterResolve)
	wait(func() bool {
		if inc.LatestRunID == nil {
			return false
		}
		run, er := db.Run(ctx, *inc.LatestRunID)
		return er == nil && run.Status == "succeeded"
	})
	amEvidence, _ := json.MarshalIndent(inc, "", "  ")
	if err = os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("ONCALL_L2_MANIFEST")), "alertmanager-resolved-incident.json"), amEvidence, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("real isolated Alertmanager Bearer webhook firing -> resolved callback persisted", inc.ID)

	stopContainer("qdrant")
	sourceHealthy.Store(false)
	if er := s.ProbeRetrieval(ctx, vectors, rag.HashEmbedder{}, "test:hash"); er == nil {
		t.Fatal("probe accepted stopped vector source")
	}
	status, out = request("POST", "/api/v1/documents", "l2-console", "source-down", map[string]string{"title": "source down", "content": "must fail", "source_kind": "upload"})
	if status != 503 {
		t.Fatal("source stop accepted", status, out)
	}
	status, out = request("POST", "/alert", "l2-webhook", "", alert("SourceFailure"))
	if status != 202 {
		t.Fatal(status, out)
	}
	sourceID := out["id"].(string)
	wait(func() bool {
		run, _ := db.Run(ctx, sourceID)
		return run.Status == "failed" && run.NotificationStatus == "sent"
	})
	if notifications.Load() < 2 {
		t.Fatal("independent notification worker did not deliver")
	}
	t.Log("Qdrant retrieval failure => failed report and independent real asynq notify worker retries HTTP500 then delivers full report JSON")
	events, er := db.Events(ctx, sourceID, 0, 200)
	if er != nil {
		t.Fatal(er)
	}
	maxAttempt := 0
	for _, event := range events {
		if event.Stage == "notification" && event.StageAttempt > maxAttempt {
			maxAttempt = event.StageAttempt
		}
	}
	if maxAttempt != 2 {
		t.Fatal("notification actual stage attempts", maxAttempt)
	}
	if er = exec.Command("docker", "start", manifest.Containers["qdrant"].ID).Run(); er != nil {
		t.Fatal(er)
	}
	wait(func() bool { return s.ProbeRetrieval(ctx, vectors, rag.HashEmbedder{}, "test:hash") == nil })
	sourceHealthy.Store(true)
	if er = s.ProbeRetrieval(ctx, vectors, rag.HashEmbedder{}, "different:test-provider"); er == nil {
		t.Fatal("probe accepted incompatible identity")
	}
	t.Log("production ProbeRetrieval stop failure -> restart success; incompatible provider remains rejected")
	stopContainer("redis")
	status, out = request("POST", "/alert", "l2-webhook", "", alert("QueueDownNew"))
	if status != 503 {
		t.Fatal("queue stop accepted", status, out)
	}
	var after int
	db.SQL.QueryRow("SELECT count(*) FROM diagnosis_runs").Scan(&after)
	if after != 4 {
		t.Fatal("queue stop created run", after)
	}
	t.Log("stopped Qdrant => document 503; stopped Redis => new webhook 503 and no new SQL run")

}
