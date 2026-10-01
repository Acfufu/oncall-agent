package judge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"oncall-agent/internal/config"
)

// R04：judge 出站必须带超时——慢 LLM 不得吃满 asynq 任务预算拖死 worker。
// 换装 50ms 超时 client 打 300ms 慢端点，必须报 judge LLM call 错误。
func TestScoreOutboundHasTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"分数: 3 大体可用"}}]}`)
	}))
	defer slow.Close()

	old := judgeHTTPClient
	judgeHTTPClient = &http.Client{Timeout: 50 * time.Millisecond}
	defer func() { judgeHTTPClient = old }()

	_, _, err := Score(context.Background(), config.OpenAIConfig{APIBase: slow.URL, Model: "m"}, "诊断内容", nil)
	if err == nil || !strings.Contains(err.Error(), "judge LLM call") {
		t.Fatalf("want timeout error from judge outbound call, got %v", err)
	}
}

// 正常路径回归：端点及时应答时评分照常解析。
func TestScoreParsesReply(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"分数: 4 引用支撑充分"}}]}`)
	}))
	defer ok.Close()
	score, reason, err := Score(context.Background(), config.OpenAIConfig{APIBase: ok.URL, Model: "m"}, "诊断内容", nil)
	if err != nil {
		t.Fatal(err)
	}
	if score != 4 || reason == "" {
		t.Fatalf("score=%d reason=%q", score, reason)
	}
}
