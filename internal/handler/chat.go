package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"oncall-agent/internal/agent"
)

// chatAgent 由 main.go 注入（包级持有，不改既有 Handler 结构与 /ping/upload 逻辑）。
var chatAgent *agent.ReAct

// SetChatAgent 注册 /chat 背后的 ReAct 闭环。
func SetChatAgent(a *agent.ReAct) { chatAgent = a }

type chatReq struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id"`
}

// POST /chat {message,session_id} -> {reply,citations:[{doc,snippet}]}。
// 全小写 JSON；错误形如 {"error":"..."}。
func (h *Handler) Chat(c *gin.Context) {
	var req chatReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errJSON("invalid json"))
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		c.JSON(http.StatusBadRequest, errJSON("message required"))
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		req.SessionID = "default"
	}
	if chatAgent == nil {
		c.JSON(http.StatusServiceUnavailable, errJSON("chat not configured (llm key required)"))
		return
	}
	reply, cites, err := chatAgent.Run(c.Request.Context(), req.SessionID, req.Message)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errJSON("chat failed"))
		return
	}
	if cites == nil {
		cites = []agent.Citation{}
	}
	c.JSON(http.StatusOK, gin.H{"reply": reply, "citations": cites})
}
