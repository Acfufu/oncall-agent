// workspacectl inventories or explicitly archives legacy reports; it never resets vector collections.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"oncall-agent/internal/config"
	"oncall-agent/internal/workspace"
	"os"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "workspacectl:", err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: workspacectl inventory|dry-run|import-legacy|backup --config <explicit config> [--apply] [--output <new backup>]")
	}
	command := args[0]
	switch command {
	case "inventory", "dry-run", "import-legacy", "backup":
	default:
		return fmt.Errorf("unknown command")
	}
	f := flag.NewFlagSet("workspacectl", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	configPath := f.String("config", getenv("ONCALL_CONFIG"), "explicit config path; no business default")
	sqlitePath := f.String("sqlite", "", "explicit override for target SQLite")
	legacyPath := f.String("legacy", "", "explicit legacy snapshot override")
	output := f.String("output", "", "new backup file")
	apply := f.Bool("apply", false, "explicitly apply legacy import")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional argument")
	}
	if *configPath == "" {
		return fmt.Errorf("--config or ONCALL_CONFIG required; no business configuration is loaded by default")
	}
	cfg, err := config.LoadMCP(*configPath)
	if err != nil {
		return fmt.Errorf("explicit configuration could not be read")
	}
	if *sqlitePath == "" {
		*sqlitePath = cfg.Storage.SQLitePath
	}
	if *legacyPath == "" {
		*legacyPath = cfg.Reports.PersistPath
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	switch command {
	case "inventory", "dry-run":
		v, err := workspace.InventoryMigration(ctx, *legacyPath, *sqlitePath, workspace.QdrantHTTPBase(cfg.Qdrant.Host, cfg.Qdrant.Port), cfg.Qdrant.Collection)
		if err != nil {
			return err
		}
		return encoder.Encode(v)
	case "import-legacy":
		if !*apply {
			return fmt.Errorf("legacy import requires --apply; use dry-run first")
		}
		if *legacyPath == "" || *sqlitePath == "" {
			return fmt.Errorf("legacy and sqlite paths required")
		}
		d, err := workspace.Open(*sqlitePath)
		if err != nil {
			return err
		}
		defer d.Close()
		r, err := d.ImportLegacyReports(ctx, *legacyPath)
		if err != nil {
			return err
		}
		return encoder.Encode(r)
	case "backup":
		if *output == "" {
			return fmt.Errorf("--output must name a new backup file")
		}
		if err := workspace.BackupExistingDatabase(ctx, *sqlitePath, *output); err != nil {
			return err
		}
		return encoder.Encode(map[string]any{"status": "backed_up", "source_unchanged": true, "restore": "Stop the service and explicitly choose the backup as the SQLite path; keep the previous database for rollback."})
	}
	return nil
}
