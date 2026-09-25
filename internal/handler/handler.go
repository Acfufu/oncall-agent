package handler

import (
	"sort"
	"sync"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
)

// Handler holds shared deps for all routes.
// titles is the handler-level doc registry backing /list and /delete;
// vectors live in store via rag and are never edited through this map
// (store/rag 接口本片只读使用，不改)。
type Handler struct {
	Store   *store.VectorStore
	RAG     *rag.RAG
	DemoDir string

	// PlannerAgent 供 GET /plan 只读诊断用；nil 时 Plan() 按需兜底构造。
	PlannerAgent *agent.Planner

	// reports 告警驱动诊断落点环（POST /alert 写，GET /reports 读，ADR-0005）。
	reports reportRing

	mu     sync.RWMutex
	titles map[string]string
}

func New(s *store.VectorStore, r *rag.RAG, demoDir string) *Handler {
	return &Handler{Store: s, RAG: r, DemoDir: demoDir, titles: make(map[string]string)}
}

func errJSON(msg string) map[string]string {
	return map[string]string{"error": msg}
}

func (h *Handler) saveTitle(title, content string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.titles[title] = content
	return len(h.titles)
}

func (h *Handler) removeTitle(title string) (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.titles[title]; !ok {
		return len(h.titles), false
	}
	delete(h.titles, title)
	return len(h.titles), true
}

func (h *Handler) hasTitle(title string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.titles[title]
	return ok
}

func (h *Handler) listTitles() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.titles))
	for t := range h.titles {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func (h *Handler) resetTitles(m map[string]string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.titles = m
	return len(h.titles)
}
