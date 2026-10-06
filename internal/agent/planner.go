// Package agent implements the read-only alert → knowledge → report chain.
package agent

import (
	"context"
	"errors"
	"fmt"
	"oncall-agent/internal/observability"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/tool"
	"strings"
)

type Planner struct {
	Prom *tool.PromClient
	RAG  *rag.RAG
	TopK int
}

func New(prom *tool.PromClient, r *rag.RAG) *Planner { return &Planner{Prom: prom, RAG: r, TopK: 3} }
func (p *Planner) Plan() ([]tool.Alert, string, []rag.Result) {
	return p.PlanWithContext(context.Background())
}

// PlanWithContext retains the legacy shape while making source errors visible.
func (p *Planner) PlanWithContext(ctx context.Context) ([]tool.Alert, string, []rag.Result) {
	alerts, report, cites, err := p.SafePlanWithContext(ctx)
	if err != nil {
		return alerts, "来源不可用（告警源不可达或知识检索失败），本轮未做诊断，请人工研判。", []rag.Result{}
	}
	return alerts, report, cites
}

// SafePlanWithContext preserves dependency failures for the HTTP/worker boundary.
func (p *Planner) SafePlanWithContext(ctx context.Context) (alerts []tool.Alert, diagnosis string, citations []rag.Result, err error) {
	ctx, _ = StartPlanSpan(ctx)
	defer func() { EndCallbackSpan(ctx, err, 0, 0) }()
	alerts = []tool.Alert{}
	citations = []rag.Result{}
	if err = ctx.Err(); err != nil {
		return
	}
	if p.Prom == nil {
		err = fmt.Errorf("alert source unavailable: %w", tool.ErrSourceUnavailable)
		return
	}
	fctx := StartFiringSpan(ctx)
	got, ferr := p.Prom.FiringWithContext(fctx)
	EndCallbackSpan(fctx, ferr, 0, 0)
	if ferr != nil {
		err = errors.Join(tool.ErrSourceUnavailable, fmt.Errorf("alert source unavailable: %w", ferr))
		return
	}
	if got != nil {
		alerts = got
	}
	if len(alerts) == 0 {
		return alerts, "当前无 firing 告警，无需诊断。", citations, nil
	}
	diagnosis, citations, err = p.diagnose(ctx, alerts)
	return
}
func (p *Planner) PlanPushed(ctx context.Context, alerts []tool.Alert) (string, []rag.Result) {
	report, cites, err := p.SafePlanPushed(ctx, alerts)
	if err != nil {
		return "知识来源不可用，本轮未做诊断，请人工研判。", []rag.Result{}
	}
	return report, cites
}
func (p *Planner) SafePlanPushed(ctx context.Context, alerts []tool.Alert) (report string, cites []rag.Result, err error) {
	ctx, _ = StartPlanSpan(ctx)
	defer func() { EndCallbackSpan(ctx, err, 0, 0) }()
	if err = ctx.Err(); err != nil {
		return "", []rag.Result{}, err
	}
	if len(alerts) == 0 {
		return "payload 无 firing 告警，无需诊断。", []rag.Result{}, nil
	}
	return p.diagnose(ctx, alerts)
}
func (p *Planner) diagnose(ctx context.Context, alerts []tool.Alert) (string, []rag.Result, error) {
	topK := p.TopK
	if topK <= 0 {
		topK = 3
	}
	seen := map[string]bool{}
	citations := []rag.Result{}
	if p.RAG == nil {
		return "", citations, fmt.Errorf("knowledge source unavailable: %w", tool.ErrSourceUnavailable)
	}
	for _, a := range alerts {
		if err := ctx.Err(); err != nil {
			return "", []rag.Result{}, err
		}
		query := strings.TrimSpace(a.Name + " " + a.Description)
		hits := []rag.Result{}
		if query != "" {
			rctx := StartRAGSpan(ctx, query, topK)
			h, err := p.RAG.SearchWithContext(rctx, query, topK)
			EndCallbackSpan(rctx, err, 0, 0)
			if err != nil {
				return "", []rag.Result{}, errors.Join(tool.ErrSourceUnavailable, fmt.Errorf("knowledge source unavailable: %w", err))
			}
			for _, hit := range h {
				if qualifiedQuery(rag.WithEnvironment(ctx, a.Labels["environment"]), a.Name, hit) {
					hits = append(hits, hit)
				}
			}
			observability.AddRagHits(ctx, int64(len(hits)), "alert")
		}
		if len(hits) == 0 {
			continue
		}
		for _, h := range hits {
			key := h.DocID + "\x00" + h.VersionID + "\x00" + h.ChunkID
			if !seen[key] {
				seen[key] = true
				citations = append(citations, h)
			}
		}
	}
	if len(citations) == 0 {
		return SafeReport, citations, nil
	}
	qualified := make([]Citation, 0, len(citations))
	for _, hit := range citations {
		qualified = append(qualified, citationFromHit(hit))
	}
	return EvidenceReport(qualified), citations, nil
}
func truncate(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}
