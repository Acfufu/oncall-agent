package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	s := store.NewMemoryVector()
	r := rag.New(s, nil)
	// 沉淀一篇 demo 知识供告警命中。
	md := "# CPU 高负载处置\n## 现象\nCPU 使用率持续大于 90%\n## 处置\n限流扩容排查热点"
	if err := r.AddDoc("cpu_high_usage.md", md, "demo"); err != nil {
		t.Fatal(err)
	}
	h := New(s, r, "")
	h.PlannerAgent = agent.New(nil, r)
	return h
}

func postAlert(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.POST("/alert", h.Alert)
	e.GET("/reports", h.Reports)
	req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w
}

// AM webhook 标准形状：alerts[] + labels/annotations，status=firing。
const amPayload = `{
  "alerts": [{
    "status": "firing",
    "labels": {"alertname": "CPUHighUsage", "severity": "critical", "job": "api"},
    "annotations": {"description": "CPU 使用率持续大于 90%"},
    "startsAt": "2026-09-26T10:00:00Z"
  }]
}`

func TestAlertAMPayloadDiagnosesAndRecords(t *testing.T) {
	h := newTestHandler(t)
	w := postAlert(t, h, amPayload)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Received  int    `json:"received"`
		Diagnosis string `json:"diagnosis"`
		Citations []struct {
			Doc string `json:"doc"`
		} `json:"citations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Received != 1 {
		t.Fatalf("received=%d", resp.Received)
	}
	if !strings.Contains(resp.Diagnosis, "CPUHighUsage") {
		t.Fatalf("diagnosis missing alertname: %s", resp.Diagnosis)
	}
	if len(resp.Citations) == 0 || resp.Citations[0].Doc != "cpu_high_usage.md" {
		t.Fatalf("citations not hitting demo doc: %+v", resp.Citations)
	}
	reps := h.reports.snapshot()
	if len(reps) != 1 || len(reps[0].Alerts) != 1 || reps[0].Alerts[0].Name != "CPUHighUsage" {
		t.Fatalf("report ring wrong: %+v", reps)
	}
	if reps[0].ReceivedAt == "" {
		t.Fatal("received_at empty")
	}
}

func TestAlertResolvedSkipped(t *testing.T) {
	h := newTestHandler(t)
	w := postAlert(t, h, `{"alerts":[{"status":"resolved","labels":{"alertname":"X"}}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("resolved-only payload should 400, got %d", w.Code)
	}
}

func TestAlertSingleShapes(t *testing.T) {
	h := newTestHandler(t)
	// 单条 amAlert 形状。
	w := postAlert(t, h, `{"status":"firing","labels":{"alertname":"DiskFull","severity":"warn"},"annotations":{"description":"磁盘 95%"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("single amAlert shape failed: %d %s", w.Code, w.Body.String())
	}
	// tool.Alert 直填形状。
	w = postAlert(t, h, `{"name":"MQBacklog","severity":"critical","description":"消费堆积"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("tool.Alert shape failed: %d %s", w.Code, w.Body.String())
	}
	if got := len(h.reports.snapshot()); got != 2 {
		t.Fatalf("ring size=%d want 2", got)
	}
}

func TestAlertBadPayloads(t *testing.T) {
	h := newTestHandler(t)
	for _, body := range []string{``, `{"foo":1}`, `{"alerts":[{"labels":{}}]}`} {
		w := postAlert(t, h, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%q should 400, got %d", body, w.Code)
		}
	}
	if got := len(h.reports.snapshot()); got != 0 {
		t.Fatalf("bad payloads must not enter ring, got %d", got)
	}
}

func TestReportsOrderAndCap(t *testing.T) {
	h := newTestHandler(t)
	for i := 0; i < reportRingCap+3; i++ {
		w := postAlert(t, h, `{"name":"A","severity":"critical","description":"d"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("post %d failed: %d", i, w.Code)
		}
	}
	reps := h.reports.snapshot()
	if len(reps) != reportRingCap {
		t.Fatalf("ring cap=%d got %d", reportRingCap, len(reps))
	}
	// 新→旧：最新一条在最前。
	if reps[0].ReceivedAt < reps[len(reps)-1].ReceivedAt {
		t.Fatalf("order not newest-first: first=%s last=%s", reps[0].ReceivedAt, reps[len(reps)-1].ReceivedAt)
	}
}
