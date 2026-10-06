package handler

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"oncall-agent/internal/config"
	"oncall-agent/internal/tool"
	"oncall-agent/internal/workspace"
)

type WorkspaceHTTP struct {
	Service            *workspace.Service
	Prom               *tool.PromClient
	Config             config.Config
	DemoDir            string
	topologyConfigured bool
	metricsConfigured  bool
	statusMu           sync.Mutex
	lastSuccess        map[string]string
}

// RegisterWorkspace registers only the v1 contract. The caller applies shared authentication and mounts legacy methods.
func RegisterWorkspace(e *gin.Engine, s *workspace.Service, prom *tool.PromClient, cfg config.Config) *WorkspaceHTTP {
	h := &WorkspaceHTTP{Service: s, Prom: prom, Config: cfg, DemoDir: "aiops-docs-demo"}
	g := e.Group("/api/v1")
	g.Use(workspaceBudget)
	g.GET("/system/status", h.Status)
	g.GET("/workspace/summary", h.Summary)
	g.GET("/incidents", h.Incidents)
	g.GET("/incidents/:id", h.Incident)
	g.GET("/incidents/:id/graph", h.Graph)
	g.POST("/incidents/:id/runs", h.ReDiagnose)
	g.GET("/runs/:id", h.Run)
	g.GET("/runs/:id/events", h.Events)
	g.GET("/evidence/:id", h.Evidence)
	g.GET("/documents", h.Documents)
	g.POST("/documents", h.CreateDocument)
	g.PUT("/documents/:id", h.UpdateDocument)
	g.DELETE("/documents/:id", h.DeleteDocument)
	g.GET("/documents/:id/versions", h.Versions)
	g.GET("/documents/:id/versions/:version", h.Version)
	g.POST("/knowledge/reindex-demo", h.Reindex)
	return h
}
func requestID(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		return fmt.Sprint(v)
	}
	sum := sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10) + c.Request.URL.Path))
	return "req_" + hex.EncodeToString(sum[:8])
}
func wsMeta(c *gin.Context, revision int) gin.H {
	return gin.H{"request_id": requestID(c), "as_of": time.Now().UTC().Format(time.RFC3339Nano), "data_state": "fresh", "mode": "live", "revision": revision, "next_cursor": nil, "truncated": false}
}
func wsOK(c *gin.Context, status int, data any, revision int, extra gin.H) {
	meta := wsMeta(c, revision)
	for k, v := range extra {
		meta[k] = v
	}
	c.JSON(status, gin.H{"data": data, "meta": meta})
}
func wsError(c *gin.Context, status int, code, msg string, retry bool) {
	c.JSON(status, gin.H{"error": msg, "code": code, "retryable": retry, "request_id": requestID(c)})
}
func wsFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		wsError(c, 404, "not_found", "记录不存在", false)
	case errors.Is(err, workspace.ErrConflict):
		wsError(c, 409, "conflict", "版本或幂等请求冲突", false)
	case errors.Is(err, workspace.ErrUnavailable), errors.Is(err, tool.ErrSourceUnavailable):
		wsError(c, 503, "source_unavailable", "依赖服务未就绪", true)
	case errors.Is(err, c.Request.Context().Err()) && c.Request.Context().Err() != nil:
		wsError(c, 504, "timeout", "请求超时或已取消", true)
	default:
		wsError(c, 503, "source_unavailable", "业务记录或依赖暂时不可用", true)
	}
}
func wsBody(c *gin.Context, max int64) ([]byte, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
	b, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			wsError(c, 413, "body_too_large", "请求超过大小限制", false)
		} else {
			wsError(c, 400, "invalid_request", "无法读取请求", false)
		}
		return nil, false
	}
	return b, true
}
func wsJSON(c *gin.Context, dst any, max int64) bool {
	b, ok := wsBody(c, max)
	if !ok {
		return false
	}
	if err := json.Unmarshal(b, dst); err != nil {
		wsError(c, 400, "invalid_request", "JSON 参数无效", false)
		return false
	}
	return true
}
func wsInt(c *gin.Context, key string, def, min, max int) (int, bool) {
	s := c.Query(key)
	if s == "" {
		return def, true
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < min || n > max {
		wsError(c, 400, "invalid_request", key+" 超出范围", false)
		return 0, false
	}
	return n, true
}
func (h *WorkspaceHTTP) Status(c *gin.Context) {
	if _, err := h.Service.DB.Incidents(c.Request.Context()); err != nil {
		wsFail(c, err)
		return
	}
	q := "unavailable"
	if h.Service.Ready(c.Request.Context()) {
		q = "fresh"
	}
	r := "unavailable"
	if h.Service.SourceReady != nil && h.Service.SourceReady(c.Request.Context()) {
		r = "fresh"
	}
	p := "unavailable"
	if h.Prom != nil {
		if _, err := h.Prom.FiringWithContext(c.Request.Context()); err == nil {
			p = "fresh"
		}
	}
	h.statusMu.Lock()
	topology, metrics := "not_configured", "not_configured"
	if h.topologyConfigured {
		topology = "available"
	}
	if h.metricsConfigured {
		metrics = "available"
	}
	h.statusMu.Unlock()
	retrieval := h.dependency("retrieval", r)
	if h.Service.SourceProbeTimes != nil {
		checked, succeeded := h.Service.SourceProbeTimes()
		retrieval["last_checked_at"] = checked
		retrieval["last_success_at"] = succeeded
	}
	wsOK(c, 200, gin.H{"capabilities": gin.H{"workspace": "available", "knowledge": "available", "topology": topology, "metrics": metrics, "global_graph": "deferred"}, "dependencies": gin.H{"sqlite": h.dependency("sqlite", "fresh"), "queue": h.dependency("queue", q), "retrieval": retrieval, "prometheus": h.dependency("prometheus", p)}, "config_fingerprint": h.fingerprint(), "auto_ingest": false, "chat_history": "stateless"}, 1, nil)
}

type pageCursor struct {
	Filter string `json:"filter"`
	Time   string `json:"time"`
	ID     string `json:"id"`
}

func filterHash(c *gin.Context) string {
	q := c.Request.URL.Query()
	q.Del("cursor")
	q.Del("limit")
	sum := sha256.Sum256([]byte(c.FullPath() + "?" + q.Encode()))
	return hex.EncodeToString(sum[:])
}
func decodeCursor(c *gin.Context) (pageCursor, bool) {
	var cur pageCursor
	if raw := c.Query("cursor"); raw != "" {
		if len(raw) > 4096 {
			wsError(c, 400, "invalid_cursor", "游标过长", false)
			return cur, false
		}
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if e != nil || json.Unmarshal(b, &cur) != nil || cur.Filter != filterHash(c) || cur.ID == "" {
			wsError(c, 400, "invalid_cursor", "游标无效或筛选条件已改变", false)
			return cur, false
		}
		if cur.Time != "" {
			if _, e := time.Parse(time.RFC3339Nano, cur.Time); e != nil {
				wsError(c, 400, "invalid_cursor", "游标时间无效", false)
				return cur, false
			}
		}
	}
	return cur, true
}
func pageItems[T any](c *gin.Context, items []T, key func(T) (string, string)) ([]T, gin.H, bool) {
	limit, ok := wsInt(c, "limit", 20, 1, 100)
	if !ok {
		return nil, nil, false
	}
	cur, ok := decodeCursor(c)
	if !ok {
		return nil, nil, false
	}
	sort.Slice(items, func(i, j int) bool {
		ti, ii := key(items[i])
		tj, ij := key(items[j])
		cmp := compareStamp(ti, tj)
		return cmp > 0 || (cmp == 0 && ii > ij)
	})
	out := make([]T, 0, min(limit, len(items)))
	hasMore := false
	for _, item := range items {
		t, id := key(item)
		if cur.ID != "" && (compareStamp(t, cur.Time) > 0 || (compareStamp(t, cur.Time) == 0 && id >= cur.ID)) {
			continue
		}
		if len(out) == limit {
			hasMore = true
			break
		}
		out = append(out, item)
	}
	extra := gin.H{"has_more": hasMore, "next_cursor": nil}
	if hasMore && len(out) > 0 {
		t, id := key(out[len(out)-1])
		b, _ := json.Marshal(pageCursor{Filter: filterHash(c), Time: t, ID: id})
		extra["next_cursor"] = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, extra, true
}
func incidentFilter(c *gin.Context, items []workspace.Incident) ([]workspace.Incident, bool) {
	from, to := "", ""
	for _, field := range []string{"from", "to"} {
		v := c.Query(field)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			wsError(c, 400, "invalid_request", "时间参数无效", false)
			return nil, false
		}
		if field == "from" {
			from = t.UTC().Format(time.RFC3339Nano)
		} else {
			to = t.UTC().Format(time.RFC3339Nano)
		}
	}
	if from != "" && to != "" && compareStamp(from, to) > 0 {
		wsError(c, 400, "invalid_request", "时间窗口无效", false)
		return nil, false
	}
	status := c.Query("status")
	if status != "" && status != "active" && status != "resolved" && status != "unknown" {
		wsError(c, 400, "invalid_request", "事件状态无效", false)
		return nil, false
	}
	out := []workspace.Incident{}
	for _, i := range items {
		if query := strings.ToLower(strings.TrimSpace(c.Query("query"))); query != "" && !strings.Contains(strings.ToLower(i.Name+" "+i.Service+" "+i.Environment), query) {
			continue
		}
		if env := c.Query("environment"); env != "" && env != i.Environment {
			continue
		}
		if service := c.Query("service"); service != "" && service != i.Service {
			continue
		}
		if status != "" && status != i.LifecycleStatus {
			continue
		}
		if from != "" && compareStamp(i.FirstReceivedAt, from) < 0 {
			continue
		}
		if to != "" && compareStamp(i.FirstReceivedAt, to) > 0 {
			continue
		}
		out = append(out, i)
	}
	return out, true
}
func (h *WorkspaceHTTP) Incidents(c *gin.Context) {
	items, err := h.Service.DB.Incidents(c.Request.Context())
	if err != nil {
		wsFail(c, err)
		return
	}
	items, ok := incidentFilter(c, items)
	if !ok {
		return
	}
	out, extra, ok := pageItems(c, items, func(i workspace.Incident) (string, string) { return i.FirstReceivedAt, i.ID })
	if ok {
		wsOK(c, 200, out, 1, extra)
	}
}
func (h *WorkspaceHTTP) Summary(c *gin.Context) {
	items, err := h.Service.DB.Incidents(c.Request.Context())
	if err != nil {
		wsFail(c, err)
		return
	}
	facetsEnv, facetsService := map[string]bool{}, map[string]bool{}
	for _, i := range items {
		facetsEnv[i.Environment] = true
		if env := c.Query("environment"); env == "" || env == i.Environment {
			facetsService[i.Service] = true
		}
	}
	envs, services := []string{}, []string{}
	for env := range facetsEnv {
		envs = append(envs, env)
	}
	for service := range facetsService {
		services = append(services, service)
	}
	sort.Strings(envs)
	sort.Strings(services)
	items, ok := incidentFilter(c, items)
	if !ok {
		return
	}
	summary, err := h.Service.SummaryWindow(c.Request.Context(), items, c.Query("from"), c.Query("to"))
	if err != nil {
		wsFail(c, err)
		return
	}
	summary["facets"] = gin.H{"environments": envs, "services": services}
	distribution := map[string]int{}
	for _, i := range items {
		distribution[i.Severity]++
	}
	summary["severity_distribution"] = distribution
	wsOK(c, 200, summary, 1, gin.H{"coverage": "已接入数据"})
}
func (h *WorkspaceHTTP) Incident(c *gin.Context) {
	i, err := h.Service.DB.Incident(c.Request.Context(), c.Param("id"))
	if err != nil {
		wsFail(c, err)
		return
	}
	all, err := h.Service.DB.Runs(c.Request.Context())
	if err != nil {
		wsFail(c, err)
		return
	}
	runs := []workspace.Run{}
	for _, r := range all {
		for _, id := range r.IncidentIDs {
			if id == i.ID {
				runs = append(runs, r)
				break
			}
		}
	}
	if rid := c.Query("run_id"); rid != "" {
		found := false
		for _, r := range runs {
			if r.ID == rid {
				found = true
			}
		}
		if !found {
			wsError(c, 404, "not_found", "运行不属于该事件", false)
			return
		}
	}
	wsOK(c, 200, gin.H{"incident": i, "runs": runs}, 1, nil)
}
func (h *WorkspaceHTTP) Run(c *gin.Context) {
	r, err := h.Service.DB.Run(c.Request.Context(), c.Param("id"))
	if err != nil {
		wsFail(c, err)
		return
	}
	c.Header("ETag", fmt.Sprintf(`"%d"`, r.Revision))
	wsOK(c, 200, r, r.Revision, nil)
}
func (h *WorkspaceHTTP) Events(c *gin.Context) {
	r, err := h.Service.DB.Run(c.Request.Context(), c.Param("id"))
	if err != nil {
		wsFail(c, err)
		return
	}
	after, ok := wsInt(c, "after_sequence", 0, 0, 1<<30)
	if !ok {
		return
	}
	limit, ok := wsInt(c, "limit", 100, 1, 200)
	if !ok {
		return
	}
	items, err := h.Service.DB.Events(c.Request.Context(), r.ID, after, limit+1)
	if err != nil {
		wsFail(c, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	next := after
	if len(items) > 0 {
		next = items[len(items)-1].Sequence
	}
	wsOK(c, 200, items, r.Revision, gin.H{"has_more": more, "next_after_sequence": next})
}
func (h *WorkspaceHTTP) Evidence(c *gin.Context) {
	ev, err := h.Service.DB.Evidence(c.Request.Context(), c.Param("id"))
	if err != nil {
		wsFail(c, err)
		return
	}
	wsOK(c, 200, ev, 1, nil)
}
func (h *WorkspaceHTTP) Graph(c *gin.Context) {
	depth, ok := wsInt(c, "depth", 1, 1, 2)
	if !ok {
		return
	}
	nodes, ok := wsInt(c, "node_limit", 60, 1, 300)
	if !ok {
		return
	}
	edges, ok := wsInt(c, "edge_limit", 120, 1, 600)
	if !ok {
		return
	}
	g, err := h.Service.Graph(c.Request.Context(), c.Param("id"), c.Query("run_id"), 300, 600)
	if err != nil {
		wsFail(c, err)
		return
	}
	focus := c.Query("focus_id")
	if focus == "" {
		focus = c.Param("id")
	}
	present := false
	for _, n := range g.Nodes {
		if n.ID == focus {
			present = true
		}
	}
	if !present && (len(g.Nodes) > 0 || c.Query("focus_id") != "") {
		wsError(c, 400, "invalid_focus", "焦点不属于当前事件图", false)
		return
	}
	visible := map[string]bool{focus: true}
	if c.Query("focus_id") == "" && c.Query("depth") == "" {
		for _, n := range g.Nodes {
			visible[n.ID] = true
		}
	}
	for d := 0; d < depth; d++ {
		next := map[string]bool{}
		for _, e := range g.Edges {
			if visible[e.Source] {
				next[e.Target] = true
			}
			if visible[e.Target] {
				next[e.Source] = true
			}
		}
		for id := range next {
			visible[id] = true
		}
	}
	allNodes := len(g.Nodes)
	allEdges := len(g.Edges)
	keptNodes := []workspace.Node{}
	kept := map[string]bool{}
	for _, n := range g.Nodes {
		if visible[n.ID] && len(keptNodes) < nodes {
			keptNodes = append(keptNodes, n)
			kept[n.ID] = true
		}
	}
	keptEdges := []workspace.Edge{}
	for _, e := range g.Edges {
		if kept[e.Source] && kept[e.Target] && len(keptEdges) < edges {
			keptEdges = append(keptEdges, e)
		}
	}
	g.Nodes = keptNodes
	g.Edges = keptEdges
	g.OmittedCounts["nodes"] += allNodes - len(g.Nodes)
	g.OmittedCounts["edges"] += allEdges - len(g.Edges)
	g.Truncated = g.Truncated || g.OmittedCounts["nodes"] > 0 || g.OmittedCounts["edges"] > 0
	extra := gin.H{"truncated": g.Truncated}
	if g.AsOf != "" {
		extra["as_of"] = g.AsOf
	}
	wsOK(c, 200, g, g.Revision, extra)
}
func (h *WorkspaceHTTP) ReDiagnose(c *gin.Context) {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		wsError(c, 400, "invalid_request", "Idempotency-Key 必填且不超过200字符", false)
		return
	}
	b, ok := wsBody(c, 32*1024)
	if !ok {
		return
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		b = []byte(`{}`)
	}
	var body any
	if json.Unmarshal(b, &body) != nil {
		wsError(c, 400, "invalid_request", "JSON 参数无效", false)
		return
	}
	canonical, _ := json.Marshal(body)
	r, err := h.Service.DB.ReDiagnose(c.Request.Context(), c.Param("id"), key, string(canonical), h.Service.Ready(c.Request.Context()))
	if err != nil {
		wsFail(c, err)
		return
	}
	_ = h.Service.Dispatch(c.Request.Context())
	if latest, e := h.Service.DB.Run(c.Request.Context(), r.ID); e == nil {
		r = latest
	}
	wsOK(c, 202, gin.H{"run_id": r.ID, "status": r.Status, "dispatch_state": r.DispatchState}, r.Revision, nil)
}
func (h *WorkspaceHTTP) Documents(c *gin.Context) {
	items, err := h.Service.DB.Documents(c.Request.Context())
	if err != nil {
		wsFail(c, err)
		return
	}
	source := c.Query("source_kind")
	if source != "" && source != "demo" && source != "upload" && source != "incident" {
		wsError(c, 400, "invalid_request", "来源类型无效", false)
		return
	}
	filtered := []workspace.Document{}
	for _, d := range items {
		if source != "" && source != d.SourceKind {
			continue
		}
		if q := strings.ToLower(c.Query("query")); q != "" && !strings.Contains(strings.ToLower(d.Title), q) {
			continue
		}
		filtered = append(filtered, d)
	}
	out, extra, ok := pageItems(c, filtered, func(d workspace.Document) (string, string) { return "", d.ID })
	if ok {
		wsOK(c, 200, out, 1, extra)
	}
}
func (h *WorkspaceHTTP) Versions(c *gin.Context) {
	if _, err := h.Service.DB.Document(c.Request.Context(), c.Param("id")); err != nil {
		wsFail(c, err)
		return
	}
	items, err := h.Service.DB.Versions(c.Request.Context(), c.Param("id"))
	if err != nil {
		wsFail(c, err)
		return
	}
	out, extra, ok := pageItems(c, items, func(v workspace.Version) (string, string) { return v.CreatedAt, v.ID })
	if ok {
		wsOK(c, 200, out, 1, extra)
	}
}
func (h *WorkspaceHTTP) Version(c *gin.Context) {
	v, err := h.Service.DB.Version(c.Request.Context(), c.Param("version"))
	if err != nil {
		wsFail(c, err)
		return
	}
	if v.DocumentID != c.Param("id") {
		wsError(c, 404, "not_found", "版本不属于该文档", false)
		return
	}
	wsOK(c, 200, v, 1, nil)
}

type workspaceDocumentReq struct {
	Title       string `json:"title"`
	Content     string `json:"content"`
	Source      string `json:"source_kind"`
	Environment string `json:"environment"`
}

func ifMatch(c *gin.Context) (string, bool) {
	v := strings.Trim(c.GetHeader("If-Match"), `"`)
	if v == "" || v == "*" {
		wsError(c, 400, "invalid_request", "If-Match 必须指定当前版本", false)
		return "", false
	}
	return v, true
}
func (h *WorkspaceHTTP) writeDocument(c *gin.Context, update bool) {
	cancel := setWorkspaceBudget(c)
	defer cancel()

	var req workspaceDocumentReq
	if !wsJSON(c, &req, 600*1024) {
		return
	}
	if len(req.Content) > 512*1024 {
		wsError(c, 413, "body_too_large", "文档超过512KiB", false)
		return
	}
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Content) == "" || len(req.Title) > 512 {
		wsError(c, 400, "invalid_request", "标题和正文必填", false)
		return
	}
	if req.Source == "" {
		req.Source = "upload"
	}
	if req.Source != "upload" {
		wsError(c, 400, "invalid_request", "只能创建人工上传文档", false)
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	expected, id := "", ""
	if update {
		id = c.Param("id")
		var ok bool
		expected, ok = ifMatch(c)
		if !ok {
			return
		}
	} else if key == "" || len(key) > 200 {
		wsError(c, 400, "invalid_request", "Idempotency-Key 必填且不超过200字符", false)
		return
	}
	if h.Service.SourceReady != nil && !h.Service.SourceReady(c.Request.Context()) {
		wsFail(c, workspace.ErrUnavailable)
		return
	}
	d, v, err := h.Service.PutDocument(c.Request.Context(), id, req.Title, req.Content, req.Source, req.Environment, expected, key)
	if err != nil {
		wsFail(c, err)
		return
	}
	status := 201
	if update {
		status = 200
	}
	c.Header("ETag", `"`+v.ID+`"`)
	wsOK(c, status, gin.H{"document": d, "version": v}, 1, nil)
}
func (h *WorkspaceHTTP) CreateDocument(c *gin.Context) { h.writeDocument(c, false) }
func (h *WorkspaceHTTP) UpdateDocument(c *gin.Context) { h.writeDocument(c, true) }
func (h *WorkspaceHTTP) DeleteDocument(c *gin.Context) {
	expected, ok := ifMatch(c)
	if !ok {
		return
	}
	d, err := h.Service.DeleteDocument(c.Request.Context(), c.Param("id"), expected)
	if err != nil {
		wsFail(c, err)
		return
	}
	status := 200
	if d.IndexStatus == "cleanup_pending" {
		status = 202
	}
	wsOK(c, status, d, 1, nil)
}

// Alert is the durable legacy AM admission adapter. Accepted delivery failures remain pending in outbox.
func (h *WorkspaceHTTP) Alert(c *gin.Context) {
	b, ok := wsBody(c, 1<<20)
	if !ok {
		return
	}
	obs, err := workspace.ParseObservations(b)
	if err != nil {
		wsError(c, 400, "invalid_request", "告警格式或批次数量无效", false)
		return
	}
	for _, o := range obs {
		var begin time.Time
		if o.StartsAt != "" {
			var e error
			begin, e = time.Parse(time.RFC3339Nano, o.StartsAt)
			if e != nil {
				wsError(c, 400, "invalid_request", "startsAt 无效", false)
				return
			}
		}
		if o.Status == "resolved" {
			end, e := time.Parse(time.RFC3339Nano, o.EndsAt)
			if o.StartsAt == "" || e != nil || end.Before(begin) {
				wsError(c, 400, "invalid_request", "恢复告警需要有效 startsAt 与 endsAt", false)
				return
			}
		}
	}
	r, duplicate, err := h.Service.DB.Admit(c.Request.Context(), obs, h.Service.Ready(c.Request.Context()))
	if err != nil {
		wsFail(c, err)
		return
	}
	if r == nil {
		c.JSON(200, gin.H{"status": "accepted", "received": len(obs), "diagnosis_enqueued": false})
		return
	}
	_ = h.Service.Dispatch(c.Request.Context())
	if latest, e := h.Service.DB.Run(c.Request.Context(), r.ID); e == nil {
		r = &latest
	}
	status := r.Status
	if status == "succeeded" {
		status = "done"
	}
	c.JSON(202, gin.H{"id": r.ID, "status": status, "received": len(obs), "duplicate": duplicate, "dispatch_state": r.DispatchState})
}
func (h *WorkspaceHTTP) Reports(c *gin.Context) {
	items, err := h.Service.LegacyReports(c.Request.Context())
	if err != nil {
		wsFail(c, err)
		return
	}
	c.JSON(200, gin.H{"reports": items, "count": len(items)})
}
func (h *WorkspaceHTTP) Upload(c *gin.Context) {
	cancel := setWorkspaceBudget(c)
	defer cancel()

	var req uploadReq
	if !wsJSON(c, &req, 600*1024) {
		return
	}
	if len(req.Content) > 512*1024 {
		wsError(c, 413, "body_too_large", "文档超过512KiB", false)
		return
	}
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Content) == "" {
		wsError(c, 400, "invalid_request", "标题和正文必填", false)
		return
	}
	if h.Service.SourceReady != nil && !h.Service.SourceReady(c.Request.Context()) {
		wsFail(c, workspace.ErrUnavailable)
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if key == "" {
		sum := sha256.Sum256([]byte(req.Title + "\x00" + req.Content))
		key = "legacy:" + hex.EncodeToString(sum[:])
	}
	id, expected := "", ""
	prior, e := h.Service.DB.Documents(c.Request.Context())
	if e != nil {
		wsFail(c, e)
		return
	}
	for _, doc := range prior {
		if doc.SourceKind == "upload" && strings.EqualFold(doc.Title, strings.TrimSpace(req.Title)) && doc.DeletedAt == nil {
			id = doc.ID
			if doc.ActiveVersionID != nil {
				expected = *doc.ActiveVersionID
			}
			break
		}
	}
	d, _, err := h.Service.PutDocument(c.Request.Context(), id, req.Title, req.Content, "upload", "unknown", expected, key)
	if err != nil {
		wsFail(c, err)
		return
	}
	items, err := h.Service.DB.Documents(c.Request.Context())
	if err != nil {
		wsFail(c, err)
		return
	}
	c.JSON(200, gin.H{"title": d.Title, "count": len(items), "document_id": d.ID, "active_version_id": d.ActiveVersionID})
}
func (h *WorkspaceHTTP) List(c *gin.Context) {
	items, err := h.Service.DB.Documents(c.Request.Context())
	if err != nil {
		wsFail(c, err)
		return
	}
	titles := []string{}
	for _, d := range items {
		if d.DeletedAt == nil {
			titles = append(titles, d.Title)
		}
	}
	sort.Strings(titles)
	c.JSON(200, gin.H{"titles": titles, "count": len(titles)})
}
func (h *WorkspaceHTTP) Delete(c *gin.Context) {
	title := strings.TrimSpace(c.Query("title"))
	if title == "" {
		var req deleteReq
		if !wsJSON(c, &req, 32*1024) {
			return
		}
		title = strings.TrimSpace(req.Title)
	}
	if title == "" {
		wsError(c, 400, "invalid_request", "标题必填", false)
		return
	}
	docs, err := h.Service.DB.Documents(c.Request.Context())
	if err != nil {
		wsFail(c, err)
		return
	}
	matches := []workspace.Document{}
	for _, d := range docs {
		if d.SourceKind == "upload" && d.DeletedAt == nil && d.Title == title {
			matches = append(matches, d)
		}
	}
	if len(matches) == 0 {
		wsFail(c, sql.ErrNoRows)
		return
	}
	if len(matches) != 1 {
		wsFail(c, workspace.ErrConflict)
		return
	}
	d := matches[0]
	if d.ActiveVersionID == nil {
		wsFail(c, workspace.ErrConflict)
		return
	}
	d, err = h.Service.DeleteDocument(c.Request.Context(), d.ID, *d.ActiveVersionID)
	if err != nil {
		wsFail(c, err)
		return
	}
	n := 0
	for _, item := range docs {
		if item.DeletedAt == nil && item.ID != d.ID {
			n++
		}
	}
	status := 200
	if d.IndexStatus == "cleanup_pending" {
		status = 202
	}
	c.JSON(status, gin.H{"title": d.Title, "count": n, "index_status": d.IndexStatus})
}
func (h *WorkspaceHTTP) Reindex(c *gin.Context) {
	cancel := setWorkspaceBudget(c)
	defer cancel()

	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !wsJSON(c, &req, 1024) {
		return
	}
	if !req.Confirm {
		wsError(c, 400, "confirmation_required", "请明确确认仅重建演示文档", false)
		return
	}
	n, err := h.Service.ReindexDemo(c.Request.Context(), h.DemoDir)
	if err != nil {
		wsFail(c, err)
		return
	}
	if strings.HasPrefix(c.FullPath(), "/api/v1/") {
		wsOK(c, 200, gin.H{"documents": n, "versions": n}, 1, nil)
	} else {
		c.JSON(200, gin.H{"count": n})
	}
}

func compareStamp(a, b string) int {
	ta, ea := time.Parse(time.RFC3339Nano, a)
	tb, eb := time.Parse(time.RFC3339Nano, b)
	if ea == nil && eb == nil {
		if ta.Before(tb) {
			return -1
		}
		if ta.After(tb) {
			return 1
		}
		return 0
	}
	return strings.Compare(a, b)
}

func (h *WorkspaceHTTP) dependency(name, state string) gin.H {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	if h.lastSuccess == nil {
		h.lastSuccess = map[string]string{}
	}
	if state == "fresh" {
		h.lastSuccess[name] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	var last any
	if v := h.lastSuccess[name]; v != "" {
		last = v
	}
	return gin.H{"data_state": state, "last_success_at": last}
}
func (h *WorkspaceHTTP) fingerprint() string {
	b, _ := json.Marshal([]string{h.Config.Embedder.Model, h.Config.Embedder.Host, strconv.Itoa(h.Config.Embedder.Port), h.Config.Qdrant.Collection, h.Config.UI.Mode})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (h *WorkspaceHTTP) setM2Capabilities(topology, metrics bool) {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	h.topologyConfigured = topology
	h.metricsConfigured = metrics
}
