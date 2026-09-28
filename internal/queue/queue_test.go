package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/hibiken/asynq"

	"oncall-agent/internal/tool"
)

// taskID 只哈希 alerts 数组——report_id 不参与（AM 重投去重的根基，F02）：
// 同告警重投换 report_id 必须命中同一 TaskID；不同告警必须不同 TaskID；
// 同 payload 稳定、长度 32（sha1 截 16 字节 hex）。
func TestTaskIDStableAndSensitive(t *testing.T) {
	alerts := []tool.Alert{{Name: "ContainerOOMKilled", Severity: "critical"}}
	p1, err := json.Marshal(alertPayload{ReportID: "a", Alerts: alerts})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := json.Marshal(alertPayload{ReportID: "b", Alerts: alerts})
	if err != nil {
		t.Fatal(err)
	}
	if taskID(p1) != taskID(p2) {
		t.Fatal("same alerts + different report_id must dedup to same taskID")
	}
	p3, err := json.Marshal(alertPayload{ReportID: "a", Alerts: []tool.Alert{{Name: "Other"}}})
	if err != nil {
		t.Fatal(err)
	}
	if taskID(p1) == taskID(p3) {
		t.Fatal("different alerts must produce different taskID")
	}
	if len(taskID(p1)) != 32 {
		t.Fatalf("taskID len = %d, want 32", len(taskID(p1)))
	}
}

// 载荷 roundtrip：worker 反序列化后 reportID/alerts 不失真。
func TestPayloadRoundtrip(t *testing.T) {
	alerts := []tool.Alert{{Name: "ContainerOOMKilled", Severity: "critical", Description: "d"}}
	raw, err := json.Marshal(alertPayload{ReportID: "rid", Alerts: alerts})
	if err != nil {
		t.Fatal(err)
	}
	var p alertPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.ReportID != "rid" || len(p.Alerts) != 1 || p.Alerts[0].Name != "ContainerOOMKilled" {
		t.Fatalf("roundtrip mismatch: %+v", p)
	}
}

// 坏载荷走 SkipRetry 语义：格式错误重试无意义，包裹后仍可 errors.Is 判定。
func TestBadPayloadSkipsRetry(t *testing.T) {
	wrapped := fmt.Errorf("bad payload: %w: %v", asynq.SkipRetry, "unexpected end of JSON input")
	if !errors.Is(wrapped, asynq.SkipRetry) {
		t.Fatal("SkipRetry must survive wrapping")
	}
}
