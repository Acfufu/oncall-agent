package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/config"
	"oncall-agent/internal/handler"
	"oncall-agent/internal/observability"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
)

func main() {
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
	if _, err := h.ReindexLoad(); err != nil {
		log.Printf("warn: demo preload failed: %v", err)
	}

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
}
