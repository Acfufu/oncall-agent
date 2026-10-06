package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"oncall-agent/internal/config"
	"oncall-agent/internal/workspace"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCLIRequiresExplicitConfigAndApply(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"inventory"}, &out, func(string) string { return "" }); err == nil {
		t.Fatal("inventory defaulted to business config")
	}
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.SQLitePath = filepath.Join(dir, "absent.sqlite")
	cfg.Reports.PersistPath = filepath.Join(dir, "source.json")
	path := filepath.Join(dir, "config.json")
	b, _ := json.Marshal(cfg)
	os.WriteFile(path, b, 0600)
	if err := run([]string{"import-legacy", "--config", path}, &out, func(string) string { return "" }); err == nil {
		t.Fatal("import allowed without apply")
	}
	if _, err := os.Stat(cfg.Storage.SQLitePath); !os.IsNotExist(err) {
		t.Fatal("non-apply import touched target database")
	}
}
func TestCLIFixtureDryRunImportBackupAndInventory(t *testing.T) {
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Write([]byte(`{"result":{"points_count":0,"config":{"params":{"vectors":{"size":64}}}}}`))
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Qdrant.Host = u.Hostname()
	cfg.Qdrant.Port, _ = strconv.Atoi(u.Port())
	cfg.Qdrant.Collection = "fixture-owned"
	cfg.Storage.SQLitePath = filepath.Join(dir, "owned.sqlite")
	cfg.Reports.PersistPath = filepath.Join(dir, "legacy.json")
	configPath := filepath.Join(dir, "config.json")
	b, _ := json.Marshal(cfg)
	os.WriteFile(configPath, b, 0600)
	source := `[{"id":"old-id","status":"done","received_at":"2026-10-01T12:00:00Z","alerts":[{"name":"CPU"}],"diagnosis":"raw legacy","score":0}]`
	os.WriteFile(cfg.Reports.PersistPath, []byte(source), 0600)
	env := func(k string) string {
		if k == "ONCALL_CONFIG" {
			return configPath
		}
		return ""
	}
	var out bytes.Buffer
	if err := run([]string{"dry-run"}, &out, env); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"state": "not_found"`)) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(cfg.Storage.SQLitePath); !os.IsNotExist(err) {
		t.Fatal("dry-run created database")
	}
	out.Reset()
	if err := run([]string{"import-legacy", "--apply"}, &out, env); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"imported": 1`)) {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := run([]string{"import-legacy", "--apply"}, &out, env); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"duplicates": 1`)) {
		t.Fatal(out.String())
	}
	out.Reset()
	backup := filepath.Join(dir, "backup.sqlite")
	if err := run([]string{"backup", "--output", backup}, &out, env); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"backup", "--output", backup}, &out, env); err == nil {
		t.Fatal("backup overwritten")
	}
	out.Reset()
	if err := run([]string{"inventory"}, &out, env); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"run_count": 1`)) {
		t.Fatal(out.String())
	}
	for _, m := range methods {
		if m != "GET" {
			t.Fatal("mutating upstream command", methods)
		}
	}
	original, _ := os.ReadFile(cfg.Reports.PersistPath)
	if string(original) != source {
		t.Fatal("CLI mutated source")
	}
	restored, err := workspace.Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	r, err := restored.Run(context.Background(), "old-id")
	if err != nil || r.JudgeScore != nil || r.Status != "succeeded" {
		t.Fatalf("backup restored wrong %+v %v", r, err)
	}
}
