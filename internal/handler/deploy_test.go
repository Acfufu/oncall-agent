package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"oncall-agent/internal/tool"
)

// stubGitHub 报告链取数源 stub：返回 commits/deployments 形状 JSON。
// 事件日期落在 amPayload startsAt（2026-09-26T10:00Z）取窗 [T−24h, T] 之内。
func stubGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	commits := `[{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","commit":{"author":{"date":"2026-09-26T09:00:00Z"},"message":"fix: cpu throttle"}}]`
	deploys := `[{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","environment":"prod","created_at":"2026-09-26T08:00:00Z","description":"release v7","ref":"main"}]`
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/commits", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(commits))
	})
	mux.HandleFunc("/repos/o/r/deployments", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(deploys))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// runReport 直驱共用链并返回回填后的报告。
func runReport(t *testing.T, h *Handler, id string) Report {
	t.Helper()
	h.reports.add(Report{ID: id, Status: StatusQueued, ReceivedAt: "2026-09-27T10:05:00Z"})
	rep := h.RunAlertDiagnosis(context.Background(), id, parseAM(t))
	return rep
}

// ADR-0009：repo 配置→报告含非空 deploy_events（env/sha/message/time 扁平数组）。
func TestReportDeployEventsAttached(t *testing.T) {
	h := newTestHandler(t)
	h.SetDeploy(tool.DeploySource{Repo: "o/r", BaseURL: stubGitHub(t).URL})
	rep := runReport(t, h, "dep1")
	if rep.Status != StatusDone {
		t.Fatalf("status want done, got %s", rep.Status)
	}
	if rep.DeployEvents == nil || len(*rep.DeployEvents) != 2 {
		t.Fatalf("want 2 deploy events, got %+v", rep.DeployEvents)
	}
	body, _ := json.Marshal(rep)
	if !strings.Contains(string(body), `"deploy_events":[{"env":""`) {
		t.Fatalf("json missing deploy_events array: %s", body)
	}
	// /reports 面同样带字段。
	snap := h.reports.snapshot()
	if snap[0].DeployEvents == nil || len(*snap[0].DeployEvents) != 2 {
		t.Fatalf("ring entry missing deploy_events: %+v", snap[0])
	}
}

// ADR-0009 降级缝：GitHub 不可达→报告仍 done，deploy_events 空非 nil（配置态形状稳定）。
func TestReportDeployEventsDegraded(t *testing.T) {
	h := newTestHandler(t)
	h.SetDeploy(tool.DeploySource{Repo: "o/r", BaseURL: "http://127.0.0.1:1"})
	rep := runReport(t, h, "dep2")
	if rep.Status != StatusDone {
		t.Fatalf("degrade must not fail the chain, got status=%s diagnosis=%s", rep.Status, rep.Diagnosis)
	}
	if rep.DeployEvents == nil || len(*rep.DeployEvents) != 0 {
		t.Fatalf("degraded want empty non-nil array, got %+v", rep.DeployEvents)
	}
	body, _ := json.Marshal(rep)
	if !strings.Contains(string(body), `"deploy_events":[]`) {
		t.Fatalf("degraded json want empty array present: %s", body)
	}
}

// ADR-0009 未配置：字段 nil，JSON 整体省略（与 v0.6 报告同形状，eval 同级可比）。
func TestReportDeployEventsUnconfiguredOmitted(t *testing.T) {
	h := newTestHandler(t)
	rep := runReport(t, h, "dep3")
	if rep.DeployEvents != nil {
		t.Fatalf("unconfigured want nil, got %+v", rep.DeployEvents)
	}
	body, _ := json.Marshal(rep)
	if strings.Contains(string(body), "deploy_events") {
		t.Fatalf("unconfigured json must omit field: %s", body)
	}
}
