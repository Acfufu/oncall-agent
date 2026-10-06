package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func topologyFixture(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	v := map[string]any{"version": "test-v1", "source_ref": "controlled:test-config", "valid_from": "2026-01-01T00:00:00Z", "nodes": []any{map[string]any{"environment": "test", "namespace": "default", "service": "api", "source_ref": "controlled:api"}, map[string]any{"environment": "test", "namespace": "default", "service": "db", "source_ref": "controlled:db"}}, "edges": []any{map[string]any{"source": "test/default/api", "target": "test/default/db", "kind": "declared", "source_ref": "controlled:api-depends-db"}}}
	if mutate != nil {
		mutate(v)
	}
	b, _ := json.Marshal(v)
	p := filepath.Join(t.TempDir(), "topology.json")
	os.WriteFile(p, b, 0600)
	return p
}
func TestM2TopologyBoundedCycleAndPotentialImpact(t *testing.T) {
	p := topologyFixture(t, func(v map[string]any) {
		v["edges"] = append(v["edges"].([]any), map[string]any{"source": "test/default/db", "target": "test/default/api", "kind": "declared", "source_ref": "controlled:cycle"})
	})
	top, err := LoadTopology(p)
	if err != nil {
		t.Fatal(err)
	}
	g, err := top.ForService(context.Background(), "test", "db")
	if err != nil || len(g.Nodes) != 2 || len(g.Edges) != 2 || len(g.PotentialImpact) != 1 {
		t.Fatalf("cycle traversal %+v %v", g, err)
	}
	if g.PotentialImpact[0].ServiceID != "test/default/api" || g.PotentialImpact[0].Basis != "inferred" || g.PotentialImpact[0].Label != "潜在影响" {
		t.Fatalf("impact overclaimed: %+v", g)
	}
	if g.Version != "test-v1" || g.ContentSHA256 == "" || g.SourceRef == "" {
		t.Fatal("missing config provenance")
	}
	for _, e := range g.Edges {
		if e.SourceRef == "" || e.Kind != "declared" {
			t.Fatalf("unsourced edge %+v", e)
		}
	}
	if _, err = top.ForService(context.Background(), "test", "unknown"); err == nil {
		t.Fatal("unknown service accepted")
	}
}
func TestM2TopologyRejectsInvalidDeclarations(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){"cross_environment": func(v map[string]any) {
		v["nodes"] = append(v["nodes"].([]any), map[string]any{"environment": "prod", "namespace": "default", "service": "db", "source_ref": "controlled:prod"})
		v["edges"].([]any)[0].(map[string]any)["target"] = "prod/default/db"
	}, "unknown_service": func(v map[string]any) { v["edges"].([]any)[0].(map[string]any)["target"] = "test/default/missing" }, "missing_source": func(v map[string]any) { v["edges"].([]any)[0].(map[string]any)["source_ref"] = "" }, "ambiguous_namespace": func(v map[string]any) {
		v["nodes"] = append(v["nodes"].([]any), map[string]any{"environment": "test", "namespace": "other", "service": "api", "source_ref": "controlled:other"})
	}, "invalid_time": func(v map[string]any) { v["valid_to"] = "2025-01-01T00:00:00Z" }} {
		t.Run(name, func(t *testing.T) {
			top, err := LoadTopology(topologyFixture(t, mutate))
			if name == "ambiguous_namespace" {
				if err == nil {
					_, err = top.ForService(context.Background(), "test", "api")
				}
			}
			if err == nil {
				t.Fatal("invalid declaration accepted")
			}
		})
	}
}

func TestM2TopologyVersionsStayImmutableAndRespectValidity(t *testing.T) {
	s, _ := fixtureService(t)
	path := topologyFixture(t, nil)
	first, err := LoadTopology(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PersistTopology(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var cfg map[string]any
	json.Unmarshal(raw, &cfg)
	cfg["version"] = "test-v2"
	cfg["edges"] = []any{}
	nextRaw, _ := json.Marshal(cfg)
	os.WriteFile(path, nextRaw, 0600)
	second, err := LoadTopology(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PersistTopology(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.SQL.QueryRow("SELECT count(*) FROM topology_versions").Scan(&n)
	if n != 2 {
		t.Fatalf("versions overwritten: %d", n)
	}
	old, _ := first.ForService(context.Background(), "test", "db")
	if len(old.Edges) != 1 || old.ContentSHA256 == second.snapshot.ContentSHA256 {
		t.Fatal("old loaded snapshot changed")
	}
	future, err := LoadTopology(topologyFixture(t, func(v map[string]any) { v["valid_from"] = "2099-01-01T00:00:00Z" }))
	if err != nil {
		t.Fatal(err)
	}
	g, err := future.ForService(context.Background(), "test", "db")
	if err != nil || len(g.Edges) != 0 || len(g.PotentialImpact) != 0 || g.ValidityState != "inactive" {
		t.Fatalf("inactive declaration promoted to impact: %+v %v", g, err)
	}
}
