package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/hibiken/asynq"

	"oncall-agent/internal/tool"
)

// taskID 对同一 payload 稳定、随 payload 变化——AM 重投去重的根基。
func TestTaskIDStableAndSensitive(t *testing.T) {
	p1 := []byte(`{"report_id":"a","alerts":[{"name":"X"}]}`)
	p2 := []byte(`{"report_id":"b","alerts":[{"name":"X"}]}`)
	if taskID(p1) != taskID(p1) {
		t.Fatal("taskID must be deterministic")
	}
	if taskID(p1) == taskID(p2) {
		t.Fatal("taskID must differ across payloads")
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
