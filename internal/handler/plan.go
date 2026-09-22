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
	if h.PlannerAgent == nil {
		h.PlannerAgent = agent.New(nil, h.RAG)
	}
	alerts, diagnosis, citations := h.PlannerAgent.Plan()
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
