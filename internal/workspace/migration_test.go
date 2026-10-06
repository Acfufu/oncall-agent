package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const migrationLegacyFixture = `[{"id":"original-report","status":"done","received_at":"2026-10-01T12:30:00Z","alerts":[{"name":"CPUHigh","labels":{"service":"api"},"startsAt":"2026-10-01T12:00:00Z"}],"diagnosis":"historical prose","citations":[{"doc":"missing-original.md","snippet":"historical unverified quote"}],"score":0,"deploy_events":[{"sha":"abc"}]}]`

func migrationFixture(t *testing.T) (*DB, string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "reports.json")
	if err := os.WriteFile(source, []byte(migrationLegacyFixture), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "facts.sqlite")
	d, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	return d, source, path
}
func TestLegacyImportIdempotencyRestartAndSourcePreservation(t *testing.T) {
	d, source, path := migrationFixture(t)
	ctx := context.Background()
	sum := hash(migrationLegacyFixture)
	result, err := d.ImportLegacyReports(ctx, source)
	if err != nil || result.Imported != 1 || result.SourceSHA256 != sum {
		t.Fatalf("import %+v %v", result, err)
	}
	r, err := d.Run(ctx, "original-report")
	if err != nil || r.Status != "succeeded" || r.JudgeScore != nil || r.EvidenceStatus != "not_evaluated" || len(r.CitationIDs) != 0 || len(r.IncidentIDs) != 0 || r.Error.Code != "legacy_incomplete" {
		t.Fatalf("invented legacy authority %+v %v", r, err)
	}
	raw, err := d.LegacyReportArchive(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	var original, archived any
	json.Unmarshal([]byte(migrationLegacyFixture), &original)
	json.Unmarshal(raw, &archived)
	if !bytes.Equal(raw, []byte(migrationLegacyFixture[1:len(migrationLegacyFixture)-1])) {
		t.Fatal("original report JSON changed")
	}
	if !bytes.Contains(raw, []byte(`"alerts"`)) || !bytes.Contains(raw, []byte("historical unverified quote")) {
		t.Fatal("original raw payload lost")
	}
	for _, table := range []string{"incidents", "run_events", "outbox", "documents", "document_versions", "report_citations"} {
		var count int
		if err = d.SQL.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("invented %s facts count=%d %v", table, count, err)
		}
	}
	d.Close()
	restarted, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	result, err = restarted.ImportLegacyReports(ctx, source)
	if err != nil || result.Imported != 0 || result.Duplicates != 1 {
		t.Fatalf("reimport %+v %v", result, err)
	}
	b, e := os.ReadFile(source)
	if e != nil || hash(string(b)) != sum {
		t.Fatal("legacy source changed")
	}
	if _, err = restarted.Run(ctx, r.ID); err != nil {
		t.Fatal("import lost on restart")
	}
}
func TestLegacyImportTransactionalConflictRollback(t *testing.T) {
	d, source, _ := migrationFixture(t)
	ctx := context.Background()
	if _, err := d.ImportLegacyReports(ctx, source); err != nil {
		t.Fatal(err)
	}
	raw := `[{"id":"new-report","status":"done","received_at":"2026-10-01T12:30:00Z","score":0},{"id":"original-report","status":"failed","received_at":"2026-10-01T12:30:00Z","score":0}]`
	os.WriteFile(source, []byte(raw), 0600)
	if _, err := d.ImportLegacyReports(ctx, source); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed original report not rejected: %v", err)
	}
	if _, err := d.Run(ctx, "new-report"); err == nil {
		t.Fatal("partial import survived transaction rollback")
	}
	var count int
	d.SQL.QueryRow("SELECT COUNT(*) FROM diagnosis_runs").Scan(&count)
	if count != 1 {
		t.Fatalf("rollback count=%d", count)
	}
}
func TestInventoryReadOnlyAndAbsentDatabase(t *testing.T) {
	d, source, path := migrationFixture(t)
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Write([]byte(`{"result":{"points_count":4,"config":{"params":{"vectors":{"size":768}}}}}`))
	}))
	defer server.Close()
	v, err := InventoryMigration(context.Background(), source, path, server.URL, "fixture")
	if err != nil || v.Legacy.ReportCount == nil || *v.Legacy.ReportCount != 1 || v.SQLite.RunCount == nil || *v.SQLite.RunCount != 0 || v.Qdrant.Dimension == nil || *v.Qdrant.Dimension != 768 {
		t.Fatalf("inventory %+v %v", v, err)
	}
	if len(methods) != 1 || methods[0] != "GET" {
		t.Fatalf("mutating collection inventory: %+v", methods)
	}
	var runs int
	d.SQL.QueryRow("SELECT COUNT(*) FROM diagnosis_runs").Scan(&runs)
	if runs != 0 {
		t.Fatal("inventory changed facts")
	}
	missing := filepath.Join(t.TempDir(), "not-created.sqlite")
	v, err = InventoryMigration(context.Background(), "", missing, "", "")
	if err != nil || v.SQLite.State != "not_found" {
		t.Fatalf("absent %+v %v", v, err)
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("inventory initialized absent database")
	}
}
func TestLegacyBackupRestoreAndRefusesOverwrite(t *testing.T) {
	d, source, path := migrationFixture(t)
	ctx := context.Background()
	if _, err := d.ImportLegacyReports(ctx, source); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.sqlite")
	if err := BackupExistingDatabase(ctx, path, backup); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	sum := hash(string(b))
	if err := BackupExistingDatabase(ctx, path, backup); err == nil {
		t.Fatal("existing backup overwrite allowed")
	}
	b, _ = os.ReadFile(backup)
	if hash(string(b)) != sum {
		t.Fatal("existing backup changed")
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	r, err := restored.Run(ctx, "original-report")
	if err != nil || r.Status != "succeeded" {
		t.Fatalf("restore lost original %+v %v", r, err)
	}
	if _, err := d.Run(ctx, r.ID); err != nil {
		t.Fatal("backup modified source")
	}
}
func TestBackupSelectionRollbackRestoresPriorFacts(t *testing.T) {
	d, source, path := migrationFixture(t)
	ctx := context.Background()
	if _, err := d.ImportLegacyReports(ctx, source); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "prior.sqlite")
	if err := BackupExistingDatabase(ctx, path, backup); err != nil {
		t.Fatal(err)
	}
	later := filepath.Join(t.TempDir(), "later.json")
	os.WriteFile(later, []byte(`[{"id":"later-report","status":"failed","received_at":"2026-10-02T12:00:00Z","diagnosis":"later","score":2}]`), 0600)
	if _, err := d.ImportLegacyReports(ctx, later); err != nil {
		t.Fatal(err)
	}
	rollback, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback.Close()
	if _, err := rollback.Run(ctx, "original-report"); err != nil {
		t.Fatal("rollback lost original history")
	}
	if _, err := rollback.Run(ctx, "later-report"); err == nil {
		t.Fatal("rollback retained post-backup fact")
	}
	if _, err := d.Run(ctx, "later-report"); err != nil {
		t.Fatal("rollback selection destroyed newer source facts")
	}
}
func TestInventoryConnectionCannotWriteSource(t *testing.T) {
	d, _, path := migrationFixture(t)
	ro, err := openSQLiteReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err = ro.Exec("INSERT INTO schema_migrations VALUES(9000)"); err == nil {
		t.Fatal("read-only source connection accepted write")
	}
	var count int
	if err = d.SQL.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=9000").Scan(&count); err != nil || count != 0 {
		t.Fatalf("source modified count=%d err=%v", count, err)
	}
}
