package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/observability"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/tool"
)

// POST /alert + GET /reports（ADR-0005/0006）：告警驱动诊断，报告落内存环供人
// 查看；诊断后置管线 RunAlertDiagnosis 为 HTTP 与队列 worker 共用链。

// 报告状态（ADR-0006 异步契约）：queued 入队 → running 执行中 → done/failed。
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// reportRingCap 报告环容量：存最近 N 条告警驱动诊断，内存态重启即失。
const reportRingCap = 20

// Report 为一条告警驱动诊断的落点记录。Score=0 表示 judge 未评分（降级或关闭）。
type Report struct {
	ID         string       `json:"id"`
	Status     string       `json:"status"`
	ReceivedAt string       `json:"received_at"`
	Alerts     []tool.Alert `json:"alerts"`
	Diagnosis  string       `json:"diagnosis"`
	Citations  []rag.Result `json:"citations"`
	Ingested   int          `json:"ingested"`
	Score      int          `json:"score"`
	LowScore   bool         `json:"low_score"`
}

// reportRing 并发安全的定长报告环，零值可用。
type reportRing struct {
	mu    sync.Mutex
	items []Report
}

func (r *reportRing) add(rep Report) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, rep)
	if len(r.items) > reportRingCap {
		r.items = r.items[len(r.items)-reportRingCap:]
	}
}

// snapshot 返回副本，新→旧排列。
func (r *reportRing) snapshot() []Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Report, len(r.items))
	for i, it := range r.items {
		out[len(r.items)-1-i] = it
	}
	return out
}

// update 按 ID 就地改写环内条目（worker 回填 running/done/failed 用），
// 返回是否命中（条目可能已被环容量驱逐）。
func (r *reportRing) update(id string, mut func(*Report)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.items {
		if r.items[i].ID == id {
			mut(&r.items[i])
			return true
		}
	}
	return false
}

// newReportID 报告 ID：8 字节随机 hex。
func newReportID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// amAlert 为 Alertmanager webhook payload 的单条告警形状。
type amAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Description string            `json:"description"`
	StartsAt    string            `json:"startsAt"`
}

func (a amAlert) toAlert() tool.Alert {
	desc := a.Annotations["description"]
	if desc == "" {
		desc = a.Description
	}
	name := a.Labels["alertname"]
	if name == "" {
		name = a.Labels["alert_name"]
	}
	return tool.Alert{
		Name:        name,
		Severity:    a.Labels["severity"],
		Description: desc,
		Labels:      a.Labels,
		StartsAt:    a.StartsAt,
	}
}

// parseAlertPayload 兼容三种形状：AM webhook（alerts[]）、单条 amAlert、
// tool.Alert（name 直填）。只收 firing（status 空按 firing），无可用告警报错。
func parseAlertPayload(raw []byte) ([]tool.Alert, error) {
	var payload struct {
		Alerts []amAlert `json:"alerts"`
	}
	alerts := []tool.Alert{}
	if err := json.Unmarshal(raw, &payload); err == nil && len(payload.Alerts) > 0 {
		for _, a := range payload.Alerts {
			if a.Status != "" && a.Status != "firing" {
				continue
			}
			alerts = append(alerts, a.toAlert())
		}
		return alerts, nil
	}
	var one amAlert
	if err := json.Unmarshal(raw, &one); err == nil && one.Labels != nil {
		if one.Status == "" || one.Status == "firing" {
			alerts = append(alerts, one.toAlert())
		}
		return alerts, nil
	}
	var direct tool.Alert
	if err := json.Unmarshal(raw, &direct); err == nil && direct.Name != "" {
		alerts = append(alerts, direct)
	}
	return alerts, nil
}

// RunAlertDiagnosis 诊断后置管线（ADR-0005/0006，HTTP 与 asynq worker 共用链）：
// PlanPushed → 事件沉淀 → 落环（命中已有 ID 条目则就地回填，否则新增）→ 指标。
// id 由调用方生成：HTTP 同步路径现生成现用；异步路径在入队时生成并随任务透传。
func (h *Handler) RunAlertDiagnosis(ctx context.Context, id string, alerts []tool.Alert) Report {
	if h.PlannerAgent == nil {
		h.PlannerAgent = agent.New(nil, h.RAG)
	}
	diagnosis, citations := h.PlannerAgent.PlanPushed(ctx, alerts)
	rep := Report{
		ID:         id,
		Status:     StatusDone,
		ReceivedAt: time.Now().UTC().Format(time.RFC3339),
		Alerts:     alerts,
		Diagnosis:  diagnosis,
		Citations:  citations,
	}
	rep.Ingested = h.ingestIncident(alerts, diagnosis)
	if !h.reports.update(id, func(r *Report) { *r = rep }) {
		h.reports.add(rep)
	}
	observability.AddAlertDiagnosis(ctx, 1)
	return rep
}

// Alert 处理 POST /alert：同步走 Plan-Execute 同一条链，报告入环。
func (h *Handler) Alert(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil || len(raw) == 0 {
		c.JSON(http.StatusBadRequest, errJSON("empty payload"))
		return
	}
	alerts, perr := parseAlertPayload(raw)
	if perr != nil {
		c.JSON(http.StatusBadRequest, errJSON("bad payload: "+perr.Error()))
		return
	}
	if len(alerts) == 0 {
		c.JSON(http.StatusBadRequest, errJSON("payload 无可用 firing 告警（需 alertname）"))
		return
	}
	for _, a := range alerts {
		if a.Name == "" {
			c.JSON(http.StatusBadRequest, errJSON("alert 缺 alertname"))
			return
		}
	}
	rep := h.RunAlertDiagnosis(c.Request.Context(), newReportID(), alerts)
	c.JSON(http.StatusOK, gin.H{
		"received":  len(alerts),
		"ingested":  rep.Ingested,
		"diagnosis": rep.Diagnosis,
		"citations": rep.Citations,
	})
}

// Reports 处理 GET /reports：返回最近告警驱动诊断（新→旧）。
func (h *Handler) Reports(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"reports": h.reports.snapshot()})
}

// ingestIncident 事件沉淀（ADR-0005）：每告警名一篇（doc={name}.incident.md），
// 同题覆盖；开关关或 RAG 缺席时跳过。返回入库篇数。
func (h *Handler) ingestIncident(alerts []tool.Alert, diagnosis string) int {
	if !h.autoIngest || h.RAG == nil {
		return 0
	}
	seen := map[string]bool{}
	ingested := 0
	for _, a := range alerts {
		if a.Name == "" || seen[a.Name] {
			continue
		}
		seen[a.Name] = true
		if err := h.RAG.IngestIncident(a.Name+".incident.md", incidentMarkdown(a, diagnosis)); err != nil {
			log.Printf("warn: incident ingest %s failed: %v", a.Name, err)
			continue
		}
		ingested++
	}
	if ingested > 0 {
		observability.AddIncidentIngested(context.Background(), int64(ingested))
	}
	return ingested
}

// incidentMarkdown 沉淀文档形状：一级标题即题（含告警名），标注来源与信任级。
func incidentMarkdown(a tool.Alert, diagnosis string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s 事件沉淀\n\n> AI 诊断报告自动入库（source=incident，检索降权，未经人工审定）。\n\n## 告警\n- alertname: %s\n- severity: %s\n- startsAt: %s\n- description: %s\n\n## 诊断\n%s\n",
		a.Name, a.Name, a.Severity, a.StartsAt, a.Description, diagnosis)
	return sb.String()
}
