package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// maxBodyBytes 请求体上限（R07）：回环绑定缓解外暴露，但超大 payload 仍可
// 占内存——/alert /upload /chat /delete 统一 2MB。
const maxBodyBytes = 2 << 20

// bindJSON 带 2MB 上限的请求体绑定（R07）：超限回 413、其余绑定错误回 400
// 并返回 false；true 表示绑定成功。
func bindJSON(c *gin.Context, obj any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
	err := c.ShouldBindJSON(obj)
	if err == nil {
		return true
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		c.JSON(http.StatusRequestEntityTooLarge, errJSON("payload too large (2MB limit)"))
	} else {
		c.JSON(http.StatusBadRequest, errJSON("invalid json"))
	}
	return false
}
