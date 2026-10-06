package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct{ SQL *sql.DB }

func Open(path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	dsn := path + "?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL&_txlock=immediate"
	s, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	s.SetMaxOpenConns(1)
	d := &DB{SQL: s}
	if err = d.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	if path != ":memory:" {
		_ = os.Chmod(path, 0600)
	}
	return d, nil
}
func (d *DB) Close() error { return d.SQL.Close() }
func (d *DB) migrate() error {
	tx, err := d.SQL.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS incidents(id TEXT PRIMARY KEY, canonical_key TEXT NOT NULL UNIQUE, environment TEXT NOT NULL, service TEXT NOT NULL, received_at TEXT NOT NULL, data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS alert_observations(id TEXT PRIMARY KEY, incident_id TEXT REFERENCES incidents(id), source_event_key TEXT NOT NULL UNIQUE, data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS diagnosis_runs(id TEXT PRIMARY KEY, business_key TEXT NOT NULL UNIQUE, status TEXT NOT NULL, received_at TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, lease_until TEXT, revision INTEGER NOT NULL DEFAULT 1, data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS run_incidents(run_id TEXT REFERENCES diagnosis_runs(id), incident_id TEXT REFERENCES incidents(id), PRIMARY KEY(run_id,incident_id));
 CREATE TABLE IF NOT EXISTS run_events(event_id TEXT PRIMARY KEY, run_id TEXT REFERENCES diagnosis_runs(id), sequence INTEGER NOT NULL, data TEXT NOT NULL, UNIQUE(run_id,sequence));
 CREATE TABLE IF NOT EXISTS documents(id TEXT PRIMARY KEY, namespace TEXT NOT NULL, title TEXT NOT NULL, data TEXT NOT NULL, UNIQUE(namespace,title));
 CREATE TABLE IF NOT EXISTS document_versions(id TEXT PRIMARY KEY, document_id TEXT REFERENCES documents(id), content_hash TEXT NOT NULL, data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS chunks(id TEXT PRIMARY KEY, version_id TEXT REFERENCES document_versions(id), data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS retrievals(id TEXT PRIMARY KEY, run_id TEXT REFERENCES diagnosis_runs(id), incident_id TEXT REFERENCES incidents(id), data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS retrieval_hits(retrieval_id TEXT REFERENCES retrievals(id), chunk_id TEXT REFERENCES chunks(id), data TEXT NOT NULL, PRIMARY KEY(retrieval_id,chunk_id));
 CREATE TABLE IF NOT EXISTS report_citations(run_id TEXT REFERENCES diagnosis_runs(id), incident_id TEXT REFERENCES incidents(id), retrieval_id TEXT REFERENCES retrievals(id), chunk_id TEXT REFERENCES chunks(id), evidence_id TEXT NOT NULL UNIQUE, PRIMARY KEY(run_id,incident_id,chunk_id));
 CREATE TABLE IF NOT EXISTS evidence(id TEXT PRIMARY KEY, run_id TEXT REFERENCES diagnosis_runs(id), incident_id TEXT REFERENCES incidents(id), data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS outbox(id TEXT PRIMARY KEY, kind TEXT NOT NULL, dedupe_key TEXT NOT NULL UNIQUE, payload BLOB NOT NULL, state TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, next_at TEXT NOT NULL, last_error TEXT);
 CREATE TABLE IF NOT EXISTS idempotency(scope TEXT NOT NULL, key TEXT NOT NULL, body_hash TEXT NOT NULL, result_id TEXT NOT NULL, PRIMARY KEY(scope,key));
 INSERT OR IGNORE INTO schema_migrations VALUES(1);`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func decodeRow[T any](row *sql.Row) (T, error) {
	var v T
	var raw string
	err := row.Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &v)
	}
	return v, err
}
func listRows[T any](ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		var v T
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (d *DB) Run(ctx context.Context, id string) (Run, error) {
	return decodeRow[Run](d.SQL.QueryRowContext(ctx, "SELECT data FROM diagnosis_runs WHERE id=?", id))
}
func (d *DB) Incident(ctx context.Context, id string) (Incident, error) {
	return decodeRow[Incident](d.SQL.QueryRowContext(ctx, "SELECT data FROM incidents WHERE id=?", id))
}
func (d *DB) Document(ctx context.Context, id string) (Document, error) {
	return decodeRow[Document](d.SQL.QueryRowContext(ctx, "SELECT data FROM documents WHERE id=?", id))
}
func (d *DB) Version(ctx context.Context, id string) (Version, error) {
	return decodeRow[Version](d.SQL.QueryRowContext(ctx, "SELECT data FROM document_versions WHERE id=?", id))
}
func (d *DB) Evidence(ctx context.Context, id string) (Evidence, error) {
	return decodeRow[Evidence](d.SQL.QueryRowContext(ctx, "SELECT data FROM evidence WHERE id=?", id))
}
func (d *DB) Runs(ctx context.Context) ([]Run, error) {
	return listRows[Run](ctx, d.SQL, "SELECT data FROM diagnosis_runs ORDER BY received_at DESC,id DESC")
}
func (d *DB) Incidents(ctx context.Context) ([]Incident, error) {
	return listRows[Incident](ctx, d.SQL, "SELECT data FROM incidents ORDER BY received_at DESC,id DESC")
}
func (d *DB) Documents(ctx context.Context) ([]Document, error) {
	return listRows[Document](ctx, d.SQL, "SELECT data FROM documents ORDER BY title,id")
}
func (d *DB) Versions(ctx context.Context, id string) ([]Version, error) {
	return listRows[Version](ctx, d.SQL, "SELECT data FROM document_versions WHERE document_id=? ORDER BY rowid DESC", id)
}
func (d *DB) Events(ctx context.Context, id string, after, limit int) ([]Event, error) {
	return listRows[Event](ctx, d.SQL, "SELECT data FROM run_events WHERE run_id=? AND sequence>? ORDER BY sequence LIMIT ?", id, after, limit)
}
func (d *DB) Backup(ctx context.Context, path string) error {
	if path == "" || strings.ContainsRune(path, 0) {
		return fmt.Errorf("invalid backup path")
	}
	_, err := d.SQL.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}
func saveRun(ctx context.Context, tx *sql.Tx, r Run) error {
	_, err := tx.ExecContext(ctx, "UPDATE diagnosis_runs SET status=?,attempt=?,revision=?,data=? WHERE id=?", r.Status, r.CurrentAttempt, r.Revision, marshal(r), r.ID)
	return err
}
func addEvent(ctx context.Context, tx *sql.Tx, r Run, stage, kind string, ms *int64, ref *string, ae *APIError) error {
	var seq int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0)+1 FROM run_events WHERE run_id=?", r.ID).Scan(&seq); err != nil {
		return err
	}
	executionStage := "queue"
	if stage == "notification" {
		executionStage = "notification"
	}
	var attempts int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM run_events WHERE run_id=? AND json_extract(data,'$.stage')=? AND json_extract(data,'$.kind')='started'", r.ID, executionStage).Scan(&attempts); err != nil {
		return err
	}
	if stage == executionStage && kind == "started" {
		attempts++
	}
	attempts = max(1, attempts)
	e := Event{EventID: stableID("evt_", fmt.Sprintf("%s:%d", r.ID, seq)), RunID: r.ID, Attempt: r.CurrentAttempt, Sequence: seq, Stage: stage, StageAttempt: attempts, Kind: kind, OccurredAt: stamp(), DurationMS: ms, ResultRef: ref, Error: ae}
	_, err := tx.ExecContext(ctx, "INSERT INTO run_events VALUES(?,?,?,?)", e.EventID, r.ID, seq, marshal(e))
	return err
}
