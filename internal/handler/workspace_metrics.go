package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"oncall-agent/internal/config"
	"oncall-agent/internal/workspace"
)

const maxMetricBody = 2 << 20
const maxMetricPoints = 2000
const maxMetricSeries = 20

type metricTemplate struct {
	ID     string `json:"id"`
	Unit   string `json:"unit"`
	PromQL string `json:"promql"`
}
type metricConfig struct {
	Version   string           `json:"version"`
	Templates []metricTemplate `json:"templates"`
}
type m2Sources struct {
	topology      *workspace.Topology
	templates     map[string]metricTemplate
	version, hash string
}

func loadMetricTemplates(path string) (metricConfig, string, error) {
	var cfg metricConfig
	f, err := os.Open(path)
	if err != nil {
		return cfg, "", fmt.Errorf("metric templates unavailable")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(raw) > 16384 {
		return cfg, "", fmt.Errorf("metric templates exceed size budget")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&cfg) != nil || d.Decode(new(any)) != io.EOF || cfg.Version == "" || len(cfg.Templates) == 0 || len(cfg.Templates) > 3 {
		return cfg, "", fmt.Errorf("metric templates invalid")
	}
	seen := map[string]bool{}
	for _, t := range cfg.Templates {
		if (t.ID != "error_rate" && t.ID != "request_rate" && t.ID != "latency_p99") || seen[t.ID] || strings.TrimSpace(t.Unit) == "" || len(t.Unit) > 64 || len(t.PromQL) > 4096 || !strings.Contains(t.PromQL, "{{environment}}") || !strings.Contains(t.PromQL, "{{service}}") {
			return cfg, "", fmt.Errorf("metric template scope invalid")
		}
		seen[t.ID] = true
	}
	sum := sha256.Sum256(raw)
	return cfg, hex.EncodeToString(sum[:]), nil
}

// RegisterM2 pins controlled configuration snapshots at startup. The caller retains the shared auth boundary.
func (h *WorkspaceHTTP) RegisterM2(e *gin.Engine, cfg config.Config) error {
	sources := m2Sources{templates: map[string]metricTemplate{}}
	if cfg.Topology.File != "" {
		top, err := workspace.LoadTopology(cfg.Topology.File)
		if err != nil {
			return err
		}
		sources.topology = top
	}
	if cfg.Metrics.TemplatesFile != "" {
		t, hash, err := loadMetricTemplates(cfg.Metrics.TemplatesFile)
		if err != nil {
			return err
		}
		sources.version = t.Version
		sources.hash = hash
		for _, template := range t.Templates {
			sources.templates[template.ID] = template
		}
	}
	if sources.topology != nil {
		if err := h.Service.PersistTopology(context.Background(), sources.topology); err != nil {
			return err
		}
	}
	e.GET("/api/v1/incidents/:id/topology", func(c *gin.Context) { h.topology(c, sources) })
	e.GET("/api/v1/incidents/:id/metrics", func(c *gin.Context) { h.metrics(c, sources) })
	h.setM2Capabilities(sources.topology != nil, len(sources.templates) > 0)
	return nil
}
func allowedMetricParams(c *gin.Context, allowed map[string]bool) bool {
	for k, vs := range c.Request.URL.Query() {
		if !allowed[k] || len(vs) != 1 {
			wsError(c, 400, "invalid_request", "不支持的查询参数或重复参数", false)
			return false
		}
	}
	return true
}
func (h *WorkspaceHTTP) topology(c *gin.Context, sources m2Sources) {
	if !allowedMetricParams(c, map[string]bool{"environment": true, "service": true}) {
		return
	}
	inc, err := h.Service.DB.Incident(c.Request.Context(), c.Param("id"))
	if err != nil {
		wsFail(c, err)
		return
	}
	if (c.Query("environment") != "" && c.Query("environment") != inc.Environment) || (c.Query("service") != "" && c.Query("service") != inc.Service) {
		wsError(c, 400, "invalid_request", "拓扑范围必须与当前事件一致", false)
		return
	}
	if sources.topology == nil {
		wsOK(c, 200, workspace.TopologySnapshot{Capability: "not_configured", Nodes: []workspace.TopologyNode{}, Edges: []workspace.TopologyEdge{}, PotentialImpact: []workspace.PotentialImpact{}}, 1, nil)
		return
	}
	g, err := sources.topology.ForService(c.Request.Context(), inc.Environment, inc.Service)
	if err != nil {
		if c.Request.Context().Err() != nil {
			wsFail(c, err)
		} else {
			wsError(c, 400, "unknown_service", "服务未在当前环境唯一声明", false)
		}
		return
	}
	wsOK(c, 200, g, 1, gin.H{"as_of": g.AsOf})
}

type metricSeries struct {
	Labels         map[string]string `json:"labels"`
	Values         [][2]any          `json:"values"`
	MissingSamples int               `json:"missing_samples"`
}
type metricResult struct {
	Capability         string         `json:"capability"`
	MetricID           string         `json:"metric_id"`
	Unit               string         `json:"unit"`
	Start              string         `json:"start"`
	End                string         `json:"end"`
	Step               int            `json:"step"`
	Series             []metricSeries `json:"series"`
	Source             string         `json:"source"`
	Query              string         `json:"query"`
	TemplateVersion    string         `json:"template_version"`
	TemplateSHA256     string         `json:"template_sha256"`
	SampleState        string         `json:"sample_state"`
	HistoricalEvidence bool           `json:"historical_evidence"`
}
type metricWindow struct {
	start, end   time.Time
	step, points int
}

func parseMetricWindow(c *gin.Context) (metricWindow, bool) {
	var w metricWindow
	start, err := time.Parse(time.RFC3339Nano, c.Query("start"))
	if err != nil {
		wsError(c, 400, "invalid_request", "start 必须为 RFC3339 时间", false)
		return w, false
	}
	end, err := time.Parse(time.RFC3339Nano, c.Query("end"))
	if err != nil || end.Before(start) || end.Sub(start) > 24*time.Hour {
		wsError(c, 400, "invalid_request", "时间窗必须有序且不超过24小时", false)
		return w, false
	}
	step, err := strconv.Atoi(c.Query("step"))
	if err != nil || step < 15 || step > 86400 {
		wsError(c, 400, "invalid_request", "step 必须为15到86400秒的整数", false)
		return w, false
	}
	points := int(end.Sub(start)/(time.Duration(step)*time.Second)) + 1
	if points > maxMetricPoints {
		wsError(c, 400, "query_budget_exceeded", "采样点超过每条series 2000点预算", false)
		return w, false
	}
	return metricWindow{start: start.UTC(), end: end.UTC(), step: step, points: points}, true
}
func (h *WorkspaceHTTP) metrics(c *gin.Context, sources m2Sources) {
	if !allowedMetricParams(c, map[string]bool{"metric_id": true, "start": true, "end": true, "step": true}) {
		return
	}
	id := c.Query("metric_id")
	if id != "error_rate" && id != "request_rate" && id != "latency_p99" {
		wsError(c, 400, "unknown_template", "未知指标模板", false)
		return
	}
	window, ok := parseMetricWindow(c)
	if !ok {
		return
	}
	inc, err := h.Service.DB.Incident(c.Request.Context(), c.Param("id"))
	if err != nil {
		wsFail(c, err)
		return
	}
	result := metricResult{Capability: "not_configured", MetricID: id, Start: window.start.Format(time.RFC3339Nano), End: window.end.Format(time.RFC3339Nano), Step: window.step, Series: []metricSeries{}, HistoricalEvidence: false, SampleState: "empty"}
	template, ok := sources.templates[id]
	if !ok {
		wsOK(c, 200, result, 1, nil)
		return
	}
	if h.Prom == nil {
		wsError(c, 503, "source_unavailable", "Prometheus 未配置", true)
		return
	}
	base, err := url.Parse(h.Prom.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.RawQuery != "" || base.Fragment != "" {
		wsError(c, 503, "source_unavailable", "Prometheus 配置不可用", false)
		return
	}
	query := strings.NewReplacer("{{environment}}", strconv.Quote(inc.Environment), "{{service}}", strconv.Quote(inc.Service)).Replace(template.PromQL)
	source := *base
	source.User = nil
	source.Path = strings.TrimRight(source.Path, "/") + "/api/v1/query_range"
	source.RawPath = ""
	result.Capability = "available"
	result.Unit = template.Unit
	result.Source = source.String()
	result.Query = query
	result.TemplateVersion = sources.version
	result.TemplateSHA256 = sources.hash
	target := *base
	target.Path = strings.TrimRight(target.Path, "/") + "/api/v1/query_range"
	target.RawPath = ""
	params := url.Values{"query": {query}, "start": {result.Start}, "end": {result.End}, "step": {strconv.Itoa(window.step)}, "timeout": {"10s"}}
	target.RawQuery = params.Encode()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		wsError(c, 503, "source_unavailable", "无法构造指标请求", true)
		return
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if h.Prom.Client != nil {
		client.Transport = h.Prom.Client.Transport
		if h.Prom.Client.Timeout > 0 && h.Prom.Client.Timeout < client.Timeout {
			client.Timeout = h.Prom.Client.Timeout
		}
	}
	res, err := client.Do(req)
	if err != nil {
		var ne interface{ Timeout() bool }
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &ne) && ne.Timeout() {
			wsError(c, 504, "timeout", "指标查询超时或取消", true)
		} else {
			wsError(c, 503, "source_unavailable", "Prometheus 暂时不可用", true)
		}
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		wsError(c, 503, "source_unavailable", "Prometheus 返回错误", true)
		return
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxMetricBody+1))
	if err != nil || len(body) > maxMetricBody {
		wsError(c, 503, "response_budget_exceeded", "指标响应超过预算或读取失败", true)
		return
	}
	series, code, err := parseMetricMatrix(body, window, inc)
	if err != nil {
		status := 503
		if code == "unsupported_result_type" {
			status = 422
		}
		wsError(c, status, code, "指标响应格式或资源预算不受支持", code != "unsupported_result_type")
		return
	}
	result.Series = series
	result.SampleState = "complete"
	if len(series) == 0 {
		result.SampleState = "empty"
	}
	for _, s := range series {
		if s.MissingSamples > 0 {
			result.SampleState = "partial"
		}
	}
	extra := gin.H{}
	if result.SampleState == "partial" {
		extra["data_state"] = "partial"
	}
	wsOK(c, 200, result, 1, extra)
}
func parseMetricMatrix(body []byte, w metricWindow, inc workspace.Incident) ([]metricSeries, string, error) {
	var rsp struct {
		Status string `json:"status"`
		Data   struct {
			Type   string `json:"resultType"`
			Result []struct {
				Metric     map[string]string   `json:"metric"`
				Values     [][]json.RawMessage `json:"values"`
				Histograms json.RawMessage     `json:"histograms"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &rsp) != nil || rsp.Status != "success" || rsp.Data.Result == nil {
		return nil, "source_unavailable", fmt.Errorf("invalid Prometheus matrix")
	}
	if rsp.Data.Type != "matrix" {
		return nil, "unsupported_result_type", fmt.Errorf("matrix required")
	}
	if len(rsp.Data.Result) > maxMetricSeries {
		return nil, "response_budget_exceeded", fmt.Errorf("series exceeds budget")
	}
	out := []metricSeries{}
	start := float64(w.start.Unix()) + float64(w.start.Nanosecond())/1e9
	for _, s := range rsp.Data.Result {
		if len(s.Histograms) > 0 && string(s.Histograms) != "null" && string(s.Histograms) != "[]" {
			return nil, "unsupported_result_type", fmt.Errorf("histogram not supported")
		}
		if s.Values == nil {
			return nil, "source_unavailable", fmt.Errorf("values array required")
		}
		if len(s.Values) > maxMetricPoints {
			return nil, "response_budget_exceeded", fmt.Errorf("points exceeds budget")
		}
		if len(s.Metric) > 50 {
			return nil, "response_budget_exceeded", fmt.Errorf("labels exceeds budget")
		}
		for k, v := range s.Metric {
			if len(k) > 256 || len(v) > 1024 {
				return nil, "response_budget_exceeded", fmt.Errorf("label size exceeds budget")
			}
		}
		if (s.Metric["environment"] != "" && s.Metric["environment"] != inc.Environment) || (s.Metric["service"] != "" && s.Metric["service"] != inc.Service) {
			return nil, "source_unavailable", fmt.Errorf("upstream result outside incident scope")
		}
		values := make([][2]any, w.points)
		seen := map[int]bool{}
		for i := range values {
			values[i] = [2]any{start + float64(i*w.step), nil}
		}
		for _, pair := range s.Values {
			if len(pair) != 2 {
				return nil, "source_unavailable", fmt.Errorf("invalid sample")
			}
			var timestamp float64
			var text string
			if json.Unmarshal(pair[0], &timestamp) != nil || json.Unmarshal(pair[1], &text) != nil {
				return nil, "unsupported_result_type", fmt.Errorf("float sample expected")
			}
			offset := (timestamp - start) / float64(w.step)
			index := int(math.Round(offset))
			if index < 0 || index >= w.points || math.Abs(offset-float64(index)) > 0.0001 || seen[index] {
				return nil, "source_unavailable", fmt.Errorf("sample timestamp outside grid or duplicated")
			}
			seen[index] = true
			value, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, "source_unavailable", fmt.Errorf("invalid float")
			}
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				values[index][1] = value
			}
		}
		missing := 0
		for _, p := range values {
			if p[1] == nil {
				missing++
			}
		}
		labels := s.Metric
		if labels == nil {
			labels = map[string]string{}
		}
		out = append(out, metricSeries{Labels: labels, Values: values, MissingSamples: missing})
	}
	return out, "", nil
}
