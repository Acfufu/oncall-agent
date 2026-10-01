package agent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"oncall-agent/internal/observability"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
)

// F07：alert 主链（PlanPushed→diagnose→RAG.Search）检索命中必须进 rag_hits_total，
// 且带 caller="alert" 维度与工具面 caller="tool" 区分。此前该指标只在工具面
// （tool/exec.go）打点，告警驱动诊断的检索是指标盲区。
func TestAlertChainRagHitsCarriesCallerAlert(t *testing.T) {
	shutdown, err := observability.InitMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(context.Background())

	s := store.NewMemoryVector()
	r := rag.New(s, nil)
	md := "# CPU 高负载处置\n## 现象\nCPU 使用率持续大于 90%\n## 处置\n限流扩容排查热点"
	if err := r.AddDoc("cpu_high_usage.md", md, "demo"); err != nil {
		t.Fatal(err)
	}
	p := New(nil, r)
	alerts := []tool.Alert{{
		Name:        "CPUHighUsage",
		Description: "CPU 使用率持续大于 90%",
		Severity:    "warning",
		StartsAt:    "2026-10-02T00:00:00Z",
	}}
	_, citations := p.PlanPushed(context.Background(), alerts)
	if len(citations) == 0 {
		t.Fatal("expected citations from alert-chain search, got none")
	}

	rec := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	// OTel exporter 附带 otel_scope_* 标签，按行级匹配而非精确前缀。
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(ln, "rag_hits_total{") &&
			strings.Contains(ln, `caller="alert"`) &&
			strings.Contains(ln, " 3") {
			return // 命中：主链 1 告警 × 3 候选
		}
	}
	t.Fatalf("metrics body missing rag_hits_total series with caller=\"alert\" and value 3; has rag_hits lines:\n%s",
		linesWith(body, "rag_hits"))
}

func linesWith(s, sub string) string {
	var keep []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, sub) {
			keep = append(keep, ln)
		}
	}
	return strings.Join(keep, "\n")
}

// R02：Prom 源不可达不得吞成「无告警」假阴性——planner 明示区分，
// 值班员看到「源不可达请检查 Prometheus」而不是「当前无 firing 告警」。
func TestPlanPromUnreachableNotSilent(t *testing.T) {
	prom := tool.NewPromClient("http://127.0.0.1:1") // 死端口
	p := New(prom, nil)
	_, diagnosis, _ := p.Plan()
	if strings.Contains(diagnosis, "当前无 firing 告警") {
		t.Fatalf("prom unreachable swallowed as no-alerts false negative: %s", diagnosis)
	}
	if !strings.Contains(diagnosis, "不可达") {
		t.Fatalf("diagnosis must say source unreachable, got: %s", diagnosis)
	}
}
