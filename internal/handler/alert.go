package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/observability"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/tool"
)

// POST /alert + GET /reports（ADR-0005）：告警推送入口同步诊断，
// 报告落内存环供人查看；AM webhook 重试会重复投递，环按到达顺序覆盖。

// reportRingCap 报告环容量：存最近 N 条告警驱动诊断，内存态重启即失。
const reportRingCap = 20

// Report 为一条告警驱动诊断的落点记录。
type Report struct {
	ReceivedAt string       `json:"received_at"`
	Alerts     []tool.Alert `json:"alerts"`
	Diagnosis  string       `json:"diagnosis"`
	Citations  []rag.Result `json:"citations"`
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
	if h.PlannerAgent == nil {
		h.PlannerAgent = agent.New(nil, h.RAG)
	}
	diagnosis, citations := h.PlannerAgent.PlanPushed(c.Request.Context(), alerts)
	rep := Report{
		ReceivedAt: time.Now().UTC().Format(time.RFC3339),
		Alerts:     alerts,
		Diagnosis:  diagnosis,
		Citations:  citations,
	}
	h.reports.add(rep)
	observability.AddAlertDiagnosis(c.Request.Context(), 1)
	c.JSON(http.StatusOK, gin.H{
		"received":  len(alerts),
		"diagnosis": diagnosis,
		"citations": citations,
	})
}

// Reports 处理 GET /reports：返回最近告警驱动诊断（新→旧）。
func (h *Handler) Reports(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"reports": h.reports.snapshot()})
}
