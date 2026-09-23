package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"oncall-agent/internal/trace"
)

// PromDeps 为 prometheus_query 依赖（只读 GET /api/v1/query）。
type PromDeps struct {
	URL    string
	Client *http.Client
}

// PromQuery 只读查 Prometheus 即时向量，返回截断原文（最多 4KB）。
// 无 ctx 版走 Background。
func (d *PromDeps) PromQuery(argsJSON string) (string, error) {
	return d.PromQueryWithContext(context.Background(), argsJSON)
}

// PromQueryWithContext 为 PromQuery 的 ctx 版：Prom.query 子 span 包 HTTP。
func (d *PromDeps) PromQueryWithContext(ctx context.Context, argsJSON string) (out string, err error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := argsOf(argsJSON, &args); err != nil {
		return "", fmt.Errorf("prometheus_query bad args: %w", err)
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return "", fmt.Errorf("prometheus_query: query required")
	}
	base := "http://localhost:9090"
	if d != nil && strings.TrimSpace(d.URL) != "" {
		base = strings.TrimRight(strings.TrimSpace(d.URL), "/")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if d != nil && d.Client != nil {
		client = d.Client
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, s := trace.Start(ctx, "Prom.query", map[string]string{
		"component":  "Prom",
		"span.type":  "query",
		"prom.query": args.Query,
	})
	defer func() {
		if err != nil {
			s.RecordError(err)
			s.SetStatus(trace.StatusError, err.Error())
		} else {
			s.SetStatus(trace.StatusOK, "")
			s.SetAttribute("prom.bytes", itoa(len(out)))
		}
		s.End()
	}()
	u := base + "/api/v1/query?" + url.Values{"query": {args.Query}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("prometheus %d: %s", resp.StatusCode, string(raw))
	}
	return string(raw), nil
}
