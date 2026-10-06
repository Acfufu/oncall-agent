package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"oncall-agent/internal/config"
	"oncall-agent/internal/tool"
	"oncall-agent/internal/workspace"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func m2HTTP(t *testing.T, upstream http.HandlerFunc) (*WorkspaceHTTP, *httptest.Server, string) {
	t.Helper()
	e, h := newWorkspaceHTTPTest(t)
	p := httptest.NewServer(upstream)
	t.Cleanup(p.Close)
	h.Prom = tool.NewPromClient(p.URL)
	_, source, _, _ := runtime.Caller(0)
	cfg := config.Default()
	cfg.Metrics.TemplatesFile = filepath.Join(filepath.Dir(source), "../../config/metric_templates.json")
	if err := h.RegisterM2(e, cfg); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(e)
	t.Cleanup(s.Close)
	r, _, err := h.Service.DB.Admit(context.Background(), []workspace.Observation{{Name: "CPU", Status: "firing", StartsAt: "2026-10-06T00:00:00Z", Labels: map[string]string{"alertname": "CPU", "environment": "test", "service": "api"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	return h, s, r.IncidentIDs[0]
}
func m2GET(t *testing.T, s *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	r, err := s.Client().Get(s.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var v map[string]any
	if err = json.NewDecoder(r.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return r.StatusCode, v
}
func TestM2MetricsGapNonFiniteAndTemplateScope(t *testing.T) {
	_, s, id := m2HTTP(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/query_range" || !strings.Contains(q.Get("query"), `environment="test"`) || !strings.Contains(q.Get("query"), `service="api"`) || q.Get("timeout") != "10s" {
			t.Errorf("unbounded query %s", r.URL)
		}
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"service":"api"},"values":[[1791244800,"1"],[1791244830,"NaN"],[1791244845,"+Inf"],[1791244860,"-Inf"],[1791244875,"2"]]}]}}`))
	})
	status, v := m2GET(t, s, "/api/v1/incidents/"+id+"/metrics?metric_id=error_rate&start=2026-10-06T00:00:00Z&end=2026-10-06T00:01:15Z&step=15")
	if status != 200 {
		t.Fatalf("%d %+v", status, v)
	}
	d := v["data"].(map[string]any)
	values := d["series"].([]any)[0].(map[string]any)["values"].([]any)
	if len(values) != 6 {
		t.Fatalf("missing grid %v", values)
	}
	for i := 1; i <= 4; i++ {
		if values[i].([]any)[1] != nil {
			t.Fatalf("missing/nonfinite coerced to zero: %+v", values)
		}
	}
	if d["historical_evidence"] != false || d["source"] == "" || d["unit"] != "ratio" {
		t.Fatalf("missing provenance %+v", d)
	}
}
func TestM2MetricsRejectsBudgetsAndArbitraryInputs(t *testing.T) {
	_, s, id := m2HTTP(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid query reached upstream") })
	base := "/api/v1/incidents/" + id + "/metrics?metric_id=error_rate&start=2026-10-06T00:00:00Z&end=2026-10-06T01:00:00Z&step=60"
	for _, path := range []string{base + "&url=http://evil", base + "&query=up", strings.Replace(base, "error_rate", "up", 1), strings.Replace(base, "step=60", "step=1", 1), strings.Replace(base, "2026-10-06T01:00:00Z", "2026-10-08T01:00:00Z", 1), strings.Replace(strings.Replace(base, "2026-10-06T01:00:00Z", "2026-10-07T00:00:00Z", 1), "step=60", "step=15", 1)} {
		status, v := m2GET(t, s, path)
		if status != 400 {
			t.Fatalf("%d %+v", status, v)
		}
	}
}
func TestM2MetricsUpstreamLimitsAndFailureTypes(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{{"histogram", `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"histograms":[[1791244800,{}]]}]}}`, "unsupported_result_type", 200}, {"malformed", `{`, "source_unavailable", 200}, {"http_failure", `{}`, "source_unavailable", 500}, {"empty", `{"status":"success","data":{"resultType":"matrix","result":[]}}`, "", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			_, s, id := m2HTTP(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) })
			status, v := m2GET(t, s, "/api/v1/incidents/"+id+"/metrics?metric_id=request_rate&start=2026-10-06T00:00:00Z&end=2026-10-06T01:00:00Z&step=60")
			if tc.code != "" {
				if status < 400 || v["code"] != tc.code {
					t.Fatalf("%d %+v", status, v)
				}
			} else if status != 200 || len(v["data"].(map[string]any)["series"].([]any)) != 0 {
				t.Fatalf("empty failed %d %+v", status, v)
			}
		})
	}
	for _, kind := range []string{"series", "points", "body"} {
		t.Run(kind, func(t *testing.T) {
			_, s, id := m2HTTP(t, func(w http.ResponseWriter, r *http.Request) {
				if kind == "body" {
					w.Write([]byte(strings.Repeat("x", 3<<20)))
					return
				}
				items := []any{}
				values := []any{}
				n := 1
				if kind == "series" {
					n = 21
				}
				if kind == "points" {
					for range 2001 {
						values = append(values, []any{1791244800, "1"})
					}
				}
				for range n {
					items = append(items, map[string]any{"metric": map[string]string{}, "values": values})
				}
				json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": items}})
			})
			status, _ := m2GET(t, s, "/api/v1/incidents/"+id+"/metrics?metric_id=request_rate&start=2026-10-06T00:00:00Z&end=2026-10-06T01:00:00Z&step=60")
			if status < 400 {
				t.Fatal("upstream budget accepted")
			}
		})
	}
}

func TestM2MetricCancellationAndTimeout(t *testing.T) {
	h, s, id := m2HTTP(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	h.Prom.Client = &http.Client{Timeout: 20 * time.Millisecond}
	status, v := m2GET(t, s, "/api/v1/incidents/"+id+"/metrics?metric_id=latency_p99&start=2026-10-06T00:00:00Z&end=2026-10-06T01:00:00Z&step=60")
	if status != 504 || v["code"] != "timeout" {
		t.Fatalf("timeout %d %+v", status, v)
	}
}
func TestM2ConfigurationDisabledAndTopologyHTTP(t *testing.T) {
	e, h := newWorkspaceHTTPTest(t)
	cfg := config.Default()
	cfg.Metrics.TemplatesFile = ""
	if err := h.RegisterM2(e, cfg); err != nil {
		t.Fatal(err)
	}
	r, _, err := h.Service.DB.Admit(context.Background(), []workspace.Observation{{Name: "CPU", Status: "firing", StartsAt: "2026-10-06T00:00:00Z", Labels: map[string]string{"environment": "test", "service": "api", "alertname": "CPU"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(e)
	defer s.Close()
	for _, path := range []string{"/topology", "/metrics?metric_id=error_rate&start=2026-10-06T00:00:00Z&end=2026-10-06T01:00:00Z&step=60"} {
		status, v := m2GET(t, s, "/api/v1/incidents/"+r.IncidentIDs[0]+path)
		if status != 200 || v["data"].(map[string]any)["capability"] != "not_configured" {
			t.Fatalf("disabled config %d %+v", status, v)
		}
	}
}

func TestM2TopologyReferencesResolveToImmutableEvidence(t *testing.T) {
	e, h := newWorkspaceHTTPTest(t)
	cfg := config.Default()
	cfg.Metrics.TemplatesFile = ""
	cfg.Topology.File = filepath.Join(t.TempDir(), "topology.json")
	raw := `{"version":"test-v1","source_ref":"controlled:config","valid_from":"2026-01-01T00:00:00Z","nodes":[{"environment":"test","namespace":"default","service":"api","source_ref":"controlled:shared"},{"environment":"test","namespace":"default","service":"db","source_ref":"controlled:shared"}],"edges":[{"source":"test/default/api","target":"test/default/db","kind":"declared","source_ref":"controlled:dependency"}]}`
	if err := os.WriteFile(cfg.Topology.File, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.RegisterM2(e, cfg); err != nil {
		t.Fatal(err)
	}
	r, _, err := h.Service.DB.Admit(context.Background(), []workspace.Observation{{Name: "CPU", Status: "firing", StartsAt: "2026-10-06T00:00:00Z", Labels: map[string]string{"environment": "test", "service": "api", "alertname": "CPU"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(e)
	defer s.Close()
	status, v := m2GET(t, s, "/api/v1/incidents/"+r.IncidentIDs[0]+"/topology")
	if status != 200 {
		t.Fatalf("%d %+v", status, v)
	}
	g := v["data"].(map[string]any)
	refs := []string{g["source_ref"].(string)}
	for _, n := range g["nodes"].([]any) {
		refs = append(refs, n.(map[string]any)["source_ref"].(string))
	}
	for _, n := range g["edges"].([]any) {
		refs = append(refs, n.(map[string]any)["source_ref"].(string))
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref, "topology_src_") {
			t.Fatal("unresolvable topology ref", ref)
		}
		status, ev := m2GET(t, s, "/api/v1/evidence/"+ref)
		if status != 200 {
			t.Fatalf("source 404: %d %+v", status, ev)
		}
		data := ev["data"].(map[string]any)
		meta := data["metadata"].(map[string]any)
		if data["kind"] != "topology_definition" || !strings.Contains(data["source_ref"].(string), "controlled:") || data["snippet"] == "" || meta["version"] != "test-v1" || meta["content_sha256"] != g["content_sha256"] {
			t.Fatalf("provenance lost: %+v", data)
		}
	}
	if _, err = h.Service.DB.SQL.Exec("SELECT count(*) FROM topology_versions"); err != nil {
		t.Fatal(err)
	}
}
