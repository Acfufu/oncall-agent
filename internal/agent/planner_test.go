package agent

import (
	"fmt"

	"net/http"
	"sync"

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

// initTestMetrics 包内共享一次 InitMetrics（不 shutdown，进程退出即清理）；
// 多测试各自 InitMetrics 会在同一 prometheus 注册表重复注册 collector。
var testMetricsOnce sync.Once

func initTestMetrics(t *testing.T) {
	t.Helper()
	var err error
	testMetricsOnce.Do(func() {
		_, err = observability.InitMetrics(context.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// R03：chat LLM 故障降级必须可见——fallback 触发时打 chat_fallback_total
// 且日志带原始 loop 错误；此前降级零观测，根因只能去 Jaeger 猜。
func TestChatFallbackObservable(t *testing.T) {
	initTestMetrics(t)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	}))
	defer llm.Close()

	s := store.NewMemoryVector()
	r := rag.New(s, nil)
	md := "# CPU 高负载处置\n## 现象\nCPU 使用率持续大于 90%\n## 处置\n限流扩容排查热点"
	if err := r.AddDoc("cpu_high_usage.md", md, "demo"); err != nil {
		t.Fatal(err)
	}
	ra := NewReAct(llm.URL, "", "test-model", tool.NewDeps(r, ""))
	reply, _, err := ra.Run(context.Background(), "s-fallback", "CPU 使用率高怎么办")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "根据知识库匹配到以下内容") {
		t.Fatalf("fallback should return rag-direct reply, got: %s", reply)
	}
	rec := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	for _, ln := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(ln, "chat_fallback_total") && strings.Contains(ln, " 1") {
			return
		}
	}
	t.Fatalf("metrics missing chat_fallback_total=1 after LLM failure; body tail:\n%s", linesWith(rec.Body.String(), "chat_fallback"))
}

// R12：LLM 非 JSON 错误体（网关 502 HTML / 429 空 body）不得掩盖真实 HTTP
// 状态——先查状态码再 decode，排障方向不被 "decode llm resp" 带偏。
func TestChatNonJSONErrorSurfacesStatus(t *testing.T) {
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "<html>502 Bad Gateway</html>")
	}))
	defer gw.Close()
	ra := NewReAct(gw.URL, "", "test-model", tool.NewDeps(nil, ""))
	cites := []Citation{}
	_, err := ra.loop(context.Background(), []apiMsg{{Role: "user", Content: "q"}}, &cites, map[string]bool{})
	if err == nil {
		t.Fatal("want error from llm call")
	}
	if strings.Contains(err.Error(), "decode llm resp") {
		t.Fatalf("decode error masks real status: %v", err)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error must surface HTTP 502, got: %v", err)
	}
}
