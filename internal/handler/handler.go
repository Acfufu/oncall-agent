package handler

import (
	"sort"
	"sync"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/config"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
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

	// queue 诊断任务入队缝（ADR-0006）：生产实现 internal/queue.Client（Redis
	// 硬依赖），nil 时 /alert 返回 503；测试注入同步假实现。
	queue AlertEnqueuer

	// judgeLLM 自评分主 LLM 配置（复用 openai 段，ADR-0006）；key 空则跳过评分。
	judgeLLM config.OpenAIConfig

	// judgeThreshold 低分阈值（1-5 分制，score<阈值记 low_score），<=0 关低分标记。
	judgeThreshold int

	// webhookURL 通知写回目标（ADR-0008）：空=通知关闭；notifier 通知任务
	// 入队缝（生产实现 queue.Client，测试假实现）。
	webhookURL string
	notifier   NotifyEnqueuer

	// deploy 变更富化报告链取数源（ADR-0009）：Repo 空=报告不带 deploy_events；
	// BaseURL 缺省 api.github.com，测试可注入 stub。
	deploy tool.DeploySource

	// reports 告警驱动诊断落点环（POST /alert 写，GET /reports 读，ADR-0005）。
	reports reportRing

	// autoIngest 事件沉淀自动入库开关（ADR-0005，默认开）。
	autoIngest bool

	mu      sync.RWMutex
	titles  map[string]string
	sources map[string]string // 标题→来源（demo/upload）：reindex 合并语义的依据（F08）
}

func New(s *store.VectorStore, r *rag.RAG, demoDir string) *Handler {
	return &Handler{
		Store: s, RAG: r, DemoDir: demoDir, autoIngest: true,
		titles:  make(map[string]string),
		sources: make(map[string]string),
	}
}

// SetAutoIngest 设置事件沉淀自动入库开关（config knowledge.auto_ingest）。
func (h *Handler) SetAutoIngest(v bool) { h.autoIngest = v }

// SetQueue 装配诊断任务入队实现（config queue.redis_addr，ADR-0006）。
func (h *Handler) SetQueue(q AlertEnqueuer) { h.queue = q }

// SetJudge 装配自评分配置（config judge.low_threshold，LLM 复用 openai 段）。
func (h *Handler) SetJudge(llm config.OpenAIConfig, threshold int) {
	h.judgeLLM = llm
	h.judgeThreshold = threshold
}

// SetNotify 装配通知写回（ADR-0008）：url 空=关闭（不设 notifier 亦可）。
func (h *Handler) SetNotify(url string, q NotifyEnqueuer) {
	h.webhookURL = url
	h.notifier = q
}

// SetDeploy 装配变更富化报告链取数源（ADR-0009）：repo 空=报告不带 deploy_events。
func (h *Handler) SetDeploy(src tool.DeploySource) {
	h.deploy = src
}

func errJSON(msg string) map[string]string {
	return map[string]string{"error": msg}
}

func (h *Handler) saveTitle(title, content string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.titles[title] = content
	h.sources[title] = "upload"
	return len(h.titles)
}

func (h *Handler) removeTitle(title string) (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.titles[title]; !ok {
		return len(h.titles), false
	}
	delete(h.titles, title)
	delete(h.sources, title)
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

// resetTitles demo 重载的合并语义（F08）：source=demo 的旧条目按目录现状重建
// （目录已消失的文档随之摘除），非 demo（upload）条目保留在册；撞名时 fresh
// 覆盖内容且 source 归 demo。
func (h *Handler) resetTitles(m map[string]string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	merged := make(map[string]string, len(m)+len(h.titles))
	for t, c := range h.titles {
		if h.sources[t] == "demo" {
			continue
		}
		merged[t] = c
	}
	for t, c := range m {
		merged[t] = c
		h.sources[t] = "demo"
	}
	h.titles = merged
	return len(h.titles)
}
