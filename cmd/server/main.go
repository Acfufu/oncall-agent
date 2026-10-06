package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/auth"
	"oncall-agent/internal/config"
	"oncall-agent/internal/handler"
	"oncall-agent/internal/mcpserver"
	"oncall-agent/internal/observability"
	"oncall-agent/internal/queue"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
	"oncall-agent/internal/workspace"
)

func configPath() string {
	if p := os.Getenv("ONCALL_CONFIG"); p != "" {
		return p
	}
	return "config/config.json"
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		mcpMode := fs.Bool("mcp", false, "read-only MCP STDIO")
		_ = fs.Parse(os.Args[2:])
		if !*mcpMode {
			log.Fatal("serve requires --mcp")
		}
		runMCPStdio()
		return
	}
	cfg, err := config.LoadMCP(configPath())
	if err != nil {
		log.Fatalf("configuration unavailable: %v (copy config_template.json to a separate config path)", err)
	}
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if shutdown, err := observability.InitTracer(sigCtx); err == nil {
		defer func() {
			ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = shutdown(ctx)
		}()
	} else {
		log.Printf("tracing unavailable: %v", err)
	}
	if shutdown, err := observability.InitMetrics(sigCtx); err == nil {
		defer func() {
			ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = shutdown(ctx)
		}()
	} else {
		log.Fatalf("metrics initialization: %v", err)
	}
	port := cfg.Qdrant.Port
	if port == 6334 {
		port = 6333
	}
	vectors := store.NewVectorFromHostPort(cfg.Qdrant.Host, port, cfg.Qdrant.Collection)
	emb := rag.SelectEmbedder(cfg.Embedder.Host, cfg.Embedder.Port, cfg.Embedder.Model)
	var sourceOK atomic.Bool
	r := rag.New(vectors, emb)
	r.SetEmbeddingSpace("unavailable", 0)
	db, err := workspace.Open(cfg.Storage.SQLitePath)
	if err != nil {
		log.Fatalf("durable storage unavailable: %v", err)
	}
	defer db.Close()
	q := queue.NewWorkspaceClient(cfg.Queue.RedisAddr, cfg.Queue.Namespace, cfg.Queue.RedisDB)
	defer q.Close()
	s := &workspace.Service{DB: db, RAG: r, SpaceID: "unavailable", Queue: q, QueueReady: func(ctx context.Context) bool {
		if ctx.Err() != nil {
			return false
		}
		return q.Ping() == nil
	}, SourceReady: func(context.Context) bool { return sourceOK.Load() }, Judge: cfg.OpenAI, JudgeThreshold: cfg.Judge.LowThreshold, WebhookURL: cfg.Notify.WebhookURL}
	var checkedAt, successAt atomic.Value
	identity := fmt.Sprintf("%s:%d:%s", cfg.Embedder.Host, cfg.Embedder.Port, cfg.Embedder.Model)
	probe := func(parent context.Context) {
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		err := s.ProbeRetrieval(ctx, vectors, emb, identity)
		if errors.Is(err, workspace.ErrProbeBusy) {
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		checkedAt.Store(now)
		sourceOK.Store(err == nil)
		if err == nil {
			successAt.Store(now)
		} else {
			log.Printf("retrieval probe unavailable; existing collection preserved: %v", err)
		}
	}
	s.SourceProbeTimes = func() (checked, succeeded *string) {
		if v := checkedAt.Load(); v != nil {
			x := v.(string)
			checked = &x
		}
		if v := successAt.Load(); v != nil {
			x := v.(string)
			succeeded = &x
		}
		return
	}
	probe(sigCtx)
	if err = s.RestoreProjection(sigCtx); err != nil {
		log.Fatalf("knowledge recovery: %v", err)
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-sigCtx.Done():
				return
			case <-ticker.C:
				probe(sigCtx)
			}
		}
	}()

	if err = s.Recover(sigCtx); err != nil {
		log.Fatalf("run recovery: %v", err)
	}
	// Demo knowledge is not reindexed automatically at boot; importing it is an authenticated operation.
	h := handler.New(vectors, r, "aiops-docs-demo")
	h.SetAutoIngest(false)
	h.Planner(tool.NewPromClient(cfg.Prometheus.URL), r)
	handler.SetChatAgent(agent.NewReAct(cfg.OpenAI.APIBase, cfg.OpenAI.APIKey, cfg.OpenAI.Model, tool.NewDeps(r, cfg.Prometheus.URL).WithDeploy(tool.DeploySource{Repo: cfg.Deploy.GitHubRepo, Token: cfg.Deploy.GitHubToken})))
	workers := queue.NewWorkspaceServer(cfg.Queue.RedisAddr, cfg.Queue.Namespace, cfg.Queue.RedisDB, s)
	if err = workers.Start(); err != nil {
		log.Printf("queue workers unavailable: %v", err)
	}
	defer workers.Shutdown()
	defer workers.Stop()
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-sigCtx.Done():
				return
			case <-ticker.C:
				ctx, c := context.WithTimeout(sigCtx, 10*time.Second)
				if dispatchErr := s.Dispatch(ctx); dispatchErr != nil && sigCtx.Err() == nil {
					log.Printf("outbox dispatch unavailable: %v", dispatchErr)
				}
				c()
			}
		}
	}()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	_ = router.SetTrustedProxies(nil)
	router.Use(gin.Recovery(), otelgin.Middleware(observability.ServiceName, otelgin.WithFilter(func(req *http.Request) bool { return req.URL.Path != "/ping" && req.URL.Path != "/metrics" })))
	a := auth.New(os.Getenv(cfg.Auth.ConsoleTokenEnv), os.Getenv(cfg.Auth.WebhookTokenEnv), auth.WithLocalhostHTTP(cfg.Auth.LocalhostHTTP))
	a.Register(router)
	router.GET("/ping", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	router.GET("/ready", func(c *gin.Context) {
		status := 200
		if !s.Ready(c.Request.Context()) || !sourceOK.Load() {
			status = 503
		}
		c.JSON(status, gin.H{"ready": status == 200})
	})
	// Protect all business, metrics and MCP paths; static shell contains no business facts.
	guard := a.Middleware()
	router.Use(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/v1/") || p == "/chat" || p == "/alert" || p == "/reports" || p == "/plan" || p == "/upload" || p == "/list" || p == "/delete" || p == "/session" || p == "/reindex" || p == "/mcp" || p == "/metrics" {
			guard(c)
		} else {
			c.Next()
		}
	})
	api := handler.RegisterWorkspace(router, s, tool.NewPromClient(cfg.Prometheus.URL), *cfg)
	if err = api.RegisterM2(router, *cfg); err != nil {
		log.Fatalf("M2 controlled configuration: %v", err)
	}
	router.POST("/alert", api.Alert)
	router.GET("/reports", api.Reports)
	router.POST("/upload", api.Upload)
	router.GET("/list", api.List)
	router.DELETE("/delete", api.Delete)
	router.POST("/reindex", api.Reindex)
	router.POST("/chat", h.Chat)
	router.DELETE("/session", h.SessionClear)
	router.GET("/plan", api.Plan)
	mcp := mcpserver.StreamableHTTPHandler(mcpserver.New(r, cfg.Prometheus.URL, cfg.Deploy.GitHubRepo, cfg.Deploy.GitHubToken))
	router.Any("/mcp", gin.WrapH(mcp))
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))
	if cfg.UI.LegacyEnabled {
		router.StaticFile("/legacy", "web/console.html")
		router.StaticFile("/v01", "web/index.html")
	}
	if cfg.UI.LegacyDefault {
		router.StaticFile("/", "web/console.html")
	} else {
		if _, err = os.Stat("web/dist/index.html"); err != nil {
			log.Fatalf("frontend build missing; run npm ci && npm run build in web/app: %v", err)
		}
		router.Static("/assets", "web/dist/assets")
		router.NoRoute(func(c *gin.Context) {
			if c.Request.Method != "GET" || strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.JSON(404, gin.H{"error": "not found"})
				return
			}
			c.File(filepath.Join("web/dist", "index.html"))
		})
	}
	srv := &http.Server{Addr: fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port), Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		log.Printf("Evidence Workspace listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-sigCtx.Done()
	ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = srv.Shutdown(ctx)
}
func runMCPStdio() {
	cfg, err := config.LoadMCP(configPath())
	if err != nil {
		log.Fatal(err)
	}
	port := cfg.Qdrant.Port
	if port == 6334 {
		port = 6333
	}
	vectors := store.NewVectorFromHostPort(cfg.Qdrant.Host, port, cfg.Qdrant.Collection)
	emb := rag.SelectEmbedder(cfg.Embedder.Host, cfg.Embedder.Port, cfg.Embedder.Model)
	r := rag.New(vectors, emb)
	db, err := workspace.Open(cfg.Storage.SQLitePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	s := workspace.Service{DB: db, RAG: r} // STDIO trusts its local process owner; retrieval still requires a configured validated space.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	v, err := rag.EmbedContext(ctx, emb, "dimension probe")
	if err != nil {
		log.Fatal("embedding unavailable")
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("space-v1:%s:%d:%s:%d", cfg.Embedder.Host, cfg.Embedder.Port, cfg.Embedder.Model, len(v))))
	s.SpaceID = hex.EncodeToString(sum[:])
	if err = vectors.EnsureCompatible(ctx, len(v)); err != nil {
		log.Fatal(err)
	}
	if err = vectors.EnsureSpace(ctx, s.SpaceID); err != nil {
		log.Fatal(err)
	}
	r.SetEmbeddingSpace(s.SpaceID, len(v))
	if err = s.RestoreProjection(ctx); err != nil {
		log.Fatal(err)
	}
	if err = mcpserver.RunStdio(mcpserver.New(r, cfg.Prometheus.URL, cfg.Deploy.GitHubRepo, cfg.Deploy.GitHubToken), ctx); err != nil {
		log.Fatal(err)
	}
}
