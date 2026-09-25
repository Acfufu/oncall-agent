package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
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
	h.SetAutoIngest(false) // 默认测试关沉淀，入库行为单测另开
	return h
}

// fakeQueue 同步假入队：fn 为注入的入级行为（默认成功无操作）。
type fakeQueue struct {
	fn func(reportID string, alerts []tool.Alert) error
}

func (f *fakeQueue) EnqueueAlertDiagnosis(reportID string, alerts []tool.Alert) error {
	if f.fn == nil {
		return nil
	}
	return f.fn(reportID, alerts)
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

// parseAM 解析 fixture 告警，供直驱 worker 回调使用。
func parseAM(t *testing.T) []tool.Alert {
	t.Helper()
	alerts, err := parseAlertPayload([]byte(amPayload))
	if err != nil || len(alerts) == 0 {
		t.Fatalf("parse fixture: %v alerts=%d", err, len(alerts))
	}
	return alerts
}

// ADR-0006 异步契约：入队即回 202 {id,status:queued}，环先落 queued 条目，
// worker 回调走共用链回填 done（诊断命中 demo 知识）。
func TestAlertAsyncEnqueueAndWorkerCompletes(t *testing.T) {
	h := newTestHandler(t)
	h.SetQueue(&fakeQueue{})
	w := postAlert(t, h, amPayload)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Received int    `json:"received"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == "" || resp.Status != StatusQueued || resp.Received != 1 {
		t.Fatalf("202 body mismatch: %+v", resp)
	}
	reps := h.reports.snapshot()
	if len(reps) != 1 || reps[0].Status != StatusQueued || reps[0].ID != resp.ID {
		t.Fatalf("queued entry wrong: %+v", reps)
	}

	if err := h.ProcessAlertDiagnosis(context.Background(), resp.ID, parseAM(t)); err != nil {
		t.Fatal(err)
	}
	reps = h.reports.snapshot()
	if len(reps) != 1 {
		t.Fatalf("worker must upsert, not append: %+v", reps)
	}
	done := reps[0]
	if done.Status != StatusDone || done.ID != resp.ID {
		t.Fatalf("status=%s id=%s", done.Status, done.ID)
	}
	if !strings.Contains(done.Diagnosis, "CPUHighUsage") {
		t.Fatalf("diagnosis missing alertname: %s", done.Diagnosis)
	}
	if len(done.Citations) == 0 || done.Citations[0].Doc != "cpu_high_usage.md" {
		t.Fatalf("citations not hitting demo doc: %+v", done.Citations)
	}
	if done.ReceivedAt == "" {
		t.Fatal("received_at empty")
	}
}

func TestAlertResolvedSkipped(t *testing.T) {
	h := newTestHandler(t)
	h.SetQueue(&fakeQueue{})
	w := postAlert(t, h, `{"alerts":[{"status":"resolved","labels":{"alertname":"X"}}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("resolved-only payload should 400, got %d", w.Code)
	}
}

func TestAlertSingleShapes(t *testing.T) {
	h := newTestHandler(t)
	h.SetQueue(&fakeQueue{})
	// 单条 amAlert 形状。
	w := postAlert(t, h, `{"status":"firing","labels":{"alertname":"DiskFull","severity":"warn"},"annotations":{"description":"磁盘 95%"}}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("single amAlert shape failed: %d %s", w.Code, w.Body.String())
	}
	// tool.Alert 直填形状。
	w = postAlert(t, h, `{"name":"MQBacklog","severity":"critical","description":"消费堆积"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("tool.Alert shape failed: %d %s", w.Code, w.Body.String())
	}
	if got := len(h.reports.snapshot()); got != 2 {
		t.Fatalf("ring size=%d want 2", got)
	}
}

func TestAlertBadPayloads(t *testing.T) {
	h := newTestHandler(t)
	h.SetQueue(&fakeQueue{})
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

// 队列未装配（redis 缺席）：503 且不入环。
func TestAlertQueueNotWired(t *testing.T) {
	h := newTestHandler(t)
	w := postAlert(t, h, amPayload)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no queue should 503, got %d", w.Code)
	}
	if got := len(h.reports.snapshot()); got != 0 {
		t.Fatalf("ring must stay empty, got %d", got)
	}
}

// 同 payload 任务在队（TaskID 冲突）：503 且条目标 failed 人可见。
func TestAlertEnqueueConflictMarksFailed(t *testing.T) {
	h := newTestHandler(t)
	h.SetQueue(&fakeQueue{fn: func(string, []tool.Alert) error {
		return asynq.ErrTaskIDConflict
	}})
	w := postAlert(t, h, amPayload)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("conflict should 503, got %d", w.Code)
	}
	reps := h.reports.snapshot()
	if len(reps) != 1 || reps[0].Status != StatusFailed || reps[0].Diagnosis == "" {
		t.Fatalf("failed entry wrong: %+v", reps)
	}
}

// 入队其他失败：同样 503 + failed 落环。
func TestAlertEnqueueErrorMarksFailed(t *testing.T) {
	h := newTestHandler(t)
	h.SetQueue(&fakeQueue{fn: func(string, []tool.Alert) error {
		return errors.New("redis down")
	}})
	w := postAlert(t, h, amPayload)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("enqueue error should 503, got %d", w.Code)
	}
	if reps := h.reports.snapshot(); reps[0].Status != StatusFailed {
		t.Fatalf("status=%s want failed", reps[0].Status)
	}
}

func TestReportsOrderAndCap(t *testing.T) {
	h := newTestHandler(t)
	h.SetQueue(&fakeQueue{})
	for i := 0; i < reportRingCap+3; i++ {
		w := postAlert(t, h, `{"name":"A","severity":"critical","description":"d"}`)
		if w.Code != http.StatusAccepted {
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

// v0.4 事件沉淀（ADR-0005）在异步链上行为不变：worker 完成后入库、同题覆盖、可关。
func TestAlertAutoIngestIncident(t *testing.T) {
	h := newTestHandler(t)
	h.SetAutoIngest(true)
	h.SetQueue(&fakeQueue{})
	w := postAlert(t, h, amPayload)
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if err := h.ProcessAlertDiagnosis(context.Background(), resp.ID, parseAM(t)); err != nil {
		t.Fatal(err)
	}
	reps := h.reports.snapshot()
	if len(reps) != 1 || reps[0].Ingested != 1 {
		t.Fatalf("ingested=%d want 1: %+v", reps[0].Ingested, reps)
	}
	hits, err := h.RAG.Search("CPUHighUsage", 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, hit := range hits {
		if hit.Doc == "CPUHighUsage.incident.md" && hit.Source == "incident" {
			found = true
		}
	}
	if !found {
		t.Fatalf("incident note not retrievable: %+v", hits)
	}

	// 同告警重推：同题覆盖不膨胀。
	w = postAlert(t, h, amPayload)
	var resp2 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp2); err != nil {
		t.Fatal(err)
	}
	if err := h.ProcessAlertDiagnosis(context.Background(), resp2.ID, parseAM(t)); err != nil {
		t.Fatal(err)
	}
	// 关开关后不再入库。
	h.SetAutoIngest(false)
	w = postAlert(t, h, `{"name":"DiskFull","severity":"warn","description":"磁盘满"}`)
	var resp3 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp3); err != nil {
		t.Fatal(err)
	}
	if err := h.ProcessAlertDiagnosis(context.Background(), resp3.ID, parseAM(t)); err != nil {
		t.Fatal(err)
	}
	reps = h.reports.snapshot()
	if reps[0].Ingested != 0 {
		t.Fatalf("ingest should be off, got %d", reps[0].Ingested)
	}
}
