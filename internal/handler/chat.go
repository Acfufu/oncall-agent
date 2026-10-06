package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/rag"
)

// chatAgent 由 main.go 注入（包级持有，不改既有 Handler 结构与 /ping/upload 逻辑）。
var chatAgent *agent.ReAct

// SetChatAgent 注册 /chat 背后的 ReAct 闭环。
func SetChatAgent(a *agent.ReAct) { chatAgent = a }

type chatReq struct {
	Message     string `json:"message"`
	SessionID   string `json:"session_id"`
	Environment string `json:"environment"`
}

// POST /chat {message,session_id} -> {reply,citations:[{doc,snippet}]}。
// 全小写 JSON；错误形如 {"error":"..."}。
func (h *Handler) Chat(c *gin.Context) {
	var req chatReq
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	body, err := io.ReadAll(c.Request.Body)
	if err == nil {
		err = json.Unmarshal(body, &req)
	}
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			c.JSON(http.StatusRequestEntityTooLarge, errJSON("chat body exceeds 32KiB"))
		} else {
			c.JSON(http.StatusBadRequest, errJSON("invalid json"))
		}
		return
	}
	if len(req.Message) > 12<<10 {
		c.JSON(http.StatusRequestEntityTooLarge, errJSON("message exceeds 12KiB"))
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		c.JSON(http.StatusBadRequest, errJSON("message required"))
		return
	}
	// Legacy session_id is input compatibility only, never authority to read history.
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		c.JSON(http.StatusServiceUnavailable, errJSON("request session unavailable"))
		return
	}
	req.SessionID = "request:" + hex.EncodeToString(nonce[:])
	if chatAgent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "聊天依赖未就绪", "code": "source_unavailable", "retryable": true, "evidence_status": "source_unavailable"})
		return
	}
	defer chatAgent.ClearSessions(req.SessionID)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if len(req.Environment) > 128 {
		c.JSON(400, errJSON("invalid environment"))
		return
	}
	ctx = rag.WithEnvironment(ctx, req.Environment)
	reply, cites, err := chatAgent.Run(ctx, req.SessionID, req.Message)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": "聊天请求超时", "code": "timeout", "retryable": true, "evidence_status": "source_unavailable"})
		return
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		c.JSON(http.StatusRequestTimeout, gin.H{"error": "聊天请求已取消", "code": "cancelled", "retryable": true})
		return
	}
	if err != nil {
		c.Error(err)
		if agent.IsSourceUnavailable(err) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "知识或工具来源暂时不可用", "code": "source_unavailable", "retryable": true, "evidence_status": "source_unavailable"})
			return
		}
		c.JSON(http.StatusInternalServerError, errJSON("chat failed"))
		return
	}
	if cites == nil {
		cites = []agent.Citation{}
	}
	evidenceStatus := "has_citations"
	if len(cites) == 0 {
		evidenceStatus = "no_evidence"
		reply = agent.SafeReport
	}
	reply = agent.EvidenceReport(cites)
	c.JSON(http.StatusOK, gin.H{"reply": reply, "citations": cites, "evidence_status": evidenceStatus, "history_mode": "stateless"})
}

// DELETE /session retains the legacy response shape for stateless clients.
func (h *Handler) SessionClear(c *gin.Context) {
	// Stateless HTTP chat stores no reusable history and cannot clear another identity.
	c.JSON(http.StatusOK, gin.H{"cleared": 0, "history_mode": "stateless"})
}
