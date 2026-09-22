package tool

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PromDeps 为 prometheus_query 依赖（只读 GET /api/v1/query）。
type PromDeps struct {
	URL    string
	Client *http.Client
}

// PromQuery 只读查 Prometheus 即时向量，返回截断原文（最多 4KB）。
func (d *PromDeps) PromQuery(argsJSON string) (string, error) {
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
	u := base + "/api/v1/query?" + url.Values{"query": {args.Query}}.Encode()
	resp, err := client.Get(u)
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
