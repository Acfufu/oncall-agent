// Package agent ReAct 只读闭环（v0.1）：最多 3 轮 LLM->tool->LLM。
// 工具仅 time_now / rag_search / prometheus_query，白名单外拒绝。
// 诊断必须带引用片段 {doc,snippet}，无匹配明示无匹配，不编造。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"oncall-agent/internal/tool"
)

// MaxRounds 为 ReAct 上限。
const MaxRounds = 3

// Citation 为引用片段 {doc,snippet}。
type Citation struct {
	Doc     string `json:"doc"`
	Snippet string `json:"snippet"`
}

// ReAct 持有 OpenAI 兼容配置 + 只读工具 + 会话内存。
type ReAct struct {
	APIBase string
	APIKey  string
	Model   string
	Tools   *tool.Deps
	Client  *http.Client

	mu       sync.Mutex
	sessions map[string][]apiMsg
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

const systemPrompt = `你是 oncall-agent 值班助手（只读）。可用工具仅 time_now / rag_search / prometheus_query，不可做任何写操作（确认/静默告警、改配置等一律拒绝）。
流程：先用 rag_search 查知识库，必要时用 prometheus_query 查指标、time_now 取时间，最多 3 轮工具调用，然后给出诊断。
诊断必须引用知识库原文片段；若 rag_search 无匹配、或检出的引用与问题不相关，必须明示“未找到相关匹配”，不得编造处置步骤，不得硬凑不相关引用作答。`

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
	r.mu.Unlock()

	cites = []Citation{}
	seen := map[string]bool{}

	final, err := r.loop(ctx, hist, &cites, seen)
	if err != nil {
		// LLM 不可用时降级：直接只读检索 + 模板回复，保证入库→检索链可用。
		return r.fallback(ctx, userMsg)
	}

	if len(cites) == 0 && !strings.Contains(final, "未找到相关匹配") {
		// 无引用时强制明示无匹配，不编造。
		if strings.TrimSpace(final) == "" {
			final = "未找到相关匹配：知识库中暂无与该问题相关的内容，未查询到相关告警/指标。请补充故障名或告警名后重试。"
		} else {
			final = "未找到相关匹配：知识库中暂无可引用的相关内容。\n\n" + final
		}
	}

	r.mu.Lock()
	nh := append(hist, apiMsg{Role: "assistant", Content: final})
	if len(nh) > 20 {
		nh = nh[len(nh)-20:]
	}
	r.sessions[sessionID] = nh
	r.mu.Unlock()

	if cites == nil {
		cites = []Citation{}
	}
	return final, cites, nil
}

func (r *ReAct) loop(ctx context.Context, hist []apiMsg, cites *[]Citation, seen map[string]bool) (string, error) {
	msgs := append([]apiMsg{{Role: "system", Content: systemPrompt}}, hist...)
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
			out, hits, terr := r.Tools.ExecWithContext(tctx, tc.Function.Name, tc.Function.Arguments)
			EndCallbackSpan(tctx, terr, 0, 0)
			err := terr
			if err != nil {
				out = "error: " + err.Error()
			}
			for _, h := range hits {
				key := h.Doc + "\x00" + trunc(h.Snippet, 200)
				if !seen[key] {
					seen[key] = true
					*cites = append(*cites, Citation{Doc: h.Doc, Snippet: h.Snippet})
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
		"tools":      tool.Definitions(),
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
	if resp.StatusCode >= 300 {
		msg := ""
		if decoded.Error != nil {
			msg = decoded.Error.Message
		}
		return nil, fmt.Errorf("llm %d: %s", resp.StatusCode, msg)
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
	if r.Tools == nil {
		return "未找到相关匹配：LLM 不可用且检索未配置。请稍后重试。", []Citation{}, nil
	}
	raw, hits, err := r.Tools.ExecWithContext(ctx, "rag_search", `{"query":`+jsonStr(query)+`,"top_k":3}`)
	if err != nil || len(hits) == 0 {
		_ = raw
		return "未找到相关匹配：知识库中暂无与该问题相关的内容，未查询到相关告警/指标。请补充故障名或告警名后重试。", []Citation{}, nil
	}
	cites := make([]Citation, 0, len(hits))
	var sb strings.Builder
	sb.WriteString("根据知识库匹配到以下内容：\n")
	for i, h := range hits {
		cites = append(cites, Citation{Doc: h.Doc, Snippet: h.Snippet})
		fmt.Fprintf(&sb, "\n%d. 【%s】\n%s\n", i+1, h.Doc, trunc(h.Snippet, 500))
	}
	return sb.String(), cites, nil
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
