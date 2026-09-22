// Package agent Plan-Execute 一键诊断（v0.1 只读）：拉告警→检索→报告。
package agent

import (
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
// 无告警 / 无匹配均明示，不编造。
func (p *Planner) Plan() (alerts []tool.Alert, diagnosis string, citations []rag.Result) {
	topK := p.TopK
	if topK <= 0 {
		topK = 3
	}
	if p.Prom != nil {
		if got, err := p.Prom.Firing(); err == nil && got != nil {
			alerts = got
		}
	}
	if alerts == nil {
		alerts = []tool.Alert{}
	}
	if len(alerts) == 0 {
		return alerts, "当前无 firing 告警，无需诊断。", []rag.Result{}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "检出 %d 条 firing 告警，诊断如下：\n", len(alerts))
	seen := map[string]bool{}
	for i, a := range alerts {
		fmt.Fprintf(&sb, "\n%d. [%s] %s (severity=%s, startsAt=%s)\n", i+1, a.Name, a.Description, a.Severity, a.StartsAt)
		query := strings.TrimSpace(a.Name + " " + a.Description)
		var hits []rag.Result
		if p.RAG != nil && query != "" {
			hits, _ = p.RAG.Search(query, topK)
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
	return alerts, sb.String(), citations
}

func truncate(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}
