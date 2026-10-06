package handler

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"oncall-agent/internal/config"
)

func contractMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func TestAPI01OpenAPIActualRoutesAndResponses(t *testing.T) {
	raw, err := os.ReadFile("../../docs/api/workspace.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err = yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	paths := contractMap(spec["paths"])
	schemas := contractMap(contractMap(spec["components"])["schemas"])
	e, h := newWorkspaceHTTPTest(t)
	cfg := config.Default()
	cfg.Metrics.TemplatesFile = ""
	cfg.Topology.File = ""
	if err := h.RegisterM2(e, cfg); err != nil {
		t.Fatal(err)
	}
	colon := regexp.MustCompile(`:([a-zA-Z_]+)`)
	actual := map[string]bool{}
	for _, r := range e.Routes() {
		p := colon.ReplaceAllString(r.Path, "{$1}")
		method := strings.ToLower(r.Method)
		actual[p+" "+method] = true
		if contractMap(contractMap(paths[p])[method]) == nil {
			t.Errorf("undocumented actual route %s %s", method, p)
		}
	}
	for p, v := range paths {
		if !strings.HasPrefix(p, "/api/v1/") || p == "/api/v1/auth/session" {
			continue
		}
		for method := range contractMap(v) {
			if !actual[p+" "+method] {
				t.Errorf("documented unimplemented route %s %s", method, p)
			}
		}
	}
	validate := func(method, path, status string, response any) {
		t.Helper()
		operation := contractMap(contractMap(paths[path])[method])
		rs := contractMap(contractMap(operation["responses"])[status])
		sch := contractMap(contractMap(contractMap(rs["content"])["application/json"])["schema"])
		if sch == nil {
			t.Fatalf("missing response schema %s %s %s", method, path, status)
		}
		validateContract(t, schemas, sch, response, path)
	}
	for _, p := range []string{"/api/v1/documents", "/api/v1/incidents", "/api/v1/workspace/summary", "/api/v1/system/status"} {
		status, v := wsTestRequest(t, e, "GET", p, "", nil)
		requireWSStatus(t, status, 200, v)
		validate("get", p, "200", v)
	}
	status, v := wsTestRequest(t, e, "POST", "/api/v1/documents", `{"title":"CPU","content":"# CPU\nCPU fixture guidance"}`, map[string]string{"Idempotency-Key": "api01"})
	requireWSStatus(t, status, 201, v)
	validate("post", "/api/v1/documents", "201", v)
	doc := contractMap(contractMap(v["data"])["document"])
	version := contractMap(contractMap(v["data"])["version"])
	p := "/api/v1/documents/" + fmt.Sprint(doc["id"]) + "/versions/" + fmt.Sprint(version["id"])
	status, v = wsTestRequest(t, e, "GET", p, "", nil)
	requireWSStatus(t, status, 200, v)
	validate("get", "/api/v1/documents/{id}/versions/{version}", "200", v)
	status, v = wsTestRequest(t, e, "POST", "/api/v1/documents", `{"title":"Changed","content":"other"}`, map[string]string{"Idempotency-Key": "api01"})
	requireWSStatus(t, status, 409, v)
	validate("post", "/api/v1/documents", "409", v)
	status, v = wsTestRequest(t, e, "POST", "/alert", wsAdmissionBody("API01", "firing"), nil)
	requireWSStatus(t, status, 202, v)
	run := fmt.Sprint(v["id"])
	status, v = wsTestRequest(t, e, "GET", "/api/v1/runs/"+run, "", nil)
	requireWSStatus(t, status, 200, v)
	validate("get", "/api/v1/runs/{id}", "200", v)
	incident := fmt.Sprint(contractMap(v["data"])["incident_ids"].([]any)[0])
	for _, part := range []string{"graph", "topology", "metrics?metric_id=error_rate&start=2026-10-06T10:00:00Z&end=2026-10-06T10:01:00Z&step=15"} {
		status, v = wsTestRequest(t, e, "GET", "/api/v1/incidents/"+incident+"/"+part, "", nil)
		requireWSStatus(t, status, 200, v)
		validate("get", "/api/v1/incidents/{id}/"+strings.Split(part, "?")[0], "200", v)
	}
	t.Log("API-01: registered workspace+M2 route inventory and actual isolated SQLite HTTP 200/201/409 DTO responses match OpenAPI")
}
func validateContract(t *testing.T, schemas map[string]any, schema map[string]any, value any, path string) {
	t.Helper()
	if r, ok := schema["$ref"].(string); ok {
		n := strings.TrimPrefix(r, "#/components/schemas/")
		s := contractMap(schemas[n])
		if s == nil {
			t.Fatalf("unresolved schema %s", r)
		}
		validateContract(t, schemas, s, value, path)
		return
	}
	if alternatives, ok := schema["anyOf"].([]any); ok {
		if value == nil {
			return
		}
		validateContract(t, schemas, contractMap(alternatives[0]), value, path)
		return
	}
	if value == nil {
		switch ty := schema["type"].(type) {
		case string:
			if ty != "null" {
				t.Errorf("%s unexpected null for %s", path, ty)
			}
		case []any:
			allowed := false
			for _, x := range ty {
				allowed = allowed || x == "null"
			}
			if !allowed {
				t.Errorf("%s unexpected null", path)
			}
		}
		return
	}
	switch schema["type"] {
	case "object":
		m := contractMap(value)
		if m == nil {
			t.Errorf("%s expected object got %T", path, value)
			return
		}
		if required, ok := schema["required"].([]any); ok {
			for _, k := range required {
				if _, present := m[fmt.Sprint(k)]; !present {
					t.Errorf("%s missing required %s", path, k)
				}
			}
		}
		for k, s := range contractMap(schema["properties"]) {
			if v, present := m[k]; present {
				validateContract(t, schemas, contractMap(s), v, path+"."+k)
			}
		}
	case "array":
		a, ok := value.([]any)
		if !ok {
			t.Errorf("%s expected array got %T", path, value)
			return
		}
		for i, v := range a {
			validateContract(t, schemas, contractMap(schema["items"]), v, fmt.Sprintf("%s[%d]", path, i))
		}
	case "string":
		if _, ok := value.(string); !ok {
			t.Errorf("%s expected string got %T", path, value)
		}
	case "integer", "number":
		if _, ok := value.(float64); !ok {
			t.Errorf("%s expected number got %T", path, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			t.Errorf("%s expected boolean got %T", path, value)
		}
	}
}
