package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"oncall-agent/internal/tool"
	"testing"
)

func TestLegacyPlanSynchronousDurableEvidence(t *testing.T) {
	e, h := newWorkspaceHTTPTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"alerts":[{"labels":{"alertname":"CPUHigh","environment":"test","service":"api"},"state":"firing","activeAt":"2026-10-06T00:00:00Z"}]}}`))
	}))
	defer upstream.Close()
	h.Prom = tool.NewPromClient(upstream.URL)
	e.GET("/plan", h.Plan)
	_, _, err := h.Service.PutDocument(context.Background(), "", "CPUHigh", "# CPUHigh\n人工核对指标，保留原始观测。", "upload", "test", "", "plan-doc")
	if err != nil {
		t.Fatal(err)
	}
	status, result := wsTestRequest(t, e, "GET", "/plan", "", nil)
	requireWSStatus(t, status, 200, result)
	if result["status"] != "done" || result["evidence_status"] != "has_citations" || len(result["citations"].([]any)) == 0 {
		t.Fatalf("unfinished or uncited legacy result %+v", result)
	}
	run, err := h.Service.DB.Run(context.Background(), result["id"].(string))
	if err != nil || run.Status != "succeeded" || len(run.CitationIDs) == 0 {
		t.Fatalf("facts not captured %+v %v", run, err)
	}
	status, duplicate := wsTestRequest(t, e, "GET", "/plan", "", nil)
	requireWSStatus(t, status, 200, duplicate)
	if result["id"] != duplicate["id"] {
		t.Fatal("GET retry duplicated diagnosis")
	}
	upstream.Close()
	status, result = wsTestRequest(t, e, "GET", "/plan", "", nil)
	requireWSStatus(t, status, 503, result)
	if result["code"] != "source_unavailable" {
		t.Fatal(result)
	}
}
