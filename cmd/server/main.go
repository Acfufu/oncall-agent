package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/config"
	"oncall-agent/internal/handler"
	"oncall-agent/internal/mcpserver"
	"oncall-agent/internal/observability"
	"oncall-agent/internal/queue"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
)

func main() {
	// serve --mcp（ADR-0007，对标 k8sgpt serve --mcp）：STDIO 模式只暴露
	// 三只读查询工具，不启 Gin/HTTP/LLM，不需要 api_key。
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		mcpMode := fs.Bool("mcp", false, "expose time_now/rag_search/prometheus_query over MCP STDIO")
		_ = fs.Parse(os.Args[2:])
		if !*mcpMode {
			log.Fatal("serve 需要 --mcp；不带参数直接运行即启 HTTP 服务")
		}
		runMCPStdio()
		return
	}

	cfg, err := config.Load("config/config.json")
	if err != nil {
		log.Printf("warn: %v; using defaults (key needed only for /chat, next slice)", err)
		d := config.Default()
		cfg = &d
	}

	// Qdrant HTTP 默认 6333；template 6334 为 gRPC 端口，HTTP 探测失败时 store 自动降级内存。
	httpPort := cfg.Qdrant.Port
	if httpPort == 6334 {
		httpPort = 6333
	}
	s := store.NewVectorFromHostPort(cfg.Qdrant.Host, httpPort, cfg.Qdrant.Collection)
	var emb rag.Embedder = rag.SelectEmbedder(cfg.Embedder.Host, cfg.Embedder.Port, cfg.Embedder.Model)
	// 探测真实向量维度；Hash 回退恒为 rag.Dim(64)，此时跳过重建避免误判。
	dim := rag.Dim
	if v, err := emb.Embed("dim-probe"); err == nil && len(v) != rag.Dim {
		dim = len(v)
	}
	if cur, err := s.VectorSize(); err == nil && cur > 0 && cur != dim && dim != rag.Dim {
		log.Printf("vector size mismatch (collection=%d, embedder=%d); recreating collection", cur, dim)
		if err := s.RecreateCollection(dim); err != nil {
			log.Printf("warn: recreate collection failed: %v", err)
		}
	} else if err := s.EnsureCollection(dim); err != nil {
		log.Printf("warn: ensure collection failed: %v", err)
	}
	r := rag.New(s, emb)
	r.Floor = rag.DefaultFloor
	// 事件沉淀降权（ADR-0005）：config 可调，缺省 0.5。
	if cfg.Knowledge.IncidentWeight > 0 {
		r.IncidentWeight = cfg.Knowledge.IncidentWeight
	}

	h := handler.New(s, r, "aiops-docs-demo")
	handler.SetChatAgent(agent.NewReAct(cfg.OpenAI.APIBase, cfg.OpenAI.APIKey, cfg.OpenAI.Model, tool.NewDeps(r, cfg.Prometheus.URL)))
	h.Planner(tool.NewPromClient(cfg.Prometheus.URL), r)
	h.SetAutoIngest(cfg.Knowledge.AutoIngest)
	h.SetJudge(cfg.OpenAI, cfg.Judge.LowThreshold)
	if _, err := h.ReindexLoad(); err != nil {
		log.Printf("warn: demo preload failed: %v", err)
	}

	// 诊断队列（ADR-0006）：Redis 硬依赖，同进程收发两端——入队端给 /alert，
	// 消费端 goroutine 调 Handler.ProcessAlertDiagnosis 走共用链。
	qClient := queue.NewClient(cfg.Queue.RedisAddr)
	defer qClient.Close()
	h.SetQueue(qClient)
	qServer := queue.NewServer(cfg.Queue.RedisAddr, h)
	go func() {
		if err := qServer.Start(); err != nil {
			log.Printf("warn: diagnosis queue server exited: %v", err)
		}
	}()

	// OTel providers: trace via OTLP gRPC -> collector -> Jaeger;
	// metrics via /metrics scraped directly by Prometheus (ADR 0004).
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if shutdownTracer, err := observability.InitTracer(sigCtx); err != nil {
		log.Printf("warn: init tracer failed: %v", err)
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownTracer(ctx); err != nil {
				log.Printf("warn: tracer shutdown failed: %v", err)
			}
		}()
	}
	if shutdownMeter, err := observability.InitMetrics(sigCtx); err != nil {
		log.Printf("warn: init metrics failed: %v", err)
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownMeter(ctx); err != nil {
				log.Printf("warn: meter shutdown failed: %v", err)
			}
		}()
	}

	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	// otelgin chain head: server span per request, context flows to downstream
	// via c.Request.Context(). /ping and /metrics filtered out.
	e.Use(otelgin.Middleware(observability.ServiceName, otelgin.WithFilter(func(req *http.Request) bool {
		return req.URL.Path != "/ping" && req.URL.Path != "/metrics"
	})))
	e.Use(gin.Recovery())

	e.GET("/metrics", gin.WrapH(promhttp.Handler()))
	e.GET("/ping", h.Ping)
	e.GET("/plan", h.Plan)
	e.POST("/alert", h.Alert)
	e.GET("/reports", h.Reports)
	// /mcp（ADR-0007）：对外 MCP server StreamableHTTP 传输，与 serve --mcp
	// STDIO 共享同一 server 实例；鉴权不新设（与 /chat 口径一致，README 已知局限）。
	mcpSrv := mcpserver.New(r, cfg.Prometheus.URL)
	e.POST("/mcp", gin.WrapH(mcpserver.StreamableHTTPHandler(mcpSrv)))
	e.GET("/mcp", gin.WrapH(mcpserver.StreamableHTTPHandler(mcpSrv)))
	e.POST("/upload", h.Upload)
	e.POST("/chat", h.Chat)
	e.GET("/list", h.List)
	e.DELETE("/delete", h.Delete)
	e.POST("/reindex", h.Reindex)
	e.StaticFile("/", "web/console.html")
	e.StaticFile("/v01", "web/index.html")

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("oncall-agent v0.1 listening on %s (memonly=%v)", addr, s.IsMemOnly())
	srv := &http.Server{Addr: addr, Handler: e}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-sigCtx.Done()
	stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("warn: server shutdown failed: %v", err)
	}
	// 队列退出序列（ADR-0006）：Stop 停拉取（在途继续），Shutdown 落盘在途任务，
	// Redis 侧未完成任务重投靠事件沉淀同题覆盖幂等。
	qServer.Stop()
	qServer.Shutdown()
	log.Printf("diagnosis queue drained, bye")
}

// runMCPStdio serve --mcp：STDIO 传输跑三只读 MCP server。不启 HTTP/队列/LLM；
// 日志默认走 stderr，不污染 stdout 协议通道。
func runMCPStdio() {
	cfg, err := config.LoadMCP("config/config.json")
	if err != nil {
		log.Fatalf("load config (mcp): %v", err)
	}
	httpPort := cfg.Qdrant.Port
	if httpPort == 6334 {
		httpPort = 6333
	}
	s := store.NewVectorFromHostPort(cfg.Qdrant.Host, httpPort, cfg.Qdrant.Collection)
	emb := rag.SelectEmbedder(cfg.Embedder.Host, cfg.Embedder.Port, cfg.Embedder.Model)
	r := rag.New(s, emb)
	mcpSrv := mcpserver.New(r, cfg.Prometheus.URL)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("oncall-agent MCP server (stdio) starting: tools=time_now,rag_search,prometheus_query")
	if err := mcpserver.RunStdio(mcpSrv, ctx); err != nil {
		log.Fatalf("mcp server: %v", err)
	}
}
