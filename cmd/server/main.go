package main

import (
	"fmt"
	"log"

	"github.com/gin-gonic/gin"

	"oncall-agent/internal/config"
	"oncall-agent/internal/handler"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
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
	if err := s.EnsureCollection(rag.Dim); err != nil {
		log.Printf("warn: ensure collection failed: %v", err)
	}
	emb := rag.NewOllamaEmbedder(
		fmt.Sprintf("http://%s:%d", cfg.Embedder.Host, cfg.Embedder.Port),
		cfg.Embedder.Model,
	)
	r := rag.New(s, emb)

	h := handler.New(s, r, "aiops-docs-demo")
	if _, err := h.ReindexLoad(); err != nil {
		log.Printf("warn: demo preload failed: %v", err)
	}

	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.Use(gin.Recovery())

	e.GET("/ping", h.Ping)
	e.POST("/upload", h.Upload)
	e.GET("/list", h.List)
	e.DELETE("/delete", h.Delete)
	e.POST("/reindex", h.Reindex)
	e.StaticFile("/", "web/index.html")

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("oncall-agent v0.1 listening on %s (memonly=%v)", addr, s.IsMemOnly())
	if err := e.Run(addr); err != nil {
		log.Fatal(err)
	}
}
