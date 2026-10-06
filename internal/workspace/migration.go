package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type MigrationInventory struct {
	Legacy struct {
		State       string `json:"state"`
		ReportCount *int   `json:"report_count"`
		SHA256      string `json:"sha256,omitempty"`
	} `json:"legacy"`
	SQLite struct {
		State         string `json:"state"`
		SchemaVersion *int   `json:"schema_version"`
		DocumentCount *int   `json:"document_count"`
		RunCount      *int   `json:"run_count"`
	} `json:"sqlite"`
	Qdrant struct {
		State      string `json:"state"`
		Dimension  *int   `json:"dimension"`
		PointCount *int   `json:"point_count"`
	} `json:"qdrant"`
	Notes []string `json:"notes"`
}
type LegacyImportResult struct {
	Imported         int    `json:"imported"`
	Duplicates       int    `json:"duplicates"`
	SourceSHA256     string `json:"source_sha256"`
	LegacyIncomplete bool   `json:"legacy_incomplete"`
}
type legacySnapshot struct {
	raw                             json.RawMessage
	id, status, received, diagnosis string
	score                           int
}

func readLegacySnapshot(path string) ([]legacySnapshot, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil {
		return nil, "", err
	}
	if len(raw) > 32<<20 {
		return nil, "", fmt.Errorf("legacy snapshot exceeds 32MiB")
	}
	var rows []json.RawMessage
	if err = json.Unmarshal(raw, &rows); err != nil {
		var envelope struct {
			Reports []json.RawMessage `json:"reports"`
		}
		if err = json.Unmarshal(raw, &envelope); err != nil || envelope.Reports == nil {
			return nil, "", fmt.Errorf("invalid legacy snapshot")
		}
		rows = envelope.Reports
	}
	if rows == nil {
		return nil, "", fmt.Errorf("legacy snapshot must be a report array")
	}
	out := make([]legacySnapshot, 0, len(rows))
	for _, row := range rows {
		var p struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			ReceivedAt string `json:"received_at"`
			Diagnosis  string `json:"diagnosis"`
			Score      int    `json:"score"`
		}
		if err = json.Unmarshal(row, &p); err != nil {
			return nil, "", fmt.Errorf("invalid legacy report")
		}
		if p.ID == "" || len(p.ID) > 200 {
			return nil, "", fmt.Errorf("legacy report ID required")
		}
		if p.Score < 0 || p.Score > 5 {
			return nil, "", fmt.Errorf("legacy judge score invalid")
		}
		switch p.Status {
		case "done", "failed", "queued", "running":
		default:
			return nil, "", fmt.Errorf("unsupported legacy report status")
		}
		if _, err = time.Parse(time.RFC3339Nano, p.ReceivedAt); err != nil {
			return nil, "", fmt.Errorf("legacy received_at required and must be RFC3339")
		}
		out = append(out, legacySnapshot{raw: row, id: p.ID, status: p.Status, received: p.ReceivedAt, diagnosis: p.Diagnosis, score: p.Score})
	}
	return out, hash(string(raw)), nil
}

// InventoryMigration performs read-only SQL and HTTP queries. It never initializes or migrates a database.
func InventoryMigration(ctx context.Context, legacyPath, sqlitePath, qdrantBase, collection string) (MigrationInventory, error) {
	var v MigrationInventory
	v.Notes = []string{"旧引用仅保留历史原文；缺少原 Markdown 时无法恢复合格文档版本，请提供原文件后经版本接口重新索引。", "本命令不写业务记录、不重建索引、不删除 Qdrant 集合。"}
	v.Legacy.State = "not_configured"
	v.SQLite.State = "not_configured"
	v.Qdrant.State = "not_configured"
	if legacyPath != "" {
		rows, sum, err := readLegacySnapshot(legacyPath)
		if err != nil {
			if os.IsNotExist(err) {
				v.Legacy.State = "not_found"
			} else {
				return v, fmt.Errorf("legacy snapshot invalid: %w", err)
			}
		} else {
			n := len(rows)
			v.Legacy.State = "fresh"
			v.Legacy.ReportCount = &n
			v.Legacy.SHA256 = sum
		}
	}
	if sqlitePath != "" {
		if _, err := os.Stat(sqlitePath); err != nil {
			if !os.IsNotExist(err) {
				return v, err
			}
			v.SQLite.State = "not_found"
		} else {
			d, err := openSQLiteReadOnly(sqlitePath)
			if err != nil {
				return v, err
			}
			defer d.Close()
			var version sql.NullInt64
			if err = d.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
				return v, fmt.Errorf("sqlite schema unavailable: %w", err)
			}
			if version.Valid {
				x := int(version.Int64)
				v.SQLite.SchemaVersion = &x
			}
			var docs, runs int
			if err = d.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents").Scan(&docs); err != nil {
				return v, err
			}
			if err = d.QueryRowContext(ctx, "SELECT COUNT(*) FROM diagnosis_runs").Scan(&runs); err != nil {
				return v, err
			}
			v.SQLite.DocumentCount = &docs
			v.SQLite.RunCount = &runs
			v.SQLite.State = "fresh"
		}
	}
	if qdrantBase != "" && collection != "" {
		u, err := url.Parse(qdrantBase)
		if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return v, fmt.Errorf("invalid Qdrant base URL")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(qdrantBase, "/")+"/collections/"+url.PathEscape(collection), nil)
		if err != nil {
			return v, err
		}
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			v.Qdrant.State = "unavailable"
			return v, nil
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 {
			v.Qdrant.State = "not_found"
			return v, nil
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			v.Qdrant.State = "unavailable"
			return v, nil
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
		if err != nil || len(b) > 1<<20 {
			v.Qdrant.State = "unavailable"
			return v, nil
		}
		var info struct {
			Result *struct {
				PointsCount *int `json:"points_count"`
				Config      struct {
					Params struct {
						Vectors struct {
							Size *int `json:"size"`
						} `json:"vectors"`
					} `json:"params"`
				} `json:"config"`
			} `json:"result"`
		}
		if json.Unmarshal(b, &info) != nil || info.Result == nil {
			v.Qdrant.State = "unavailable"
			return v, nil
		}
		v.Qdrant.State = "fresh"
		v.Qdrant.Dimension = info.Result.Config.Params.Vectors.Size
		v.Qdrant.PointCount = info.Result.PointsCount
	}
	return v, ctx.Err()
}
func openSQLiteReadOnly(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: absolute}
	q := url.Values{"mode": []string{"ro"}, "_busy_timeout": []string{"5000"}}
	u.RawQuery = q.Encode()
	d, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	return d, nil
}

// BackupExistingDatabase uses VACUUM INTO with a read-only source. SQLite refuses an existing target; no backup is overwritten.
func BackupExistingDatabase(ctx context.Context, source, target string) error {
	if source == "" || target == "" {
		return fmt.Errorf("source and backup path required")
	}
	if _, err := os.Stat(source); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("backup already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	d, err := openSQLiteReadOnly(source)
	if err != nil {
		return err
	}
	defer d.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".workspace-backup-*")
	if err != nil {
		return err
	}
	temporary := tmp.Name()
	if err = tmp.Close(); err != nil {
		os.Remove(temporary)
		return err
	}
	defer os.Remove(temporary)
	if err = (&DB{SQL: d}).Backup(ctx, temporary); err != nil {
		return err
	}
	// Atomic no-replace publication also protects a target created after the initial existence check.
	return os.Link(temporary, target)
}

// ImportLegacyReports archives original reports in one idempotent transaction. It never creates runnable tasks, inferred incidents, citations or document versions.
func (d *DB) ImportLegacyReports(ctx context.Context, path string) (LegacyImportResult, error) {
	rows, sum, err := readLegacySnapshot(path)
	result := LegacyImportResult{SourceSHA256: sum, LegacyIncomplete: true}
	if err != nil {
		return result, err
	}
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	for _, old := range rows {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		evidenceID := stableID("legacy_", old.id)
		var existing string
		err = tx.QueryRowContext(ctx, "SELECT data FROM evidence WHERE id=?", evidenceID).Scan(&existing)
		if err == nil {
			var ev Evidence
			if json.Unmarshal([]byte(existing), &ev) != nil {
				return result, fmt.Errorf("invalid existing legacy archive")
			}
			if ev.Metadata["legacy_report_sha256"] != hash(string(old.raw)) {
				return result, ErrConflict
			}
			result.Duplicates++
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		var exists int
		err = tx.QueryRowContext(ctx, "SELECT 1 FROM diagnosis_runs WHERE id=?", old.id).Scan(&exists)
		if err == nil {
			return result, ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		status := "failed"
		if old.status == "done" {
			status = "succeeded"
		}
		received, _ := time.Parse(time.RFC3339Nano, old.received)
		r := Run{ID: old.id, IncidentIDs: []string{}, Status: status, DispatchState: "none", EvidenceStatus: "not_evaluated", NotificationStatus: "disabled", ReceivedAt: received.UTC().Format(time.RFC3339Nano), Revision: 1, ReportText: ptr(old.diagnosis), CitationIDs: []string{}, Error: &APIError{Code: "legacy_incomplete", Message: "旧报告来源与版本不完整，仅保留历史原文，未核验", Retryable: false}}
		if old.score > 0 {
			x := old.score
			r.JudgeScore = &x
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO diagnosis_runs(id,business_key,status,received_at,data) VALUES(?,?,?,?,?)", r.ID, "legacy:"+hash(old.id), r.Status, r.ReceivedAt, marshal(r)); err != nil {
			return result, err
		}
		ev := Evidence{ID: evidenceID, Kind: "report", SourceKind: "system", SourceRef: "legacy:" + old.id, RecordedAt: stamp(), Snippet: ptr(old.diagnosis), Metadata: map[string]any{"run_id": old.id, "legacy_incomplete": true, "legacy_status": old.status, "legacy_json": string(old.raw), "legacy_report_sha256": hash(string(old.raw)), "source_sha256": sum, "verification": "unverified", "original_alerts_preserved": true}}
		if _, err = tx.ExecContext(ctx, "INSERT INTO evidence(id,run_id,incident_id,data) VALUES(?,?,NULL,?)", ev.ID, r.ID, marshal(ev)); err != nil {
			return result, err
		}
		result.Imported++
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

// LegacyReportArchive returns only the original JSON copy; it is never a source of qualified citations.
func (d *DB) LegacyReportArchive(ctx context.Context, id string) (json.RawMessage, error) {
	ev, err := d.Evidence(ctx, stableID("legacy_", id))
	if err != nil {
		return nil, err
	}
	raw, ok := ev.Metadata["legacy_json"].(string)
	if !ok || !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("legacy archive unavailable")
	}
	return json.RawMessage(raw), nil
}

// QdrantHTTPBase converts the historical gRPC default port to the actual read-only HTTP port.
func QdrantHTTPBase(host string, port int) string {
	if port == 0 || port == 6334 {
		port = 6333
	}
	return "http://" + host + ":" + strconv.Itoa(port)
}
