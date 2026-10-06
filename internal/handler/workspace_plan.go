package handler

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"oncall-agent/internal/workspace"
	"time"
)

// Plan retains the synchronous legacy surface while using the durable incident/run service.
func (h *WorkspaceHTTP) Plan(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	alerts, err := h.Prom.FiringWithContext(ctx)
	if err != nil {
		wsFail(c, workspace.ErrUnavailable)
		return
	}
	if len(alerts) == 0 {
		c.JSON(200, gin.H{"alerts": alerts, "diagnosis": "当前无 firing 告警", "citations": []any{}})
		return
	}
	obs := []workspace.Observation{}
	for _, a := range alerts {
		obs = append(obs, workspace.Observation{Name: a.Name, Severity: a.Severity, Description: a.Description, Labels: a.Labels, StartsAt: a.StartsAt, Status: "firing"})
	}
	run, _, err := h.Service.DB.Admit(ctx, obs, h.Service.Ready(ctx))
	if err != nil {
		wsFail(c, err)
		return
	}
	// Outbox can concurrently dispatch; attempt fencing lets exactly one worker own fact writes.
	executeErr := h.Service.ProcessAlertDiagnosis(ctx, run.ID, alerts)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, e := h.Service.DB.Run(ctx, run.ID)
		if e != nil {
			wsFail(c, e)
			return
		}
		if current.Status == "succeeded" || current.Status == "failed" || current.Status == "cancelled" {
			report, e := h.Service.LegacyReport(ctx, current.ID)
			if e != nil {
				wsFail(c, e)
				return
			}
			if current.Status != "succeeded" {
				report["error"] = "诊断依赖不可用"
				report["code"] = "source_unavailable"
				c.JSON(http.StatusServiceUnavailable, report)
			} else {
				c.JSON(200, report)
			}
			return
		}
		if executeErr != nil && current.Status != "running" {
			wsFail(c, executeErr)
			return
		}
		select {
		case <-ctx.Done():
			wsError(c, 504, "timeout", "同步诊断超时；已接受记录可在报告中查询", true)
			return
		case <-ticker.C:
		}
	}
}
