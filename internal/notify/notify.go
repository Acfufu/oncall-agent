package notify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// 通知写回出站面（ADR-0008）：把诊断终态报告 JSON POST 到配置的通用 webhook。
// 通知不是 remediation——纯单向告知，不确认不静默不处置；失败交 asynq 退避重试，
// 不挡诊断主链。url 为空属调用方违约（由 handler 侧 SkipRetry 把关），此处不兜。

// postTimeout 单次投递上限：webhook 接收端慢不能拖死 worker 槽位。
const postTimeout = 10 * time.Second

// client 共享连接池；Timeout 是兜底上限，单请求另有 ctx 控制。
var client = &http.Client{Timeout: postTimeout}

// Post 把报告 JSON 原文投递到 url（body 与 GET /reports 条目同形状，ADR-0008）。
// 2xx 视为成功；非 2xx/网络错误返回可重试 error，由 asynq 指数退避重投。
func Post(ctx context.Context, url string, reportJSON []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reportJSON))
	if err != nil {
		return fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: post %s: %w", url, err)
	}
	defer resp.Body.Close()
	// 读尽丢弃：让连接可复用；接收端响应体与本链无关。
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("notify: post %s: unexpected status %d", url, resp.StatusCode)
	}
	return nil
}
