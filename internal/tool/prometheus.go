// Package tool 只读 Prometheus 查询（v0.1）。禁确认/静默/写操作。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"oncall-agent/internal/trace"
)

// Alert 为 firing 告警实例：{name,severity,description,labels,startsAt}。
type Alert struct {
	Name        string            `json:"name"`
	Severity    string            `json:"severity"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	StartsAt    string            `json:"startsAt"`
}

// PromClient 只读客户端，仅 GET /api/v1/alerts。
type PromClient struct {
	BaseURL string
	Client  *http.Client
}

// NewPromClient 构造只读客户端。
func NewPromClient(baseURL string) *PromClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:9090"
	}
	return &PromClient{BaseURL: baseURL, Client: &http.Client{Timeout: 10 * time.Second}}
}

// Firing 拉取 state=firing 告警；Prom 不可用返回空（调用方明示无告警），不报错中断。
// 无 ctx 版走 Background。
func (p *PromClient) Firing() ([]Alert, error) {
	return p.FiringWithContext(context.Background())
}

// FiringWithContext 为 Firing 的 ctx 版：Prom.firing 子 span 包 HTTP。
func (p *PromClient) FiringWithContext(ctx context.Context) (firing []Alert, err error) {
	if p == nil {
		return nil, nil
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, s := trace.Start(ctx, "Prom.firing", map[string]string{
		"component": "Prom",
		"span.type": "firing",
	})
	defer func() {
		if err != nil {
			s.RecordError(err)
			s.SetStatus(trace.StatusError, err.Error())
		} else {
			s.SetStatus(trace.StatusOK, "")
			s.SetAttribute("prom.alerts", itoa(len(firing)))
		}
		s.End()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/api/v1/alerts", nil)
	if err != nil {
		return nil, nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, nil
	}
	var out struct {
		Status string `json:"status"`
		Data   struct {
			Alerts []struct {
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
				State       string            `json:"state"`
				ActiveAt    string            `json:"activeAt"`
			} `json:"alerts"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode alerts: %w", err)
	}
	for _, a := range out.Data.Alerts {
		if !strings.EqualFold(strings.TrimSpace(a.State), "firing") {
			continue
		}
		labels := a.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		name := labels["alertname"]
		if name == "" {
			name = labels["alert"]
		}
		sev := labels["severity"]
		desc := a.Annotations["description"]
		if desc == "" {
			desc = a.Annotations["summary"]
		}
		firing = append(firing, Alert{
			Name:        name,
			Severity:    sev,
			Description: desc,
			Labels:      labels,
			StartsAt:    a.ActiveAt,
		})
	}
	if firing == nil {
		firing = []Alert{}
	}
	return firing, nil
}
