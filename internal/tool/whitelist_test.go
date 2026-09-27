package tool

import (
	"context"
	"strings"
	"testing"
)

// ADR-0009 门控语义：repo 空=白名单缩回三只读、exec 拒绝第四只；repo 设=四只放行。
func TestWhitelistGatingOff(t *testing.T) {
	if got := AllowedFor(""); len(got) != 3 {
		t.Fatalf("repo empty want 3 tools, got %v", got)
	}
	for _, name := range AllowedFor("") {
		if name == "deploy_events" {
			t.Fatal("repo empty should not contain deploy_events")
		}
	}
	if defs := DefinitionsFor(""); len(defs) != 3 {
		t.Fatalf("repo empty want 3 definitions, got %d", len(defs))
	}
	if IsAllowed("", "deploy_events") {
		t.Fatal("repo empty must deny deploy_events")
	}
	d := NewDeps(nil, "")
	_, _, err := d.ExecWithContext(context.Background(), "deploy_events", "{}")
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("exec should deny deploy_events when repo empty, got %v", err)
	}
}

func TestWhitelistGatingOn(t *testing.T) {
	got := AllowedFor("o/r")
	if len(got) != 4 || got[3] != "deploy_events" {
		t.Fatalf("repo set want 4 tools with deploy_events last, got %v", got)
	}
	defs := DefinitionsFor("o/r")
	if len(defs) != 4 || defs[3].Function.Name != "deploy_events" {
		t.Fatalf("repo set want 4 definitions ending deploy_events, got %d", len(defs))
	}
	if !IsAllowed("o/r", "deploy_events") {
		t.Fatal("repo set must allow deploy_events")
	}
	// 三只读定义不因门控变形。
	for i, want := range []string{"time_now", "rag_search", "prometheus_query"} {
		if defs[i].Function.Name != want {
			t.Fatalf("defs[%d] want %s got %s", i, want, defs[i].Function.Name)
		}
	}
}

func TestExecDispatchDeployEvents(t *testing.T) {
	srv := stubGitHub(t, stubCommit, stubDeployment, nil)
	d := NewDeps(nil, "").WithDeploy(DeploySource{Repo: "o/r", BaseURL: srv.URL})
	out, _, err := d.ExecWithContext(context.Background(), "deploy_events", "{}")
	if err != nil {
		t.Fatalf("exec dispatch: %v", err)
	}
	if !strings.Contains(out, "deploy_events repo=o/r") {
		t.Fatalf("dispatch output wrong: %q", out)
	}
}
