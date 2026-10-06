package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"oncall-agent/internal/auth"
	"oncall-agent/internal/config"
	"oncall-agent/internal/mcpserver"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
	"oncall-agent/internal/workspace"
)

// browserQueue belongs exclusively to the explicit L1 test process. It never serves production.
type browserQueue struct {
	ctx     context.Context
	service *workspace.Service
	workers sync.WaitGroup
}

func (q *browserQueue) EnqueueAlertDiagnosis(id string, alerts []tool.Alert) error {
	if err := q.ctx.Err(); err != nil {
		return err
	}
	q.workers.Add(1)
	go func() { defer q.workers.Done(); _ = q.service.ProcessAlertDiagnosis(q.ctx, id, alerts) }()
	return nil
}
func (q *browserQueue) EnqueueNotification(id string, payload []byte) error {
	if err := q.ctx.Err(); err != nil {
		return err
	}
	q.workers.Add(1)
	go func() { defer q.workers.Done(); _ = q.service.ProcessNotification(q.ctx, id, payload) }()
	return nil
}

// TestWorkspaceBrowserServer is opt-in interactive L1 QA, never an ordinary passing test.
func TestWorkspaceBrowserServer(t *testing.T) {
	if os.Getenv("ONCALL_BROWSER_TEST") != "1" {
		t.Skip("interactive L1 server requires ONCALL_BROWSER_TEST=1")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	dist := filepath.Join(repo, "web/dist")
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		t.Fatalf("build web/app first: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	dbPath := filepath.Join(t.TempDir(), "isolated-browser-facts.sqlite")
	if reused := os.Getenv("ONCALL_BROWSER_DB"); reused != "" {
		reused = filepath.Clean(reused)
		if !filepath.IsAbs(reused) || filepath.Base(reused) != "isolated-browser-facts.sqlite" || !(strings.HasPrefix(reused, os.TempDir()+string(os.PathSeparator)) || strings.HasPrefix(reused, "/tmp/") || strings.HasPrefix(reused, "/private/tmp/")) {
			t.Fatal("browser DB reuse requires an isolated temporary profile path")
		}
		var owner struct {
			Profile string `json:"profile"`
			Space   string `json:"space"`
		}
		b, err := os.ReadFile(filepath.Join(filepath.Dir(reused), "browser-profile-owner.json"))
		if err != nil || json.Unmarshal(b, &owner) != nil || owner.Profile != "oncall-evidence-browser-L1" || owner.Space != "l1-browser/hash/64" {
			t.Fatal("browser DB ownership marker mismatch")
		}
		dbPath = reused
	}
	db, err := workspace.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	vectors := store.NewMemoryVector()
	retrieval := rag.New(vectors, rag.HashEmbedder{})
	retrieval.SetEmbeddingSpace("l1-browser/hash/64", rag.Dim)
	s := &workspace.Service{DB: db, RAG: retrieval, SpaceID: "l1-browser/hash/64", QueueReady: func(ctx context.Context) bool { return ctx.Err() == nil }, SourceReady: func(ctx context.Context) bool { return ctx.Err() == nil }}
	q := &browserQueue{ctx: ctx, service: s}
	s.Queue = q
	if err = s.RestoreProjection(ctx); err != nil {
		t.Fatal(err)
	}
	existingDocs, err := db.Documents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seedExists := false
	for _, d := range existingDocs {
		if d.Title == "CPUHigh L1 测试知识" {
			seedExists = true
		}
		if d.DeletedAt == nil && d.ActiveVersionID != nil {
			v, e := db.Version(ctx, *d.ActiveVersionID)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = retrieval.IndexVersion(ctx, d.ID, v.ID, d.Title, v.Content, d.SourceKind, s.SpaceID); e != nil {
				t.Fatal(e)
			}
		}
	}
	if !seedExists {
		if _, _, err = s.PutDocument(ctx, "", "CPUHigh L1 测试知识", "# CPUHigh\n明确的 L1 浏览器测试知识，来源为测试准备；CPUHigh 表示本次受控测试告警。\n## 核验\n请人工核对原始告警与指标，本测试不执行任何生产处置。", "upload", "test", "", "browser-seed-cpu"); err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	_ = e.SetTrustedProxies(nil)
	e.Use(gin.Recovery())
	a := auth.New("isolated-browser-console", "isolated-browser-webhook", auth.WithLocalhostHTTP(true))
	a.Register(e)
	e.GET("/ping", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok", "test_profile": "L1 isolated browser"}) })
	e.GET("/ready", func(c *gin.Context) {
		c.JSON(200, gin.H{"ready": s.Ready(c.Request.Context()), "test_profile": "L1 isolated browser"})
	})
	guard := a.Middleware()
	e.Use(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/v1/") || p == "/chat" || p == "/alert" || p == "/reports" || p == "/plan" || p == "/upload" || p == "/list" || p == "/delete" || p == "/session" || p == "/reindex" || p == "/mcp" || p == "/metrics" || p == "/test/quit" {
			guard(c)
		} else {
			c.Next()
		}
	})
	fakeProm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/alerts" {
			w.Write([]byte(`{"status":"success","data":{"alerts":[]}}`))
			return
		}
		if r.URL.Path != "/api/v1/query_range" {
			w.WriteHeader(404)
			return
		}
		from, err1 := time.Parse(time.RFC3339Nano, r.URL.Query().Get("start"))
		to, err2 := time.Parse(time.RFC3339Nano, r.URL.Query().Get("end"))
		step, err3 := time.ParseDuration(r.URL.Query().Get("step") + "s")
		if err1 != nil || err2 != nil || err3 != nil || step < 15*time.Second {
			w.WriteHeader(400)
			return
		}
		values := [][2]any{}
		i := 0
		for ts := from; !ts.After(to) && i < 2000; ts = ts.Add(step) {
			if i != 5 {
				v := 0.02 + 0.005*math.Sin(float64(i)/5)
				if strings.Contains(r.URL.Query().Get("query"), "histogram_quantile") {
					v = 0.2 + 0.03*math.Sin(float64(i)/5)
				} else if !strings.Contains(r.URL.Query().Get("query"), "status=~") {
					v = 30 + 5*math.Sin(float64(i)/5)
				}
				values = append(values, [2]any{float64(ts.Unix()) + float64(ts.Nanosecond())/1e9, fmt.Sprintf("%.6f", v)})
			}
			i++
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{"test_profile": "L1 controlled samples"}, "values": values}}}})
	}))
	defer fakeProm.Close()
	profileCfg := config.Default()
	profileCfg.Metrics.TemplatesFile = filepath.Join(repo, "config/metric_templates.json")
	profileCfg.Topology.File = filepath.Join(t.TempDir(), "topology.l1.json")
	topologyJSON := `{"version":"L1-browser-v1","source_ref":"test-profile:controlled-browser-topology","valid_from":"2026-01-01T00:00:00Z","nodes":[{"environment":"test","namespace":"default","service":"browser-api","source_ref":"test-profile:browser-api"},{"environment":"test","namespace":"default","service":"api","source_ref":"test-profile:api"},{"environment":"test","namespace":"default","service":"database","source_ref":"test-profile:database"}],"edges":[{"source":"test/default/browser-api","target":"test/default/database","kind":"declared","source_ref":"test-profile:browser-api-depends-database"},{"source":"test/default/api","target":"test/default/database","kind":"declared","source_ref":"test-profile:api-depends-database"}]}`
	if err = os.WriteFile(profileCfg.Topology.File, []byte(topologyJSON), 0600); err != nil {
		t.Fatal(err)
	}
	h := RegisterWorkspace(e, s, tool.NewPromClient(fakeProm.URL), profileCfg)
	if err = h.RegisterM2(e, profileCfg); err != nil {
		t.Fatal(err)
	}
	h.DemoDir = filepath.Join(repo, "aiops-docs-demo")
	e.POST("/alert", h.Alert)
	e.GET("/reports", h.Reports)
	e.POST("/upload", h.Upload)
	e.GET("/list", h.List)
	e.DELETE("/delete", h.Delete)
	e.POST("/reindex", h.Reindex)
	legacy := New(vectors, retrieval, filepath.Join(repo, "aiops-docs-demo"))
	e.POST("/chat", legacy.Chat)
	e.DELETE("/session", legacy.SessionClear)
	e.GET("/plan", func(c *gin.Context) {
		c.JSON(503, gin.H{"error": "L1 测试未配置 Prometheus，告警请通过 /alert 受控输入", "code": "source_unavailable", "retryable": false})
	})
	mcp := mcpserver.StreamableHTTPHandler(mcpserver.New(retrieval, "", "", ""))
	e.Any("/mcp", gin.WrapH(mcp))
	e.GET("/metrics", gin.WrapH(promhttp.Handler()))
	e.POST("/test/quit", func(c *gin.Context) {
		c.JSON(200, gin.H{"stopping": true, "test_profile": "L1 isolated browser"})
		cancel()
	})
	e.Static("/assets", filepath.Join(dist, "assets"))
	e.StaticFile("/legacy", filepath.Join(repo, "web/console.html"))
	e.StaticFile("/v01", filepath.Join(repo, "web/index.html"))
	e.NoRoute(func(c *gin.Context) {
		if c.Request.Method != "GET" || strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		c.File(filepath.Join(dist, "index.html"))
	})
	listener, err := net.Listen("tcp", "127.0.0.1:18820")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: e, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Logf("L1 isolated browser test profile listening at http://127.0.0.1:18820; sqlite=%s; no fabricated incidents; fake queue, Hash/Memory and controlled Prometheus range samples; expires in 30m", dbPath)
	select {
	case <-ctx.Done():
	case err = <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	}
	cancel()
	shutdown, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = server.Shutdown(shutdown)
	q.workers.Wait()
}
