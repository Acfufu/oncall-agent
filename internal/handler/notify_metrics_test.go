package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"oncall-agent/internal/observability"
)

// initTestMetrics 包内共享一次 InitMetrics（不 shutdown，进程退出即清理），
// 多测试重复 InitMetrics 会在同一 prometheus 注册表重复注册 collector。
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

// R10：无任务上下文（单测直调 ProcessNotification）的投递失败不得污染
// notification_failed_total——修前 GetRetryCount/GetMaxRetry ok=false 返回
// (0,0)，`0>=0` 误判耗尽走计数分支，注释（按未耗尽处理）与代码相反。
func TestNotifyNoTaskContextNoExhaustCount(t *testing.T) {
	initTestMetrics(t)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	sinkURL := dead.URL
	dead.Close() // 立即关闭 → 投递连接拒绝

	h := newTestHandler(t)
	h.SetNotify(sinkURL, &fakeNotifier{})
	if err := h.ProcessNotification(context.Background(), "r-noctx", []byte(`{"id":"r-noctx"}`)); err == nil {
		t.Fatal("want delivery error against closed sink")
	}

	rec := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	for _, ln := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(ln, "notification_failed_total") {
			t.Fatalf("notification_failed_total polluted without task context: %s", ln)
		}
	}
}
