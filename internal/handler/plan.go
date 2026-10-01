package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/tool"
)

// Planner wires the Plan-Execute deps (readonly prom + rag) into Handler.
// Nil prom/rag tolerated: planner reports explicit no-data.
func (h *Handler) Planner(prom *tool.PromClient, r *rag.RAG) {
	h.PlannerAgent = agent.New(prom, r)
}

// GET /plan -> {alerts,diagnosis,citations} 全小写 JSON，只读。
func (h *Handler) Plan(c *gin.Context) {
	// R06：懒构造只落局部变量不回写共享字段——回写是 h.mu 保护外的
	// check-then-act，并发首调即 data race（-race 实证）。
	planner := h.PlannerAgent
	if planner == nil {
		planner = agent.New(nil, h.RAG)
	}
	alerts, diagnosis, citations := planner.Plan()
	if alerts == nil {
		alerts = []tool.Alert{}
	}
	if citations == nil {
		citations = []rag.Result{}
	}
	c.JSON(http.StatusOK, gin.H{
		"alerts":    alerts,
		"diagnosis": diagnosis,
		"citations": citations,
	})
}
