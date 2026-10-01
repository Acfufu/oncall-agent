package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// R06：PlannerAgent 懒构造是 h.mu 保护外的 check-then-act——并发首调
// Plan/RunAlertDiagnosis 在 -race 下必报 data race。修复为局部构造不回写
// 共享字段（本测试须在 -race 下跑：修前 FAIL，修后干净）。
func TestPlannerAgentConcurrentLazyConstruction(t *testing.T) {
	h := newTestHandler(t)
	h.PlannerAgent = nil // 显式拆装配，逼出懒构造路径（newTestHandler 预装配了）
	h.SetAutoIngest(false)
	alerts := parseAM(t)

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.GET("/plan", h.Plan)

	// 启动屏障：16 个 goroutine 同时放行，保证都踏在 nil 检查上——
	// 无屏障时首写落地后其余只读，race 窗口太窄测不出。
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				w := httptest.NewRecorder()
				e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/plan", nil))
				return
			}
			_ = h.RunAlertDiagnosis(context.Background(), "r-race", alerts)
		}(i)
	}
	close(start)
	wg.Wait()
}
