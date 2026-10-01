package tool

import (
	"context"

	"oncall-agent/internal/observability"
	"oncall-agent/internal/rag"
)

// Deps 注入只读工具运行时依赖（不改 store/rag/config 结构，只读使用）。
// Deploy 为变更富化第四只读的配置门控源（v0.7 ADR-0009）：Repo 空=工具不注册。
// MCP client 缝已删（v0.7.2/F35，ADR-0004 修订）：白名单本地直调是唯一执行路径，
// 对外 MCP server 见 internal/mcpserver（ADR-0007）。
type Deps struct {
	RAG     *RAGDeps
	Prom    *PromDeps
	PromURL string
	Deploy  DeploySource
}

// NewDeps 由 RAG + Prometheus URL 构造依赖。
func NewDeps(r *rag.RAG, promURL string) *Deps {
	return &Deps{RAG: &RAGDeps{RAG: r}, Prom: &PromDeps{URL: promURL}, PromURL: promURL}
}

// WithDeploy 织入变更源配置（ADR-0009），返回同一 Deps（main.go 接线缝用）。
func (d *Deps) WithDeploy(src DeploySource) *Deps {
	if d == nil {
		return d
	}
	d.Deploy = src
	return d
}

// deployRepo 门控读数：nil 安全。
func (d *Deps) deployRepo() string {
	if d == nil {
		return ""
	}
	return d.Deploy.Repo
}

// Exec 分发白名单工具调用，白名单外拒绝。返回工具结果文本。
// 鉴权/熔断层：白名单校验在先。ragHits 非空时为本次 rag_search 命中（调用方
// 收集引用）。OTel：开 Tool.exec:<name> 子 span（name+args 摘要属性）。
func (d *Deps) Exec(name, argsJSON string) (string, []rag.Result, error) {
	return d.ExecWithContext(context.Background(), name, argsJSON)
}

// ExecWithContext 为 Exec 的 ctx 版：span 挂在传入 ctx 下（chat→tool 树不断）。
func (d *Deps) ExecWithContext(ctx context.Context, name, argsJSON string) (out string, hits []rag.Result, err error) {
	ctx, s := startToolSpan(ctx, name, argsJSON)
	defer func() {
		endToolSpan(s, err)
		// ADR-0004 验收指标：rag_hits_total 经 /metrics 给 Prometheus 直抓。
		// caller=tool 标工具面（F07：与 alert 主链 caller 维度拆分）。
		if name == "rag_search" {
			observability.AddRagHits(ctx, int64(len(hits)), "tool")
		}
	}()
	if !IsAllowed(d.deployRepo(), name) {
		return "", nil, errDeny(d.deployRepo(), name)
	}
	return d.execLocalWithContext(ctx, name, argsJSON)
}

// execLocal 本地实现（唯一执行路径，F35 后无远端分支）。无 ctx 版走 Background。
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
	case "deploy_events":
		src := d.Deploy
		out, _, err := src.DeployEventsWithContext(ctx, argsJSON)
		return out, nil, err
	default:
		return "", nil, errDeny(d.deployRepo(), name)
	}
}
