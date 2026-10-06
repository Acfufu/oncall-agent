package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

func (s *Service) Graph(ctx context.Context, incidentID, runID string, nodeLimit, edgeLimit int) (Graph, error) {
	tx, err := s.DB.SQL.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Graph{}, err
	}
	defer tx.Rollback()
	inc, err := decodeRow[Incident](tx.QueryRowContext(ctx, "SELECT data FROM incidents WHERE id=?", incidentID))
	if err != nil {
		return Graph{}, err
	}
	if runID == "" {
		if inc.LatestRunID == nil {
			return Graph{ID: "graph_" + inc.ID, IncidentID: inc.ID, Nodes: []Node{}, Edges: []Edge{}, OmittedCounts: map[string]int{"nodes": 0, "edges": 0}}, nil
		}
		runID = *inc.LatestRunID
	}
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM run_incidents WHERE run_id=? AND incident_id=?", runID, incidentID).Scan(&exists); err != nil {
		return Graph{}, err
	}
	r, err := decodeRow[Run](tx.QueryRowContext(ctx, "SELECT data FROM diagnosis_runs WHERE id=?", runID))
	if err != nil {
		return Graph{}, err
	}
	evs, err := listRows[Evidence](ctx, tx, "SELECT data FROM evidence WHERE run_id=? AND incident_id=? ORDER BY rowid", runID, incidentID)
	if err != nil {
		return Graph{}, err
	}
	g := Graph{Revision: r.Revision, AsOf: stamp(), ID: stableID("graph_", incidentID+runID+fmt.Sprint(r.Revision)), IncidentID: incidentID, RunID: runID, Nodes: []Node{}, Edges: []Edge{}, OmittedCounts: map[string]int{"nodes": 0, "edges": 0}}
	alertID := stableID("alert_", runID+incidentID)
	g.Nodes = append(g.Nodes, Node{ID: inc.ID, Type: "incident", Label: inc.Name, Subtitle: inc.LifecycleStatus, EntityRef: inc.ID, SourceRefs: []string{alertID}, Verification: "not_applicable"}, Node{ID: alertID, Type: "alert", Label: "原始告警", Subtitle: inc.Service, EntityRef: alertID, SourceRefs: []string{alertID}, Verification: "not_applicable"})
	add := func(from, to, relation, ref string) {
		g.Edges = append(g.Edges, Edge{ID: stableID("edge_", from+to+relation), Source: from, Target: to, Relation: relation, Basis: "recorded", SourceRefs: []string{ref}, RecordedAt: r.ReceivedAt, Verification: "not_applicable"})
	}
	add(inc.ID, alertID, "contains", alertID)
	reportID := "report_" + r.ID
	if r.ReportText != nil {
		g.Nodes = append(g.Nodes, Node{ID: reportID, Type: "report", Label: "诊断报告", Subtitle: r.EvidenceStatus, EntityRef: reportID, SourceRefs: []string{reportID}, Verification: "unverified"})
	}
	for _, ev := range evs {
		if a, ok := ev.Metadata["attempt"].(float64); ok && int(a) != r.CurrentAttempt {
			continue
		}
		if ev.Kind == "retrieval" {
			g.Nodes = append(g.Nodes, Node{ID: ev.ID, Type: "retrieval", Label: "检索记录", Subtitle: fmt.Sprint(ev.Metadata["status"]), EntityRef: ev.ID, SourceRefs: []string{ev.ID}, Verification: "not_applicable"})
			add(alertID, ev.ID, "triggered", ev.ID)
			if r.ReportText != nil {
				add(ev.ID, reportID, "produced", ev.ID)
			}
		} else if ev.Kind == "document_chunk" {
			g.Nodes = append(g.Nodes, Node{ID: ev.ID, Type: "document_chunk", Label: "文档片段", Subtitle: deref(ev.VersionID), EntityRef: ev.ID, SourceRefs: []string{ev.ID}, Verification: "unverified"})
			retID := fmt.Sprint(ev.Metadata["retrieval_id"])
			add(retID, ev.ID, "retrieved", ev.ID)
			if r.ReportText != nil {
				add(reportID, ev.ID, "cites", ev.ID)
			}
		}
	}
	if nodeLimit < 1 {
		nodeLimit = 60
	}
	if edgeLimit < 1 {
		edgeLimit = 120
	}
	allN, allE := len(g.Nodes), len(g.Edges)
	if allN > nodeLimit {
		g.Nodes = g.Nodes[:nodeLimit]
	}
	visible := map[string]bool{}
	for _, n := range g.Nodes {
		visible[n.ID] = true
	}
	filtered := []Edge{}
	for _, e := range g.Edges {
		if visible[e.Source] && visible[e.Target] && len(filtered) < edgeLimit {
			filtered = append(filtered, e)
		}
	}
	g.Edges = filtered
	g.OmittedCounts = map[string]int{"nodes": allN - len(g.Nodes), "edges": allE - len(g.Edges)}
	g.Truncated = allN > len(g.Nodes) || allE > len(g.Edges)
	return g, tx.Commit()
}
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (s *Service) Summary(ctx context.Context, incidents []Incident) (map[string]any, error) {
	return s.SummaryWindow(ctx, incidents, "", "")
}
func (s *Service) SummaryWindow(ctx context.Context, incidents []Incident, from, to string) (map[string]any, error) {
	tx, err := s.DB.SQL.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var begin, end time.Time
	if from != "" {
		begin, err = time.Parse(time.RFC3339Nano, from)
		if err != nil {
			return nil, err
		}
	}
	if to != "" {
		end, err = time.Parse(time.RFC3339Nano, to)
		if err != nil {
			return nil, err
		}
	}
	scope := map[string]bool{}
	active, no, unavailable := 0, 0, 0
	for _, i := range incidents {
		scope[i.ID] = true
		if i.LifecycleStatus == "active" {
			active++
		}
		if i.LatestRunID != nil {
			r, e := decodeRow[Run](tx.QueryRowContext(ctx, "SELECT data FROM diagnosis_runs WHERE id=?", *i.LatestRunID))
			if e != nil {
				return nil, e
			}
			if r.EvidenceStatus == "no_evidence" {
				no++
			}
			if r.EvidenceStatus == "source_unavailable" {
				unavailable++
			}
		}
	}
	runs, err := listRows[Run](ctx, tx, "SELECT data FROM diagnosis_runs ORDER BY received_at DESC,id DESC")
	if err != nil {
		return nil, err
	}
	type bucket struct {
		received, succeeded, no, cited, samples int
		duration                                int64
	}
	buckets := map[string]*bucket{}
	dayOf := func(at string) string {
		t, e := time.Parse(time.RFC3339Nano, at)
		if e != nil {
			return ""
		}
		return t.UTC().Format("2006-01-02")
	}
	getBucket := func(day string) *bucket {
		if buckets[day] == nil {
			buckets[day] = &bucket{}
		}
		return buckets[day]
	}
	for _, i := range incidents {
		if day := dayOf(i.FirstReceivedAt); day != "" {
			getBucket(day).received++
		}
	}
	success, cited, samples := 0, 0, 0
	var total int64
	for _, r := range runs {
		included := false
		for _, id := range r.IncidentIDs {
			if scope[id] {
				included = true
			}
		}
		if !included {
			continue
		}
		at, e := time.Parse(time.RFC3339Nano, r.ReceivedAt)
		if e != nil {
			return nil, e
		}
		if (!begin.IsZero() && at.Before(begin)) || (!end.IsZero() && at.After(end)) {
			continue
		}
		day := dayOf(r.ReceivedAt)
		b := getBucket(day)
		if r.EvidenceStatus == "no_evidence" {
			b.no++
		}
		if r.Status == "succeeded" {
			b.succeeded++
			success++
			if len(r.CitationIDs) > 0 {
				cited++
				b.cited++
			}
		}
		if (r.Status == "succeeded" || r.Status == "failed") && r.DurationMS != nil {
			total += *r.DurationMS
			samples++
			b.samples++
			b.duration += *r.DurationMS
		}
	}
	var mean any
	var coverage any
	if samples > 0 {
		mean = float64(total) / float64(samples)
	}
	if success > 0 {
		coverage = float64(cited) / float64(success)
	}
	days := []string{}
	for day := range buckets {
		if day != "" {
			days = append(days, day)
		}
	}
	sort.Strings(days)
	history := []map[string]any{}
	for _, day := range days {
		b := buckets[day]
		var latency, coverage any
		if b.samples > 0 {
			latency = float64(b.duration) / float64(b.samples)
		}
		if b.succeeded > 0 {
			coverage = float64(b.cited) / float64(b.succeeded)
		}
		history = append(history, map[string]any{"timestamp": day + "T00:00:00Z", "received_incidents": b.received, "succeeded_runs": b.succeeded, "no_evidence_runs": b.no, "cited_succeeded_runs": b.cited, "mean_diagnosis_ms": latency, "citation_coverage": coverage, "latency_samples": b.samples})
	}
	return map[string]any{"active_incidents": active, "no_evidence_incidents": no, "source_unavailable_incidents": unavailable, "succeeded_runs": success, "cited_succeeded_runs": cited, "mean_diagnosis_ms": mean, "latency_samples": samples, "citation_coverage": coverage, "comparison": nil, "coverage": "已接入数据", "history": history, "history_basis": "UTC received-day buckets of recorded scoped incidents and runs; no synthetic gaps"}, tx.Commit()
}
