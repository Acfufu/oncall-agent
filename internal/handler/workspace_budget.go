package handler

import (
	"context"
	"github.com/gin-gonic/gin"
	"time"
)

const workspaceExecutionBudget = 30 * time.Second

func setWorkspaceBudget(c *gin.Context) context.CancelFunc {
	ctx, cancel := context.WithTimeout(c.Request.Context(), workspaceExecutionBudget)
	c.Request = c.Request.WithContext(ctx)
	return cancel
}
func workspaceBudget(c *gin.Context) { cancel := setWorkspaceBudget(c); defer cancel(); c.Next() }
