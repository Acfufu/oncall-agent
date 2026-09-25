// Package agent Plan-Execute 一键诊断（v0.1 只读）：拉告警→检索→报告。
package agent

import (
	"context"
	"fmt"
	"strings"

	"oncall-agent/internal/rag"
	"oncall-agent/internal/tool"
)

// Planner 组合只读告警源 + RAG，无写操作。
type Planner struct {
	Prom *tool.PromClient
	RAG  *rag.RAG
	TopK int
}

// New 构造 Planner；prom/rag 允许 nil（nil 时对应步骤明示无数据）。
func New(prom *tool.PromClient, r *rag.RAG) *Planner {
	return &Planner{Prom: prom, RAG: r, TopK: 3}
}

// Plan 执行三步：1) 拉 firing 告警 2) 逐告警 RAG 检索 3) 拼诊断报告（含引用）。
// 无告警 / 无匹配均明示，不编造。无 ctx 版走 Background。
func (p *Planner) Plan() (alerts []tool.Alert, diagnosis string, citations []rag.Result) {
	return p.PlanWithContext(context.Background())
}

// PlanWithContext 为 Plan 的 ctx 版：Plan 根 span 包全程，
// Prom.firing + RAG.search 子 span 包两步只读查询。
func (p *Planner) PlanWithContext(ctx context.Context) (alerts []tool.Alert, diagnosis string, citations []rag.Result) {
	ctx, _ = StartPlanSpan(ctx)
	defer func() { EndCallbackSpan(ctx, nil, 0, 0) }()
	if p.Prom != nil {
		fctx := StartFiringSpan(ctx)
		got, ferr := p.Prom.FiringWithContext(fctx)
		EndCallbackSpan(fctx, ferr, 0, 0)
		if ferr == nil && got != nil {
			alerts = got
		}
	}
	if alerts == nil {
		alerts = []tool.Alert{}
	}
	if len(alerts) == 0 {
		return alerts, "当前无 firing 告警，无需诊断。", []rag.Result{}
	}
	diagnosis, citations = p.diagnose(ctx, alerts)
	return alerts, diagnosis, citations
}

// PlanPushed 推送入口（POST /alert，ADR-0005）：告警由 webhook 传入不拉 Prom，
// 检索与拼报告与 Plan 同一条链，Plan 根 span 同名以示同链。
func (p *Planner) PlanPushed(ctx context.Context, alerts []tool.Alert) (string, []rag.Result) {
	ctx, _ = StartPlanSpan(ctx)
	defer func() { EndCallbackSpan(ctx, nil, 0, 0) }()
	if len(alerts) == 0 {
		return "payload 无 firing 告警，无需诊断。", []rag.Result{}
	}
	return p.diagnose(ctx, alerts)
}

// diagnose 检索+拼报告内核：逐告警 RAG 检索后拼诊断（含引用），
// 无匹配明示无匹配，不编造。调用方保证 alerts 非空。
func (p *Planner) diagnose(ctx context.Context, alerts []tool.Alert) (string, []rag.Result) {
	topK := p.TopK
	if topK <= 0 {
		topK = 3
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "检出 %d 条 firing 告警，诊断如下：\n", len(alerts))
	seen := map[string]bool{}
	var citations []rag.Result
	for i, a := range alerts {
		fmt.Fprintf(&sb, "\n%d. [%s] %s (severity=%s, startsAt=%s)\n", i+1, a.Name, a.Description, a.Severity, a.StartsAt)
		query := strings.TrimSpace(a.Name + " " + a.Description)
		var hits []rag.Result
		if p.RAG != nil && query != "" {
			rctx := StartRAGSpan(ctx, query, topK)
			h, rerr := p.RAG.Search(query, topK)
			EndCallbackSpan(rctx, rerr, 0, 0)
			if rerr == nil {
				hits = h
			}
		}
		if len(hits) == 0 {
			sb.WriteString("   无匹配知识：知识库中未找到相关 runbook，请人工研判。\n")
			continue
		}
		for _, h := range hits {
			if !seen[h.Doc+h.Snippet] {
				seen[h.Doc+h.Snippet] = true
				citations = append(citations, h)
			}
			fmt.Fprintf(&sb, "   - 引用【%s】：%s\n", h.Doc, truncate(h.Snippet, 200))
		}
	}
	if citations == nil {
		citations = []rag.Result{}
	}
	return sb.String(), citations
}

func truncate(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}
