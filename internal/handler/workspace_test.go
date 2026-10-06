package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"oncall-agent/internal/config"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
	"oncall-agent/internal/workspace"
	"path/filepath"
	"strings"
	"testing"
)

type workspaceTestQueue struct{}

func (workspaceTestQueue) EnqueueAlertDiagnosis(string, []tool.Alert) error { return nil }
func (workspaceTestQueue) EnqueueNotification(string, []byte) error         { return nil }
func newWorkspaceHTTPTest(t *testing.T) (*gin.Engine, *WorkspaceHTTP) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	d, e := workspace.Open(filepath.Join(t.TempDir(), "facts.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	r := rag.New(store.NewMemoryVector(), rag.HashEmbedder{})
	r.SetEmbeddingSpace("test", rag.Dim)
	s := &workspace.Service{DB: d, RAG: r, SpaceID: "test", Queue: workspaceTestQueue{}, QueueReady: func(context.Context) bool { return true }, SourceReady: func(context.Context) bool { return true }}
	r.ActivateVersions(nil)
	router := gin.New()
	h := RegisterWorkspace(router, s, nil, config.Default())
	router.POST("/alert", h.Alert)
	router.GET("/reports", h.Reports)
	router.POST("/upload", h.Upload)
	router.GET("/list", h.List)
	router.DELETE("/delete", h.Delete)
	router.POST("/reindex", h.Reindex)
	return router, h
}
func wsTestRequest(t *testing.T, e *gin.Engine, method, url, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	var decoded map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("response status=%d raw=%s: %v", w.Code, w.Body.String(), err)
	}
	return w.Code, decoded
}
func requireWSStatus(t *testing.T, status, want int, v any) {
	t.Helper()
	if status != want {
		t.Fatalf("status %d want %d body %+v", status, want, v)
	}
}
func TestWorkspaceDocumentVersionAndLegacyAdapters(t *testing.T) {
	e, h := newWorkspaceHTTPTest(t)
	body := `{"title":"CPU","content":"# CPU\nCPU saturation guidance","source_kind":"upload"}`
	status, v := wsTestRequest(t, e, "POST", "/api/v1/documents", body, nil)
	requireWSStatus(t, status, 400, v)
	status, v = wsTestRequest(t, e, "POST", "/api/v1/documents", body, map[string]string{"Idempotency-Key": "create"})
	requireWSStatus(t, status, 201, v)
	data := v["data"].(map[string]any)
	doc := data["document"].(map[string]any)
	version := data["version"].(map[string]any)
	id := doc["id"].(string)
	vid := version["id"].(string)
	if meta := v["meta"].(map[string]any); meta["mode"] != "live" || meta["request_id"] == "" {
		t.Fatalf("bad meta %+v", meta)
	}
	status, v = wsTestRequest(t, e, "POST", "/api/v1/documents", strings.Replace(body, "saturation", "different", 1), map[string]string{"Idempotency-Key": "create"})
	requireWSStatus(t, status, 409, v)
	status, v = wsTestRequest(t, e, "POST", "/api/v1/documents", strings.Replace(body, `"CPU"`, `"Disk"`, 1), map[string]string{"Idempotency-Key": "create"})
	requireWSStatus(t, status, 409, v)
	status, v = wsTestRequest(t, e, "PUT", "/api/v1/documents/"+id, body, nil)
	requireWSStatus(t, status, 400, v)
	body2 := strings.Replace(body, "saturation", "new version", 1)
	status, v = wsTestRequest(t, e, "PUT", "/api/v1/documents/"+id, body2, map[string]string{"If-Match": vid})
	requireWSStatus(t, status, 200, v)
	newvid := v["data"].(map[string]any)["version"].(map[string]any)["id"].(string)
	status, v = wsTestRequest(t, e, "PUT", "/api/v1/documents/"+id, body, map[string]string{"If-Match": vid})
	requireWSStatus(t, status, 409, v)
	status, v = wsTestRequest(t, e, "GET", "/api/v1/documents/"+id+"/versions/"+vid, "", nil)
	requireWSStatus(t, status, 200, v)
	if !strings.Contains(v["data"].(map[string]any)["content"].(string), "saturation") {
		t.Fatal("historical source replaced")
	}
	status, v = wsTestRequest(t, e, "GET", "/list", "", nil)
	requireWSStatus(t, status, 200, v)
	if len(v["titles"].([]any)) != 1 {
		t.Fatal(v)
	}
	status, v = wsTestRequest(t, e, "DELETE", "/api/v1/documents/"+id, "", map[string]string{"If-Match": newvid})
	requireWSStatus(t, status, 202, v)
	deleted := v["data"].(map[string]any)
	if deleted["index_status"] != "cleanup_pending" || deleted["deleted_at"] == nil {
		t.Fatalf("delete did not expose durable cleanup %+v", v)
	}
	hits, err := h.Service.RAG.Search("CPU", 3)
	if err != nil || len(hits) != 0 {
		t.Fatalf("deleted currently retrieved: %+v %v", hits, err)
	}
	if err := h.Service.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := h.Service.DB.Document(context.Background(), id)
	if err != nil || current.IndexStatus != "removed" {
		t.Fatalf("durable cleanup did not finish %+v %v", current, err)
	}
	status, v = wsTestRequest(t, e, "GET", "/api/v1/documents/"+id+"/versions/"+vid, "", nil)
	requireWSStatus(t, status, 200, v)
	historical := v["data"].(map[string]any)
	if !strings.Contains(historical["content"].(string), "saturation") || len(historical["chunks"].([]any)) == 0 {
		t.Fatalf("cleanup destroyed historical source %+v", historical)
	}
	status, v = wsTestRequest(t, e, "POST", "/upload", `{"title":"Disk","content":"# Disk\nDisk full guidance"}`, nil)
	requireWSStatus(t, status, 200, v)
	status, v = wsTestRequest(t, e, "DELETE", "/delete?title=Disk", "", nil)
	requireWSStatus(t, status, 202, v)
	if v["index_status"] != "cleanup_pending" {
		t.Fatalf("legacy cleanup state missing %+v", v)
	}
	if err := h.Service.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, v = wsTestRequest(t, e, "POST", "/reindex", `{"confirm":false}`, nil)
	requireWSStatus(t, status, 400, v)
}
func wsAdmissionBody(name, status string) string {
	b, _ := json.Marshal(map[string]any{"status": status, "alerts": []any{map[string]any{"status": status, "labels": map[string]string{"alertname": name, "environment": "prod", "service": "api"}, "annotations": map[string]string{"description": "CPU high"}, "startsAt": "2026-10-06T10:00:00Z", "endsAt": "2026-10-06T11:00:00Z"}}})
	return string(b)
}
func TestWorkspaceAdmissionPaginationGraphAndEvents(t *testing.T) {
	e, h := newWorkspaceHTTPTest(t)
	status, v := wsTestRequest(t, e, "POST", "/alert", wsAdmissionBody("CPUHigh", "firing"), nil)
	requireWSStatus(t, status, 202, v)
	runID := v["id"].(string)
	h.Service.QueueReady = func(context.Context) bool { return false }
	status, v = wsTestRequest(t, e, "POST", "/alert", wsAdmissionBody("CPUHigh", "firing"), nil)
	requireWSStatus(t, status, 202, v)
	if v["id"] != runID || v["duplicate"] != true {
		t.Fatal("duplicate failed during outage", v)
	}
	status, v = wsTestRequest(t, e, "POST", "/alert", wsAdmissionBody("NewAlert", "firing"), nil)
	requireWSStatus(t, status, 503, v)
	status, v = wsTestRequest(t, e, "POST", "/alert", wsAdmissionBody("CPUHigh", "resolved"), nil)
	requireWSStatus(t, status, 200, v)
	if v["diagnosis_enqueued"] != false {
		t.Fatal("resolved inference admitted")
	}
	h.Service.QueueReady = func(context.Context) bool { return true }
	wsTestRequest(t, e, "POST", "/alert", wsAdmissionBody("DiskFull", "firing"), nil)
	status, v = wsTestRequest(t, e, "GET", "/api/v1/incidents?limit=1", "", nil)
	requireWSStatus(t, status, 200, v)
	if len(v["data"].([]any)) != 1 {
		t.Fatal(v)
	}
	cursor := v["meta"].(map[string]any)["next_cursor"].(string)
	incidentID := v["data"].([]any)[0].(map[string]any)["id"].(string)
	status, v = wsTestRequest(t, e, "GET", "/api/v1/incidents?limit=1&cursor="+cursor, "", nil)
	requireWSStatus(t, status, 200, v)
	if len(v["data"].([]any)) != 1 {
		t.Fatal(v)
	}
	status, v = wsTestRequest(t, e, "GET", "/api/v1/incidents?environment=prod&cursor="+cursor, "", nil)
	requireWSStatus(t, status, 400, v)
	status, v = wsTestRequest(t, e, "GET", "/api/v1/incidents/"+incidentID+"/graph?node_limit=301", "", nil)
	requireWSStatus(t, status, 400, v)
	status, v = wsTestRequest(t, e, "GET", "/api/v1/incidents/"+incidentID+"/graph?focus_id=foreign", "", nil)
	requireWSStatus(t, status, 400, v)
	status, v = wsTestRequest(t, e, "GET", "/api/v1/runs/"+runID+"/events?limit=1", "", nil)
	requireWSStatus(t, status, 200, v)
	if v["meta"].(map[string]any)["next_after_sequence"].(float64) < 1 {
		t.Fatal(v)
	}
	status, v = wsTestRequest(t, e, "GET", "/api/v1/runs/"+runID+"/events?after_sequence=-1", "", nil)
	requireWSStatus(t, status, 400, v)
	status, v = wsTestRequest(t, e, "GET", "/reports", "", nil)
	requireWSStatus(t, status, 200, v)
	if len(v["reports"].([]any)) != 2 {
		t.Fatal(v)
	}
	status, v = wsTestRequest(t, e, "GET", "/api/v1/workspace/summary", "", nil)
	requireWSStatus(t, status, 200, v)
	if v["data"].(map[string]any)["mean_diagnosis_ms"] != nil {
		t.Fatal("unobserved latency fabricated", v)
	}
}
func TestWorkspaceHTTPBodyAndSystemFailure(t *testing.T) {
	e, h := newWorkspaceHTTPTest(t)
	status, v := wsTestRequest(t, e, "POST", "/alert", string(bytes.Repeat([]byte("x"), 1<<20+1)), nil)
	requireWSStatus(t, status, 413, v)
	status, v = wsTestRequest(t, e, "GET", "/api/v1/system/status", "", nil)
	requireWSStatus(t, status, 200, v)
	raw, _ := json.Marshal(v)
	if bytes.Contains(raw, []byte("127.0.0.1")) || bytes.Contains(raw, []byte("api_key")) {
		t.Fatal("status leaks configuration")
	}
	h.Service.DB.Close()
	status, v = wsTestRequest(t, e, "GET", "/api/v1/system/status", "", nil)
	requireWSStatus(t, status, 503, v)
}
func TestWorkspaceDocumentReadErrorIsNotEmpty(t *testing.T) {
	e, h := newWorkspaceHTTPTest(t)
	h.Service.DB.Close()
	for _, path := range []string{"/api/v1/incidents", "/api/v1/documents", "/api/v1/workspace/summary", "/list", "/reports"} {
		status, v := wsTestRequest(t, e, "GET", path, "", nil)
		requireWSStatus(t, status, 503, v)
		if _, ok := v["data"]; ok {
			t.Fatal("failed storage represented empty success")
		}
	}
}
func TestWorkspaceInvalidObservationTimes(t *testing.T) {
	e, _ := newWorkspaceHTTPTest(t)
	for _, body := range []string{strings.Replace(wsAdmissionBody("CPU", "firing"), "2026-10-06T10:00:00Z", "invalid", 1), strings.Replace(wsAdmissionBody("CPU", "resolved"), "2026-10-06T11:00:00Z", "2026-10-06T09:00:00Z", 1)} {
		status, v := wsTestRequest(t, e, "POST", "/alert", body, nil)
		requireWSStatus(t, status, 400, v)
	}
}
func TestWorkspaceLegacyUploadReplacesOnlyUploadNamespace(t *testing.T) {
	e, h := newWorkspaceHTTPTest(t)
	status, v := wsTestRequest(t, e, "POST", "/upload", `{"title":"CPU","content":"# CPU\nold human guidance"}`, nil)
	requireWSStatus(t, status, 200, v)
	old := v["active_version_id"].(string)
	status, v = wsTestRequest(t, e, "POST", "/upload", `{"title":"CPU","content":"# CPU\nnew human guidance"}`, nil)
	requireWSStatus(t, status, 200, v)
	if v["active_version_id"] == old {
		t.Fatal("replacement did not create version")
	}
	items, err := h.Service.DB.Documents(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("duplicate documents %+v %v", items, err)
	}
	status, v = wsTestRequest(t, e, "GET", "/api/v1/documents/"+items[0].ID+"/versions/"+old, "", nil)
	requireWSStatus(t, status, 200, v)
}
func TestWorkspaceManualRunIdempotencyAndVersionBinding(t *testing.T) {
	e, h := newWorkspaceHTTPTest(t)
	status, v := wsTestRequest(t, e, "POST", "/alert", wsAdmissionBody("CPUHigh", "firing"), nil)
	requireWSStatus(t, status, 202, v)
	firstRun := v["id"].(string)
	run, err := h.Service.DB.Run(context.Background(), firstRun)
	if err != nil {
		t.Fatal(err)
	}
	id := run.IncidentIDs[0]
	status, v = wsTestRequest(t, e, "POST", "/api/v1/incidents/"+id+"/runs", `{"a":1,"b":2}`, nil)
	requireWSStatus(t, status, 400, v)
	headers := map[string]string{"Idempotency-Key": "manual"}
	status, v = wsTestRequest(t, e, "POST", "/api/v1/incidents/"+id+"/runs", `{"a":1,"b":2}`, headers)
	requireWSStatus(t, status, 202, v)
	manual := v["data"].(map[string]any)["run_id"].(string)
	h.Service.QueueReady = func(context.Context) bool { return false }
	status, v = wsTestRequest(t, e, "POST", "/api/v1/incidents/"+id+"/runs", `{"b":2,"a":1}`, headers)
	requireWSStatus(t, status, 202, v)
	if v["data"].(map[string]any)["run_id"] != manual {
		t.Fatal("canonical JSON replay created another run")
	}
	status, v = wsTestRequest(t, e, "POST", "/api/v1/incidents/"+id+"/runs", `{"a":3}`, headers)
	requireWSStatus(t, status, 409, v)
	h.Service.QueueReady = func(context.Context) bool { return true }
	status, v = wsTestRequest(t, e, "POST", "/alert", wsAdmissionBody("DiskFull", "firing"), nil)
	requireWSStatus(t, status, 202, v)
	other := v["id"].(string)
	status, v = wsTestRequest(t, e, "GET", "/api/v1/incidents/"+id+"/graph?run_id="+other, "", nil)
	requireWSStatus(t, status, 404, v)
	status, v = wsTestRequest(t, e, "GET", "/api/v1/incidents/"+id+"/graph?run_id="+manual, "", nil)
	requireWSStatus(t, status, 200, v)
	data := v["data"].(map[string]any)
	if data["run_id"] != manual || data["incident_id"] != id {
		t.Fatal("graph mixed scope", data)
	}
	for _, node := range data["nodes"].([]any) {
		n := node.(map[string]any)
		for _, ref := range n["source_refs"].([]any) {
			status, ev := wsTestRequest(t, e, "GET", "/api/v1/evidence/"+ref.(string), "", nil)
			requireWSStatus(t, status, 200, ev)
			if metadata := ev["data"].(map[string]any)["metadata"].(map[string]any); metadata["run_id"] != manual {
				t.Fatal("graph evidence from other run")
			}
		}
	}
}
