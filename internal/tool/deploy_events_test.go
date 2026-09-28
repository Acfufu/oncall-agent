package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// stubGitHub 起 httptest stub，返回 commits/deployments 形状 JSON，并记录
// 收到的 query（断言窗口透传用）。responses 按 path 前缀分发。
func stubGitHub(t *testing.T, commits, deployments string, queries *map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	handle := func(key, body string) {
		mux.HandleFunc(key, func(w http.ResponseWriter, r *http.Request) {
			if queries != nil {
				(*queries)[key] = r.URL.RawQuery
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		})
	}
	handle("/repos/o/r/commits", commits)
	handle("/repos/o/r/deployments", deployments)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

const stubCommit = `[{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","commit":{"author":{"date":"2026-09-27T10:00:00Z"},"message":"fix: cpu throttle\n\nbody ignored"}}]`

const stubDeployment = `[{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","environment":"prod","created_at":"2026-09-27T09:00:00Z","description":"release v7","ref":"main"}]`

func TestFetchDeployEventsMergesAndOrders(t *testing.T) {
	srv := stubGitHub(t, stubCommit, stubDeployment, nil)
	src := DeploySource{Repo: "o/r", BaseURL: srv.URL}
	evs, err := FetchDeployEvents(context.Background(), src, "2026-09-26T00:00:00Z", "2026-09-28T00:00:00Z")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(evs), evs)
	}
	// 倒序：commit(10:00) 在 deployment(09:00) 前。
	if evs[0].SHA[:4] != "aaaa" || evs[1].SHA[:4] != "bbbb" {
		t.Fatalf("order wrong: %+v", evs)
	}
	// commit 的 env 为空串保形状统一；deployment 的 env 取 environment。
	if evs[0].Env != "" {
		t.Fatalf("commit env should be empty, got %q", evs[0].Env)
	}
	if evs[1].Env != "prod" {
		t.Fatalf("deployment env want prod, got %q", evs[1].Env)
	}
	// message 取首行。
	if evs[0].Message != "fix: cpu throttle" {
		t.Fatalf("commit message want first line, got %q", evs[0].Message)
	}
}

func TestFetchDeployEventsWindowPassthroughAndFilter(t *testing.T) {
	queries := map[string]string{}
	// deployment created_at 在窗外（晚于 until）→ 客户端过滤掉。
	dps := `[{"sha":"cccc","environment":"prod","created_at":"2026-09-29T00:00:00Z","description":"future","ref":"main"},` + stubDeployment[1:]
	srv := stubGitHub(t, stubCommit, dps, &queries)
	src := DeploySource{Repo: "o/r", BaseURL: srv.URL}
	evs, err := FetchDeployEvents(context.Background(), src, "2026-09-26T00:00:00Z", "2026-09-28T00:00:00Z")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("out-of-window deployment should be filtered, got %d: %+v", len(evs), evs)
	}
	for _, key := range []string{"/repos/o/r/commits", "/repos/o/r/deployments"} {
		if !strings.Contains(queries[key], "per_page=10") {
			t.Fatalf("%s missing per_page=10: %s", key, queries[key])
		}
	}
	if !strings.Contains(queries["/repos/o/r/commits"], "since=") || !strings.Contains(queries["/repos/o/r/commits"], "until=") {
		t.Fatalf("commits missing since/until: %s", queries["/repos/o/r/commits"])
	}
}

func TestFetchDeployEventsDefaultWindow24h(t *testing.T) {
	queries := map[string]string{}
	srv := stubGitHub(t, "[]", "[]", &queries)
	src := DeploySource{Repo: "o/r", BaseURL: srv.URL}
	before := time.Now().UTC()
	if _, err := FetchDeployEvents(context.Background(), src, "", ""); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	q := queries["/repos/o/r/commits"]
	var sinceVal string
	for _, kv := range strings.Split(q, "&") {
		if strings.HasPrefix(kv, "since=") {
			sinceVal, _ = url.QueryUnescape(strings.TrimPrefix(kv, "since="))
		}
	}
	if sinceVal == "" {
		t.Fatalf("default since missing: %s", q)
	}
	ts, err := time.Parse(time.RFC3339, sinceVal)
	if err != nil {
		t.Fatalf("since not RFC3339 %q: %v", sinceVal, err)
	}
	if d := before.Sub(ts); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("default window want ~24h back, got %v", d)
	}
}

func TestFetchDeployEventsTruncates10(t *testing.T) {
	var commits []map[string]any
	for i := 0; i < 12; i++ {
		commits = append(commits, map[string]any{
			"sha":    strings.Repeat(string(rune('a'+i%26)), 40),
			"commit": map[string]any{"author": map[string]any{"date": "2026-09-27T10:00:00Z"}, "message": "m"},
		})
	}
	raw, _ := json.Marshal(commits)
	srv := stubGitHub(t, string(raw), stubDeployment, nil)
	src := DeploySource{Repo: "o/r", BaseURL: srv.URL}
	evs, err := FetchDeployEvents(context.Background(), src, "2026-09-26T00:00:00Z", "2026-09-28T00:00:00Z")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	commitsN := 0
	for _, e := range evs {
		if e.Env == "" {
			commitsN++
		}
	}
	if commitsN != 10 {
		t.Fatalf("commits want truncated to 10, got %d", commitsN)
	}
}

func TestFetchDeployEventsBadTime(t *testing.T) {
	src := DeploySource{Repo: "o/r", BaseURL: "http://127.0.0.1:1"}
	if _, err := FetchDeployEvents(context.Background(), src, "notatime", ""); err == nil {
		t.Fatal("bad since should error")
	}
	if _, err := FetchDeployEvents(context.Background(), src, "", "alsobad"); err == nil {
		t.Fatal("bad until should error")
	}
}

func TestFetchDeployEventsConnRefused(t *testing.T) {
	src := DeploySource{Repo: "o/r", BaseURL: "http://127.0.0.1:1"}
	if _, err := FetchDeployEvents(context.Background(), src, "", ""); err == nil {
		t.Fatal("conn refused should error")
	}
}

func TestFetchDeployEventsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	src := DeploySource{Repo: "o/r", BaseURL: srv.URL}
	_, err := FetchDeployEvents(context.Background(), src, "", "")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want 404 error text, got %v", err)
	}
}

func TestDeployEventsToolTextShape(t *testing.T) {
	srv := stubGitHub(t, stubCommit, stubDeployment, nil)
	src := DeploySource{Repo: "o/r", BaseURL: srv.URL}
	// 显式窗口覆盖 stub 日期：缺省窗 [now-24h,now] 会随时间流逝滤掉写死的
	// stub 事件（时间炸弹，2026-09-29 审查 F01）。
	out, evs, err := src.DeployEventsWithContext(context.Background(),
		`{"since":"2026-09-26T00:00:00Z","until":"2026-09-28T00:00:00Z"}`)
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	if !strings.HasPrefix(out, "deploy_events repo=o/r window=") {
		t.Fatalf("header missing: %q", out)
	}
	if !strings.Contains(out, "fix: cpu throttle") {
		t.Fatalf("event line missing: %q", out)
	}
}

func TestDeployEventsToolBadArgs(t *testing.T) {
	src := DeploySource{Repo: "o/r", BaseURL: "http://127.0.0.1:1"}
	if _, _, err := src.DeployEventsWithContext(context.Background(), `{"since":"bogus"}`); err == nil {
		t.Fatal("bad since arg should error")
	}
}
