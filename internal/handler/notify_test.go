package handler

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"oncall-agent/internal/tool"
)

// fakeNotifier 通知假入队（ADR-0008）：记录载荷供断言，fn 可注入失败行为。
type fakeNotifier struct {
	mu    sync.Mutex
	calls []notifyCall
	fn    func(reportID string, report []byte) error
}

type notifyCall struct {
	reportID string
	report   []byte
}

func (f *fakeNotifier) EnqueueNotification(reportID string, report []byte) error {
	if f.fn != nil {
		if err := f.fn(reportID, report); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, notifyCall{reportID: reportID, report: report})
	return nil
}

// TestNotifyOnLowScore（ADR-0008 触发器一）：low_score 终态报告入队通知，
// 载荷与 GET /reports 条目同形状。
func TestNotifyOnLowScore(t *testing.T) {
	h := newTestHandler(t)
	n := &fakeNotifier{}
	h.SetNotify("http://127.0.0.1:9978/hook", n)
	rep := Report{ID: "r-low", Status: StatusDone, Score: 1, LowScore: true, Diagnosis: "低分诊断"}
	h.maybeNotify(context.Background(), rep)
	if got := len(n.calls); got != 1 {
		t.Fatalf("notify calls=%d want 1", got)
	}
	c := n.calls[0]
	if c.reportID != "r-low" {
		t.Fatalf("reportID=%s want r-low", c.reportID)
	}
	var got Report
	if err := json.Unmarshal(c.report, &got); err != nil {
		t.Fatalf("payload not /reports shape: %v", err)
	}
	if got.ID != "r-low" || got.Score != 1 || !got.LowScore {
		t.Fatalf("payload mismatch: %s", c.report)
	}
}

// TestNotifySkipUnscored：未评分（score=0，judge 降级/未配 key）不得触发——
// 0 分是「无分」不是低分（ADR-0006 口径，ADR-0008 触发器沿用 LowScore 布尔）。
func TestNotifySkipUnscored(t *testing.T) {
	h := newTestHandler(t)
	n := &fakeNotifier{}
	h.SetNotify("http://127.0.0.1:9978/hook", n)
	h.maybeNotify(context.Background(), Report{ID: "r-unscored", Status: StatusDone})
	if got := len(n.calls); got != 0 {
		t.Fatalf("unscored must not notify, calls=%d", got)
	}
}

// TestNotifyDisabledWhenURLEmpty：webhook 空=通知关闭（ADR-0008 零值即关）。
func TestNotifyDisabledWhenURLEmpty(t *testing.T) {
	h := newTestHandler(t)
	n := &fakeNotifier{}
	h.SetNotify("", n)
	h.maybeNotify(context.Background(), Report{ID: "r-off", Status: StatusDone, Score: 1, LowScore: true})
	if got := len(n.calls); got != 0 {
		t.Fatalf("empty webhook must not notify, calls=%d", got)
	}
}

// TestNotifyOnPanicMarkFailed（ADR-0008 触发器二）：worker 链 panic → 报告落
// failed 终态 + 通知载荷 status=failed + error 上抛（asynq 据此重试）。不恢复
// 的现状是报告永停 running——ADR-0008 所指黑洞。
func TestNotifyOnPanicMarkFailed(t *testing.T) {
	h := newTestHandler(t)
	n := &fakeNotifier{}
	h.SetNotify("http://127.0.0.1:9978/hook", n)
	h.SetQueue(&fakeQueue{})
	h.reports.add(Report{ID: "r-panic", Status: StatusQueued})

	old := runChain
	runChain = func(*Handler, context.Context, string, []tool.Alert) Report {
		panic("boom: rag store nil")
	}
	defer func() { runChain = old }()

	err := h.ProcessAlertDiagnosis(context.Background(), "r-panic", parseAM(t))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v want panic surfaced as error", err)
	}
	reps := h.reports.snapshot()
	if len(reps) != 1 || reps[0].Status != StatusFailed || reps[0].Diagnosis == "" {
		t.Fatalf("failed terminal state wrong: %+v", reps)
	}
	if got := len(n.calls); got != 1 {
		t.Fatalf("failed report must notify, calls=%d", got)
	}
	var got Report
	if err := json.Unmarshal(n.calls[0].report, &got); err != nil {
		t.Fatalf("payload not /reports shape: %v", err)
	}
	if got.Status != StatusFailed || got.ID != "r-panic" {
		t.Fatalf("notify payload mismatch: %s", n.calls[0].report)
	}
}
