package tool

import (
	"context"
	"encoding/json"
	"time"

	"oncall-agent/internal/rag"
)

// Deps 注入三工具运行时依赖（不改 store/rag/config 结构，只读使用）。
// MCP 为远端会话缝：非空且已连接时 Exec 优先走远端，失联回退本地。
type Deps struct {
	RAG     *RAGDeps
	Prom    *PromDeps
	PromURL string
	MCP     *Client
}

// NewDeps 由 RAG + Prometheus URL 构造依赖。MCP 默认未接（本地直调）。
func NewDeps(r *rag.RAG, promURL string) *Deps {
	return &Deps{RAG: &RAGDeps{RAG: r}, Prom: &PromDeps{URL: promURL}, PromURL: promURL}
}

// WithMCP 织入远端会话，返回同一 Deps（main.go NewDeps 织入点用）。
func (d *Deps) WithMCP(c *Client) *Deps {
	if d == nil {
		return d
	}
	d.MCP = c
	return d
}

// Close 释放 MCP 会话（幂等，空会话无操作）。生命周期由 NewDeps 管理。
func (d *Deps) Close() error {
	if d == nil || d.MCP == nil {
		return nil
	}
	return d.MCP.Close()
}

// Exec 分发白名单工具调用，白名单外拒绝。返回工具结果文本。
// 鉴权/熔断层：白名单校验在先；远端成功用远端，远端失联原样回退本地不炸。
// ragHits 非空时为本次 rag_search 命中（调用方收集引用）。
// OTel：开 Tool.exec:<name> 子 span（name+args 摘要属性），ctx 透传本地分支。
func (d *Deps) Exec(name, argsJSON string) (string, []rag.Result, error) {
	return d.ExecWithContext(context.Background(), name, argsJSON)
}

// ExecWithContext 为 Exec 的 ctx 版：span 挂在传入 ctx 下（chat→tool 树不断）。
func (d *Deps) ExecWithContext(ctx context.Context, name, argsJSON string) (out string, hits []rag.Result, err error) {
	ctx, s := startToolSpan(ctx, name, argsJSON)
	defer func() { endToolSpan(s, err) }()
	if !IsAllowed(name) {
		return "", nil, errDeny(name)
	}
	if o, h, ok := d.execRemote(name, argsJSON); ok {
		return o, h, nil
	}
	return d.execLocalWithContext(ctx, name, argsJSON)
}

// execRemote 经 MCP 会话调远端。ok=false 时调用方回退本地。
func (d *Deps) execRemote(name, argsJSON string) (string, []rag.Result, bool) {
	if d == nil || d.MCP == nil || !d.MCP.Connected() {
		return "", nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := d.MCP.CallTool(ctx, name, argsJSON)
	if err != nil {
		return "", nil, false
	}
	if name == "rag_search" {
		return out, parseRagHits(out), true
	}
	return out, nil, true
}

func parseRagHits(raw string) []rag.Result {
	var arr []struct {
		Doc     string  `json:"doc"`
		Snippet string  `json:"snippet"`
		Score   float32 `json:"score"`
	}
	if err := json.Unmarshal([]byte(raw), &arr); err != nil || len(arr) == 0 {
		return nil
	}
	hits := make([]rag.Result, 0, len(arr))
	for _, a := range arr {
		hits = append(hits, rag.Result{Doc: a.Doc, Snippet: a.Snippet, Score: a.Score})
	}
	return hits
}

// execLocal 本地实现（fallback，永不删除）。无 ctx 版走 Background。
func (d *Deps) execLocal(name, argsJSON string) (string, []rag.Result, error) {
	return d.execLocalWithContext(context.Background(), name, argsJSON)
}

// execLocalWithContext 本地实现 ctx 版：rag/prom 分支透 ctx 打子 span。
func (d *Deps) execLocalWithContext(ctx context.Context, name, argsJSON string) (string, []rag.Result, error) {
	switch name {
	case "time_now":
		return TimeNow(), nil, nil
	case "rag_search":
		if d == nil || d.RAG == nil {
			rd := &RAGDeps{}
			return rd.RagSearchWithContext(ctx, argsJSON)
		}
		return d.RAG.RagSearchWithContext(ctx, argsJSON)
	case "prometheus_query":
		p := &PromDeps{URL: d.PromURL}
		if d != nil && d.Prom != nil {
			p = d.Prom
		}
		out, err := p.PromQueryWithContext(ctx, argsJSON)
		return out, nil, err
	default:
		return "", nil, errDeny(name)
	}
}
