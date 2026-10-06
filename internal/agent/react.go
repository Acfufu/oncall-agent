// Package agent ReAct 只读闭环（v0.1）：最多 3 轮 LLM->tool->LLM。
// 工具为 time_now / rag_search / prometheus_query，v0.7 起可按 config 门控
// 追加 deploy_events（ADR-0009），白名单外拒绝。
// 诊断必须带引用片段 {doc,snippet}，无匹配明示无匹配，不编造。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"oncall-agent/internal/observability"
	"oncall-agent/internal/tool"
)

// MaxRounds 为 ReAct 上限。
const MaxRounds = 3

// MaxSessions 为会话表 LRU 上限（R08）：此前 map 只增不减，唯一 session_id
// 永久占内存；触达刷新新近度，超限逐最旧。
const MaxSessions = 256

// Citation 为引用片段 {doc,snippet}。
type Citation struct {
	Doc       string `json:"doc"`
	Snippet   string `json:"snippet"`
	DocID     string `json:"doc_id,omitempty"`
	VersionID string `json:"version_id,omitempty"`
	ChunkID   string `json:"chunk_id,omitempty"`
	Source    string `json:"source,omitempty"`
}

// ReAct 持有 OpenAI 兼容配置 + 只读工具 + 会话内存（LRU 上限 MaxSessions）。
type ReAct struct {
	APIBase string
	APIKey  string
	Model   string
	Tools   *tool.Deps
	Client  *http.Client

	mu       sync.Mutex
	sessions map[string][]apiMsg
	order    []string // LRU 序：front=最旧
}

// NewReAct 构造 ReAct。apiBase 形如 https://api.openai.com/v1。
func NewReAct(apiBase, apiKey, model string, deps *tool.Deps) *ReAct {
	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if apiBase == "" {
		apiBase = "https://api.openai.com/v1"
	}
	return &ReAct{
		APIBase:  apiBase,
		APIKey:   apiKey,
		Model:    model,
		Tools:    deps,
		Client:   &http.Client{Timeout: 300 * time.Second},
		sessions: make(map[string][]apiMsg),
	}
}

type apiMsg struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function toolCallFunction `json:"function"`
}

type toolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// systemPrompt 三只读基础提示词（deploy 关闭态；与 v0.6 逐字节同形，eval 同级可比）。
const systemPrompt = `你是 oncall-agent 值班助手（只读）。可用工具仅 time_now / rag_search / prometheus_query，不可做任何写操作（确认/静默告警、改配置等一律拒绝）。
流程：先用 rag_search 查知识库，必要时用 prometheus_query 查指标、time_now 取时间，最多 3 轮工具调用，然后给出诊断。
诊断必须引用知识库原文片段；若 rag_search 无匹配、或检出的引用与问题不相关，必须明示“未找到相关匹配”，不得编造处置步骤，不得硬凑不相关引用作答。`

// systemPromptDeploy 门控注入第四只读的提示词（ADR-0009：repo 配置态才出现）。
const systemPromptDeploy = `你是 oncall-agent 值班助手（只读）。可用工具仅 time_now / rag_search / prometheus_query / deploy_events，不可做任何写操作（确认/静默告警、改配置等一律拒绝）。
流程：先用 rag_search 查知识库，必要时用 prometheus_query 查指标、time_now 取时间，涉及「最近改了什么」可用 deploy_events 只读查看配置仓库最近提交与部署（since/until 可选，缺省最近 24 小时），最多 3 轮工具调用，然后给出诊断。
诊断必须引用知识库原文片段；若 rag_search 无匹配、或检出的引用与问题不相关，必须明示“未找到相关匹配”，不得编造处置步骤，不得硬凑不相关引用作答。`

// systemPromptFor 按 deploy 门控取提示词；toolsRepo 空=关闭态。
func systemPromptFor(toolsRepo string) string {
	if strings.TrimSpace(toolsRepo) == "" {
		return systemPrompt
	}
	return systemPromptDeploy
}

// Run 执行一轮用户问答，返回 reply + citations。会话历史保存在内存 map。
// OTel：Run 根 span 包全程；ChatModel/Tool 子 span 见 loop/chat/post/fallback。
func (r *ReAct) Run(ctx context.Context, sessionID, userMsg string) (reply string, cites []Citation, err error) {
	ctx, _ = StartRunSpan(ctx, sessionID)
	defer func() { EndCallbackSpan(ctx, err, 0, 0) }()
	userMsg = strings.TrimSpace(userMsg)
	if userMsg == "" {
		return "", nil, fmt.Errorf("message required")
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = "default"
	}

	r.mu.Lock()
	hist := append(append([]apiMsg(nil), r.sessions[sessionID]...), apiMsg{Role: "user", Content: userMsg})
	touchSessionLocked(r.sessions, &r.order, sessionID)
	r.mu.Unlock()

	cites = []Citation{}
	seen := map[string]bool{}

	final, lerr := r.loop(ctx, hist, &cites, seen, userMsg)
	if lerr != nil {
		var toolErr *toolFailure
		if errors.As(lerr, &toolErr) || ctx.Err() != nil {
			return "", []Citation{}, lerr
		}
		// LLM 不可用时降级：直接只读检索 + 模板回复，保证入库→检索链可用。
		// R03：降级必须可见——warn 带原始 loop 错误（key 失效/限流/超时根因
		// 不再只能翻 Jaeger）+ chat_fallback_total 计数器。
		log.Printf("warn: chat LLM loop failed, degraded to rag-direct fallback: %v", lerr)
		observability.AddChatFallback(ctx)
		return r.fallback(ctx, userMsg)
	}

	final = EvidenceReport(cites)

	r.mu.Lock()
	nh := append(hist, apiMsg{Role: "assistant", Content: final})
	if len(nh) > 20 {
		nh = nh[len(nh)-20:]
	}
	r.sessions[sessionID] = nh
	touchSessionLocked(r.sessions, &r.order, sessionID)
	r.mu.Unlock()

	if cites == nil {
		cites = []Citation{}
	}
	return final, cites, nil
}

// touchSessionLocked 刷新 LRU 新近度并淘汰超限最旧会话（调用方持 r.mu）。
func touchSessionLocked(sessions map[string][]apiMsg, order *[]string, id string) {
	o := *order
	for i, s := range o {
		if s == id {
			o = append(o[:i], o[i+1:]...)
			break
		}
	}
	o = append(o, id)
	for len(o) > MaxSessions {
		old := o[0]
		o = o[1:]
		delete(sessions, old)
	}
	*order = o
}

// ClearSessions 清空会话（R08，CONTEXT「可清空」兑现）：id 空=清全部，
// 返回清除的会话数。
func (r *ReAct) ClearSessions(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		n := len(r.sessions)
		r.sessions = make(map[string][]apiMsg)
		r.order = nil
		return n
	}
	if _, ok := r.sessions[sessionID]; !ok {
		return 0
	}
	delete(r.sessions, sessionID)
	for i, s := range r.order {
		if s == sessionID {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return 1
}

// toolsRepo 门控读数：Tools 缺席按关闭态（与 exec 白名单门检同源，ADR-0009）。
func (r *ReAct) toolsRepo() string {
	if r == nil || r.Tools == nil {
		return ""
	}
	return r.Tools.Deploy.Repo
}

func (r *ReAct) loop(ctx context.Context, hist []apiMsg, cites *[]Citation, seen map[string]bool, originalQuery string) (string, error) {
	msgs := append([]apiMsg{{Role: "system", Content: systemPromptFor(r.toolsRepo())}}, hist...)
	for i := 0; i < MaxRounds; i++ {
		resp, err := r.chat(ctx, msgs)
		if err != nil {
			return "", err
		}
		if len(resp.ToolCalls) == 0 {
			msgs = append(msgs, apiMsg{Role: "assistant", Content: resp.Content})
			return resp.Content, nil
		}
		msgs = append(msgs, apiMsg{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			tctx := StartToolSpan(ctx, tc.Function.Name, tc.Function.Arguments)
			if err := ctx.Err(); err != nil {
				EndCallbackSpan(tctx, err, 0, 0)
				return "", err
			}
			if r.Tools == nil || (tc.Function.Name == "rag_search" && (r.Tools.RAG == nil || r.Tools.RAG.RAG == nil)) {
				err := fmt.Errorf("tool source unavailable: %w", tool.ErrSourceUnavailable)
				EndCallbackSpan(tctx, err, 0, 0)
				return "", &toolFailure{err}
			}
			out, hits, terr := r.Tools.ExecWithContext(tctx, tc.Function.Name, tc.Function.Arguments)
			EndCallbackSpan(tctx, terr, 0, 0)
			if terr != nil {
				return "", &toolFailure{fmt.Errorf("%s source unavailable: %w", tc.Function.Name, terr)}
			}
			for _, h := range hits {
				if !qualifiedQuery(ctx, originalQuery, h) {
					continue
				}
				key := h.DocID + "\x00" + h.VersionID + "\x00" + h.ChunkID
				if !seen[key] {
					seen[key] = true
					*cites = append(*cites, citationFromHit(h))
				}
			}
			msgs = append(msgs, apiMsg{Role: "tool", ToolCallID: tc.ID, Content: trunc(out, 4000)})
		}
	}
	// 3 轮耗尽：最后一次不带工具请 LLM 收尾。
	last, err := r.chatFinal(ctx, msgs)
	if err != nil {
		return "", err
	}
	return last, nil
}

type chatRespMsg struct {
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"tool_calls"`
}

func (r *ReAct) chat(ctx context.Context, msgs []apiMsg) (*chatRespMsg, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      r.Model,
		"messages":   msgs,
		"tools":      tool.DefinitionsFor(r.toolsRepo()),
		"max_tokens": 512,
	})
	return r.post(ctx, body)
}

func (r *ReAct) chatFinal(ctx context.Context, msgs []apiMsg) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      r.Model,
		"messages":   msgs,
		"max_tokens": 512,
	})
	out, err := r.post(ctx, body)
	if err != nil {
		return "", err
	}
	return out.Content, nil
}

func (r *ReAct) post(ctx context.Context, body []byte) (out *chatRespMsg, err error) {
	ctx = StartChatModelSpan(ctx, r.Model)
	defer func() { EndCallbackSpan(ctx, err, 0, 0) }()
	if strings.TrimSpace(r.Model) == "" {
		return nil, fmt.Errorf("openai.model missing")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.APIBase+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(r.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(r.APIKey))
	}
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// R12：先查状态码再 decode——网关 502 HTML / 429 空 body 的 decode 失败
	// 会掩盖真实 HTTP 状态，把排障方向带偏。JSON error.message 优先，原文兜底。
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(body))
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		return nil, fmt.Errorf("llm %d: %s", resp.StatusCode, msg)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content   any        `json:"content"`
				ToolCalls []toolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode llm resp: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return nil, fmt.Errorf("llm: empty choices")
	}
	m := decoded.Choices[0].Message
	return &chatRespMsg{Content: strOf(m.Content), ToolCalls: m.ToolCalls}, nil
}

// fallback LLM 失败时直连 rag 只读检索，保证闭环可用。
// OTel：fallback 子 span 包直连检索，token 计 0（模板回复无 LLM 消耗）。
func (r *ReAct) fallback(ctx context.Context, query string) (string, []Citation, error) {
	ctx = StartChatModelSpan(ctx, "fallback:rag-direct")
	var ferr error
	defer func() { EndCallbackSpan(ctx, ferr, 0, 0) }()
	if r.Tools == nil || r.Tools.RAG == nil || r.Tools.RAG.RAG == nil {
		ferr = fmt.Errorf("knowledge source unavailable: %w", tool.ErrSourceUnavailable)
		return "", []Citation{}, ferr
	}
	_, hits, err := r.Tools.ExecWithContext(ctx, "rag_search", `{"query":`+jsonStr(query)+`,"top_k":3}`)
	if err != nil {
		ferr = errors.Join(tool.ErrSourceUnavailable, fmt.Errorf("knowledge source unavailable: %w", err))
		return "", []Citation{}, ferr
	}
	eligible := hits[:0]
	for _, h := range hits {
		if qualifiedQuery(ctx, query, h) {
			eligible = append(eligible, h)
		}
	}
	hits = eligible
	if len(hits) == 0 {
		return SafeReport, []Citation{}, nil
	}

	cites := make([]Citation, 0, len(hits))
	for _, h := range hits {
		cites = append(cites, citationFromHit(h))
	}
	return EvidenceReport(cites), cites, nil
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func strOf(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
