package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"oncall-agent/internal/config"
	"oncall-agent/internal/judge"
	"oncall-agent/internal/notify"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/tool"
)

type Enqueuer interface {
	EnqueueAlertDiagnosis(string, []tool.Alert) error
	EnqueueNotification(string, []byte) error
}
type Service struct {
	DB               *DB
	RAG              *rag.RAG
	SpaceID          string
	Queue            Enqueuer
	QueueReady       func(context.Context) bool
	SourceReady      func(context.Context) bool
	SourceProbeTimes func() (checked, succeeded *string)
	Judge            config.OpenAIConfig
	JudgeThreshold   int
	WebhookURL       string
	knowledgeMu      sync.RWMutex
	dispatchMu       sync.Mutex
}

func (s *Service) Ready(ctx context.Context) bool {
	return s.Queue != nil && s.QueueReady != nil && s.QueueReady(ctx)
}
func (s *Service) Dispatch(ctx context.Context) error {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	rows, err := s.DB.SQL.QueryContext(ctx, "SELECT id,kind,payload,attempts FROM outbox WHERE state='pending' AND next_at<=? ORDER BY rowid LIMIT 100", stamp())
	if err != nil {
		return err
	}
	items := []Outbox{}
	for rows.Next() {
		var o Outbox
		if err = rows.Scan(&o.ID, &o.Kind, &o.Payload, &o.Attempts); err != nil {
			rows.Close()
			return err
		}
		items = append(items, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range items {
		if err = ctx.Err(); err != nil {
			return err
		}
		var runID string
		if o.Kind == "cleanup" {
			err = s.dispatchCleanup(ctx, o)
			if err == nil {
				continue
			}
		} else if o.Kind == "diagnosis" {
			var p struct {
				RunID        string        `json:"run_id"`
				Observations []Observation `json:"observations"`
			}
			if err = json.Unmarshal(o.Payload, &p); err != nil {
				return err
			}
			runID = p.RunID
			alerts := []tool.Alert{}
			for _, a := range p.Observations {
				if a.Status == "firing" {
					alerts = append(alerts, tool.Alert{Name: a.Name, Severity: a.Severity, Description: a.Description, Labels: a.Labels, StartsAt: a.StartsAt})
				}
			}
			if s.Queue == nil {
				err = ErrUnavailable
			} else {
				err = s.Queue.EnqueueAlertDiagnosis(runID, alerts)
			}
		} else if o.Kind == "notification" {
			var p struct {
				ID string `json:"id"`
			}
			if err = json.Unmarshal(o.Payload, &p); err != nil {
				return err
			}
			runID = p.ID
			if s.Queue == nil {
				err = ErrUnavailable
			} else {
				err = s.Queue.EnqueueNotification(runID, o.Payload)
			}
		} else {
			err = fmt.Errorf("unknown outbox kind")
		}
		if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
			delay := time.Second * time.Duration(1<<min(o.Attempts, 6))
			failure := "queue_unavailable"
			if o.Kind == "cleanup" {
				failure = "cleanup_unavailable"
			}
			retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_, e := s.DB.SQL.ExecContext(retryCtx, "UPDATE outbox SET attempts=attempts+1,next_at=?,last_error=? WHERE id=?", time.Now().UTC().Add(delay).Format(time.RFC3339Nano), failure, o.ID)
			cancel()
			if e != nil {
				return e
			}
			continue
		}
		tx, e := s.DB.SQL.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE outbox SET state='sent',attempts=attempts+1,last_error=NULL WHERE id=?", o.ID)
		if e == nil && o.Kind == "diagnosis" {
			r, re := decodeRow[Run](tx.QueryRowContext(ctx, "SELECT data FROM diagnosis_runs WHERE id=?", runID))
			if re != nil {
				e = re
			} else {
				r.DispatchState = "enqueued"
				r.Revision++
				e = saveRun(ctx, tx, r)
			}
		}
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) Recover(ctx context.Context) error {
	// Single-instance startup: running attempts lost their process. Fence them before re-dispatch.
	tx, err := s.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runs, err := listRows[Run](ctx, tx, "SELECT data FROM diagnosis_runs WHERE status='running'")
	if err != nil {
		return err
	}
	for _, r := range runs {
		ae := &APIError{Code: "interrupted", Message: "上次执行因进程退出中断", Retryable: true}
		if err = addEvent(ctx, tx, r, "queue", "failed", nil, nil, ae); err != nil {
			return err
		}
		r.Status = "queued"
		r.DispatchState = "pending"
		r.CurrentAttempt++
		r.Revision++
		if err = saveRun(ctx, tx, r); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE outbox SET state='pending',next_at=? WHERE dedupe_key=?", stamp(), "diagnosis:"+r.ID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Service) claim(ctx context.Context, id string) (Run, bool, error) {
	tx, err := s.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, false, err
	}
	defer tx.Rollback()
	r, err := decodeRow[Run](tx.QueryRowContext(ctx, "SELECT data FROM diagnosis_runs WHERE id=?", id))
	if err != nil {
		return r, false, err
	}
	if r.Status == "succeeded" || r.Status == "failed" || r.Status == "cancelled" {
		return r, false, nil
	}
	if r.Status == "running" {
		var lease sql.NullString
		if err = tx.QueryRowContext(ctx, "SELECT lease_until FROM diagnosis_runs WHERE id=?", id).Scan(&lease); err != nil {
			return r, false, err
		}
		if lease.Valid && lease.String > stamp() {
			return r, false, fmt.Errorf("run already leased")
		}
	}
	r.CitationIDs = []string{}
	r.EvidenceStatus = "not_evaluated"
	r.ReportText = nil
	if _, err = tx.ExecContext(ctx, "DELETE FROM report_citations WHERE run_id=?", r.ID); err != nil {
		return r, false, err
	}
	r.Status = "running"
	r.CurrentAttempt++
	r.Revision++
	r.StartedAt = ptr(stamp())
	if err = saveRun(ctx, tx, r); err != nil {
		return r, false, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE diagnosis_runs SET lease_until=? WHERE id=?", time.Now().UTC().Add(95*time.Second).Format(time.RFC3339Nano), id)
	if err != nil {
		return r, false, err
	}
	if err = addEvent(ctx, tx, r, "queue", "started", nil, nil, nil); err != nil {
		return r, false, err
	}
	waitMS := int64(0)
	if received, e := time.Parse(time.RFC3339Nano, r.ReceivedAt); e == nil {
		waitMS = max(0, time.Since(received).Milliseconds())
	}
	if err = addEvent(ctx, tx, r, "queue", "succeeded", &waitMS, nil, nil); err != nil {
		return r, false, err
	}
	err = tx.Commit()
	return r, err == nil, err
}
func owned(ctx context.Context, tx *sql.Tx, r Run) error {
	var a int
	var status string
	if err := tx.QueryRowContext(ctx, "SELECT attempt,status FROM diagnosis_runs WHERE id=?", r.ID).Scan(&a, &status); err != nil {
		return err
	}
	if a != r.CurrentAttempt || status != "running" {
		return ErrFenced
	}
	return nil
}
func (s *Service) event(ctx context.Context, r Run, stage, kind string, ms *int64, ref *string, ae *APIError) error {
	tx, err := s.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = owned(ctx, tx, r); err != nil {
		return err
	}
	if err = addEvent(ctx, tx, r, stage, kind, ms, ref, ae); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) ProcessAlertDiagnosis(parent context.Context, id string, _ []tool.Alert) (err error) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	run, ok, err := s.claim(ctx, id)
	if err != nil || !ok {
		return err
	}
	began := time.Now()
	defer func() {
		if err != nil && !errors.Is(err, ErrFenced) {
			code := "source_unavailable"
			message := "诊断依赖失败"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				code = "timeout"
				message = "诊断超时或取消"
			}
			if e := s.finish(context.WithoutCancel(ctx), run, began, "failed", "source_unavailable", SafeReport, nil, &APIError{Code: code, Message: message, Retryable: true}); e != nil && !errors.Is(e, ErrFenced) {
				err = errors.Join(err, e)
			}
		}
		if rec := recover(); rec != nil {
			err = fmt.Errorf("diagnosis panic: %w", asynq.SkipRetry)
			_ = s.finish(context.WithoutCancel(ctx), run, began, "failed", "source_unavailable", SafeReport, nil, &APIError{Code: "internal_error", Message: "诊断执行失败", Retryable: false})
		}
	}()
	return s.diagnose(ctx, run, began)
}
func (s *Service) finish(ctx context.Context, r Run, began time.Time, status, evidence, text string, score *int, ae *APIError) error {
	budget, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	tx, err := s.DB.SQL.BeginTx(budget, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = owned(budget, tx, r); err != nil {
		return err
	}
	// Reload citation and independent notification fields, then atomically publish final report and outbox.
	current, err := decodeRow[Run](tx.QueryRowContext(budget, "SELECT data FROM diagnosis_runs WHERE id=?", r.ID))
	if err != nil {
		return err
	}
	r.CitationIDs = current.CitationIDs
	r.Status = status
	r.EvidenceStatus = evidence
	r.ReportText = ptr(text)
	r.JudgeScore = score
	r.Error = ae
	r.FinishedAt = ptr(stamp())
	elapsed := time.Since(began).Milliseconds()
	r.DurationMS = &elapsed
	r.Revision = current.Revision + 1
	stageKind := "succeeded"
	if status != "succeeded" {
		stageKind = "failed"
	}
	if err = addEvent(budget, tx, r, "report", stageKind, &elapsed, nil, ae); err != nil {
		return err
	}
	if s.WebhookURL != "" && (status == "failed" || (score != nil && s.JudgeThreshold > 0 && *score < s.JudgeThreshold)) {
		r.NotificationStatus = "pending"
		payload, err := s.legacyReportTx(budget, tx, r)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(budget, "INSERT OR IGNORE INTO outbox VALUES(?,?,?,?,'pending',0,?,NULL)", "notification:"+r.ID, "notification", "notification:"+r.ID, []byte(marshal(payload)), stamp())
		if err != nil {
			return err
		}
	} else {
		r.NotificationStatus = "disabled"
		if err = addEvent(budget, tx, r, "notification", "skipped", nil, nil, nil); err != nil {
			return err
		}
	}
	reportEv := Evidence{ID: "report_" + r.ID, Kind: "report", SourceKind: "system", SourceRef: r.ID, RecordedAt: stamp(), Snippet: ptr(text), Metadata: map[string]any{"run_id": r.ID, "evidence_status": evidence, "verification": "unverified"}}
	if _, err = tx.ExecContext(budget, "INSERT OR REPLACE INTO evidence(id,run_id,incident_id,data) VALUES(?,?,NULL,?)", reportEv.ID, r.ID, marshal(reportEv)); err != nil {
		return err
	}
	if err = saveRun(budget, tx, r); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) ProcessNotification(ctx context.Context, id string, payload []byte) error {
	r, err := s.DB.Run(ctx, id)
	if err != nil {
		return err
	}
	if r.NotificationStatus == "sent" {
		return nil
	}
	if s.WebhookURL == "" {
		return asynq.SkipRetry
	}
	started, err := s.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = addEvent(ctx, started, r, "notification", "started", nil, nil, nil); err != nil {
		started.Rollback()
		return err
	}
	if err = started.Commit(); err != nil {
		return err
	}
	began := time.Now()
	sendErr := notify.Post(ctx, s.WebhookURL, payload)
	budget, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	ctx = budget
	tx, err := s.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err = decodeRow[Run](tx.QueryRowContext(ctx, "SELECT data FROM diagnosis_runs WHERE id=?", id))
	if err != nil {
		return err
	}
	ms := time.Since(began).Milliseconds()
	if sendErr != nil {
		r.NotificationStatus = "pending"
		count, ok := asynq.GetRetryCount(ctx)
		maxR, ok2 := asynq.GetMaxRetry(ctx)
		if ok && ok2 && count >= maxR {
			r.NotificationStatus = "failed"
			_, err = tx.ExecContext(ctx, "UPDATE outbox SET state='dead',last_error='notification_failed' WHERE dedupe_key=?", "notification:"+id)
		}
		ae := &APIError{Code: "notification_failed", Message: "通知投递失败", Retryable: r.NotificationStatus != "failed"}
		if err == nil {
			err = addEvent(ctx, tx, r, "notification", "failed", &ms, nil, ae)
		}
	} else {
		r.NotificationStatus = "sent"
		err = addEvent(ctx, tx, r, "notification", "succeeded", &ms, nil, nil)
	}
	r.Revision++
	if err == nil {
		err = saveRun(ctx, tx, r)
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return sendErr
}
func (s *Service) judge(ctx context.Context, r Run, text string, citations []rag.Result) (*int, error) {
	if s.Judge.APIKey == "" || s.Judge.Model == "" {
		return nil, s.event(ctx, r, "judge", "skipped", nil, nil, nil)
	}
	if err := s.event(ctx, r, "judge", "started", nil, nil, nil); err != nil {
		return nil, err
	}
	began := time.Now()
	score, _, err := judge.Score(ctx, s.Judge, text, citations)
	ms := time.Since(began).Milliseconds()
	if err != nil {
		ae := &APIError{Code: "judge_unavailable", Message: "评分服务不可用，未评分", Retryable: true}
		return nil, s.event(ctx, r, "judge", "failed", &ms, nil, ae)
	}
	return &score, s.event(ctx, r, "judge", "succeeded", &ms, nil, nil)
}
