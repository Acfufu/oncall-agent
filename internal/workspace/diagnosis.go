package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"oncall-agent/internal/rag"
)

// Eligible requires an active immutable chunk, applicable environment and an explicit lexical anchor.
// It is deliberately conservative and does not claim semantic verification or calibrated relevance.
func (s *Service) eligible(ctx context.Context, inc Incident, h rag.Result) (Chunk, bool, error) {
	if h.ChunkID == "" || h.VersionID == "" || h.DocID == "" || h.Source == "incident" {
		return Chunk{}, false, nil
	}
	c, err := decodeRow[Chunk](s.DB.SQL.QueryRowContext(ctx, "SELECT data FROM chunks WHERE id=?", h.ChunkID))
	if err != nil {
		return c, false, err
	}
	d, err := s.DB.Document(ctx, h.DocID)
	if err != nil {
		return c, false, err
	}
	if d.DeletedAt != nil || d.ActiveVersionID == nil || *d.ActiveVersionID != h.VersionID || c.VersionID != h.VersionID || c.SpaceID != s.SpaceID {
		return c, false, nil
	}
	if d.Environment != "unknown" && d.Environment != "all" && d.Environment != inc.Environment {
		return c, false, nil
	}
	anchor := strings.ToLower(strings.TrimSpace(inc.Name))
	hay := strings.ToLower(c.Title + "\n" + c.Snippet)
	return c, anchor != "" && strings.Contains(hay, anchor), nil
}
func (s *Service) diagnose(ctx context.Context, r Run, began time.Time) error {
	var report strings.Builder
	allCites := []rag.Result{}
	state := "no_evidence"
	sourceFailed := false
	for _, id := range r.IncidentIDs {
		if err := ctx.Err(); err != nil {
			return s.finish(ctx, r, began, "failed", "source_unavailable", SafeReport, nil, &APIError{Code: "timeout", Message: "诊断超时或取消", Retryable: true})
		}
		inc, err := s.DB.Incident(ctx, id)
		if err != nil {
			return err
		}
		query := inc.Name
		// Exact alert identity is the conservative evidence anchor. Raw annotations remain visible facts.
		retID := stableID("ret_", fmt.Sprintf("%s:%d:%s", r.ID, r.CurrentAttempt, id))
		if err = s.event(ctx, r, "retrieval", "started", nil, ptr(retID), nil); err != nil {
			return err
		}
		start := time.Now()
		var hits []rag.Result
		var searchErr error
		if s.SourceReady != nil && !s.SourceReady(ctx) {
			searchErr = ErrUnavailable
		} else {
			hits, searchErr = s.RAG.SearchWithContext(ctx, query, 3)
		}
		ms := time.Since(start).Milliseconds()
		s.knowledgeMu.RLock() // prevents version activation/deletion while qualifying and recording immutable references
		tx, err := s.DB.SQL.BeginTx(ctx, nil)
		if err != nil {
			s.knowledgeMu.RUnlock()
			return err
		}
		if err = owned(ctx, tx, r); err != nil {
			tx.Rollback()
			s.knowledgeMu.RUnlock()
			return err
		}
		ret := Evidence{ID: retID, Kind: "retrieval", SourceKind: "system", SourceRef: retID, RecordedAt: stamp(), Metadata: map[string]any{"query": query, "status": "succeeded", "retrieval_config_version": "lexical-anchor-v1", "embedding_space_id": s.SpaceID, "attempt": r.CurrentAttempt, "incident_id": id, "run_id": r.ID}}
		if searchErr != nil {
			sourceFailed = true
			ret.Metadata["status"] = "source_unavailable"
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO retrievals VALUES(?,?,?,?)", retID, r.ID, id, marshal(ret))
		if err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO evidence VALUES(?,?,?,?)", ret.ID, r.ID, id, marshal(ret))
		}
		// A completed traversal is recorded even when it returns no eligible citations.
		kind := "succeeded"
		var ae *APIError
		if searchErr != nil {
			kind = "failed"
			ae = &APIError{Code: "source_unavailable", Message: "知识检索源不可用", Retryable: true}
		}
		if err == nil {
			err = addEvent(ctx, tx, r, "retrieval", kind, &ms, ptr(retID), ae)
		}
		if err != nil {
			tx.Rollback()
			s.knowledgeMu.RUnlock()
			return err
		}
		if err = tx.Commit(); err != nil {
			s.knowledgeMu.RUnlock()
			return err
		}
		fmt.Fprintf(&report, "\n[%s] %s（%s / %s）\n", inc.Severity, inc.Name, inc.Environment, inc.Service)
		qualified := 0
		for rank, h := range hits {
			c, ok, e := s.eligible(ctx, inc, h)
			if e != nil {
				s.knowledgeMu.RUnlock()
				return e
			}
			if c.ID == "" {
				continue
			}
			tx, e := s.DB.SQL.BeginTx(ctx, nil)
			if e != nil {
				s.knowledgeMu.RUnlock()
				return e
			}
			if e = owned(ctx, tx, r); e != nil {
				tx.Rollback()
				s.knowledgeMu.RUnlock()
				return e
			}
			hit := map[string]any{"rank": rank + 1, "dense_score": h.DenseScore, "bm25_score": h.BM25Score, "rrf_score": h.RRFScore, "eligibility": ok, "reason": "active version + applicable environment + exact alert-name anchor"}
			_, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO retrieval_hits VALUES(?,?,?)", retID, c.ID, marshal(hit))
			if e == nil && ok {
				evidenceID := stableID("chunk_", fmt.Sprintf("%s:%d:%s:%s", r.ID, r.CurrentAttempt, id, c.ID))
				ev := Evidence{ID: evidenceID, Kind: "document_chunk", SourceKind: c.Source, SourceRef: c.ID, RecordedAt: stamp(), DocumentID: ptr(c.DocumentID), VersionID: ptr(c.VersionID), ChunkID: ptr(c.ID), Snippet: ptr(c.Snippet), Metadata: map[string]any{"snippet_sha256": hash(c.Snippet), "retrieval_id": retID, "attempt": r.CurrentAttempt, "run_id": r.ID, "incident_id": id, "verification": "unverified", "eligibility_policy": "lexical-anchor-v1", "rank": rank + 1}}
				_, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO evidence VALUES(?,?,?,?)", ev.ID, r.ID, id, marshal(ev))
				if e == nil {
					_, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO report_citations VALUES(?,?,?,?,?)", r.ID, id, retID, c.ID, ev.ID)
				}
				if e == nil {
					current, ce := decodeRow[Run](tx.QueryRowContext(ctx, "SELECT data FROM diagnosis_runs WHERE id=?", r.ID))
					if ce != nil {
						e = ce
					} else {
						current.CitationIDs = append(current.CitationIDs, evidenceID)
						current.Revision++
						e = saveRun(ctx, tx, current)
					}
				}
				if e == nil {
					qualified++
					allCites = append(allCites, h)
					fmt.Fprintf(&report, "来源片段【%s / %s】：\n%s\n", h.Doc, c.VersionID, c.Snippet)
				}
			}
			if e != nil {
				tx.Rollback()
				s.knowledgeMu.RUnlock()
				return e
			}
			if e = tx.Commit(); e != nil {
				s.knowledgeMu.RUnlock()
				return e
			}
		}
		s.knowledgeMu.RUnlock()
		if qualified > 0 {
			state = "has_citations"
			report.WriteString("以上为来源记录，尚未独立核验，不代表已确认根因。\n")
		} else if searchErr != nil {
			report.WriteString("知识检索源不可用；本轮未给出处置建议，请人工检查数据源。\n")
		} else {
			report.WriteString(SafeReport + "\n")
		}
	}
	if sourceFailed {
		state = "source_unavailable"
	}
	score, err := s.judge(ctx, r, report.String(), allCites)
	if err != nil {
		return err
	}
	status := "succeeded"
	var ae *APIError
	if sourceFailed {
		status = "failed"
		ae = &APIError{Code: "source_unavailable", Message: "部分检索源不可用", Retryable: true}
	}
	return s.finish(ctx, r, began, status, state, report.String(), score, ae)
}
func (s *Service) legacyReportTx(ctx context.Context, tx *sql.Tx, r Run) (map[string]any, error) {
	rawAlerts, err := listRows[Evidence](ctx, tx, "SELECT data FROM evidence WHERE run_id=? ORDER BY rowid", r.ID)
	if err != nil {
		return nil, err
	}
	obs := []Observation{}
	for _, ev := range rawAlerts {
		if ev.Kind == "alert_observation" {
			var o Observation
			if err = json.Unmarshal([]byte(marshal(ev.Metadata["observation"])), &o); err != nil {
				return nil, err
			}
			obs = append(obs, o)
		}
	}
	evs, err := listRows[Evidence](ctx, tx, "SELECT e.data FROM evidence e JOIN report_citations c ON c.evidence_id=e.id WHERE c.run_id=?", r.ID)
	if err != nil {
		return nil, err
	}
	status := r.Status
	if status == "succeeded" {
		status = "done"
	}
	score := 0
	if r.JudgeScore != nil {
		score = *r.JudgeScore
	}
	cites := []map[string]any{}
	for _, e := range evs {
		title := deref(e.DocumentID)
		if e.ChunkID != nil {
			chunk, ce := decodeRow[Chunk](tx.QueryRowContext(ctx, "SELECT data FROM chunks WHERE id=?", *e.ChunkID))
			if ce != nil {
				return nil, ce
			}
			title = chunk.Title
		}

		cites = append(cites, map[string]any{"doc": title, "snippet": e.Snippet, "source": e.SourceKind, "version_id": e.VersionID, "chunk_id": e.ChunkID})
	}
	text := ""
	if r.ReportText != nil {
		text = *r.ReportText
	}
	alerts := []map[string]any{}
	for _, o := range obs {
		alerts = append(alerts, map[string]any{"name": o.Name, "severity": o.Severity, "description": o.Description, "labels": o.Labels, "startsAt": o.StartsAt, "endsAt": o.EndsAt, "status": o.Status, "annotations": o.Annotations})
	}
	if len(r.IncidentIDs) == 0 {
		archived, archiveErr := decodeRow[Evidence](tx.QueryRowContext(ctx, "SELECT data FROM evidence WHERE id=?", stableID("legacy_", r.ID)))
		if archiveErr == nil {
			if raw, ok := archived.Metadata["legacy_json"].(string); ok {
				var old map[string]any
				if json.Unmarshal([]byte(raw), &old) == nil {
					old["status"] = status
					old["evidence_status"] = "not_evaluated"
					old["legacy_incomplete"] = true
					old["notification_status"] = "disabled"
					return old, nil
				}
			}
		} else if !errors.Is(archiveErr, sql.ErrNoRows) {
			return nil, archiveErr
		}
	}
	return map[string]any{"id": r.ID, "status": status, "received_at": r.ReceivedAt, "alerts": alerts, "diagnosis": text, "citations": cites, "score": score, "low_score": score > 0 && s.JudgeThreshold > 0 && score < s.JudgeThreshold, "ingested": 0, "evidence_status": r.EvidenceStatus, "notification_status": r.NotificationStatus}, nil
}
func (s *Service) LegacyReports(ctx context.Context) ([]map[string]any, error) {
	tx, err := s.DB.SQL.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	runs, err := listRows[Run](ctx, tx, "SELECT data FROM diagnosis_runs ORDER BY received_at DESC,id DESC LIMIT 20")
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, r := range runs {
		rep, e := s.legacyReportTx(ctx, tx, r)
		if e != nil {
			return nil, e
		}
		out = append(out, rep)
	}
	return out, tx.Commit()
}

func (s *Service) LegacyReport(ctx context.Context, id string) (map[string]any, error) {
	tx, err := s.DB.SQL.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	r, err := decodeRow[Run](tx.QueryRowContext(ctx, "SELECT data FROM diagnosis_runs WHERE id=?", id))
	if err != nil {
		return nil, err
	}
	report, err := s.legacyReportTx(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	return report, tx.Commit()
}
