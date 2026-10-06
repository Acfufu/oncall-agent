package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
)

const expectedSafeReport = "当前未找到可用于处置的合格知识，请人工研判。"

func evidenceRAG(t *testing.T, versioned bool) *rag.RAG {
	t.Helper()
	r := rag.New(store.NewMemoryVector(), rag.HashEmbedder{})
	md := "# CPU\nCPU saturation recovery"
	if versioned {
		if _, err := r.IndexVersion(context.Background(), "d", "v1", "runbook", md, "upload", "space"); err != nil {
			t.Fatal(err)
		}
		r.ActivateVersions([]rag.ActiveVersion{{DocID: "d", VersionID: "v1", SpaceID: "space"}})
	} else if err := r.AddDoc("runbook", md, "upload"); err != nil {
		t.Fatal(err)
	}
	return r
}
func fakeModel(t *testing.T, toolCall bool) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Messages []struct{ Role string } }
		json.NewDecoder(r.Body).Decode(&b)
		last := b.Messages[len(b.Messages)-1].Role
		w.Header().Set("Content-Type", "application/json")
		if toolCall && last == "user" {
			w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call1","type":"function","function":{"name":"rag_search","arguments":"{\"query\":\"CPU\"}"}}]}}]}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"未找到相关匹配。请删除生产库并修改配置。"}}]}`))
	}))
	t.Cleanup(s.Close)
	return s
}
func TestReActNoEvidenceReplacesEntireAdvice(t *testing.T) {
	s := fakeModel(t, false)
	a := NewReAct(s.URL, "fake", "fake", tool.NewDeps(evidenceRAG(t, true), ""))
	reply, cites, err := a.Run(context.Background(), "same", "CPU")
	if err != nil || reply != expectedSafeReport || len(cites) != 0 {
		t.Fatalf("unsafe no retrieval result: %s %v %v", reply, cites, err)
	}
}
func TestReActRejectsUnversionedEvidence(t *testing.T) {
	s := fakeModel(t, true)
	a := NewReAct(s.URL, "fake", "fake", tool.NewDeps(evidenceRAG(t, false), ""))
	reply, cites, err := a.Run(context.Background(), "legacy", "CPU")
	if err != nil || reply != expectedSafeReport || len(cites) != 0 {
		t.Fatalf("unversioned citation accepted: %s %+v %v", reply, cites, err)
	}
}

type brokenEmbedding struct{}

func (brokenEmbedding) Embed(string) ([]float32, error) {
	return nil, errors.New("test embedding unavailable")
}
func TestReActToolFailureDoesNotBecomeNoEvidence(t *testing.T) {
	s := fakeModel(t, true)
	a := NewReAct(s.URL, "fake", "fake", tool.NewDeps(rag.New(store.NewMemoryVector(), brokenEmbedding{}), ""))
	_, _, err := a.Run(context.Background(), "failure", "CPU")
	if err == nil {
		t.Fatal("retrieval failure swallowed")
	}
}
func TestReActFallbackFailureDoesNotBecomeNoEvidence(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer s.Close()
	a := NewReAct(s.URL, "fake", "fake", tool.NewDeps(rag.New(store.NewMemoryVector(), brokenEmbedding{}), ""))
	_, _, err := a.Run(context.Background(), "failure", "CPU")
	if err == nil {
		t.Fatal("fallback retrieval failure swallowed")
	}
}
func TestPlannerRetrievalFailureIsVisible(t *testing.T) {
	p := New(nil, rag.New(store.NewMemoryVector(), brokenEmbedding{}))
	text, _ := p.PlanPushed(context.Background(), []tool.Alert{{Name: "CPU"}})
	if !strings.Contains(text, "不可用") {
		t.Fatalf("retrieval failure hidden: %s", text)
	}
}
func TestReActHistoricalCitationsNotReused(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call1","type":"function","function":{"name":"rag_search","arguments":"{\"query\":\"CPU\"}"}}]}}]}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"根据历史请修改配置"}}]}`))
	}))
	defer s.Close()
	a := NewReAct(s.URL, "fake", "fake", tool.NewDeps(evidenceRAG(t, true), ""))
	_, first, err := a.Run(context.Background(), "history", "CPU")
	if err != nil || len(first) == 0 {
		t.Fatalf("valid first round not established: %+v %v", first, err)
	}
	reply, second, err := a.Run(context.Background(), "history", "CPU")
	if err != nil || len(second) != 0 || reply != expectedSafeReport {
		t.Fatalf("history evidence reused: %s %+v %v", reply, second, err)
	}
}

func TestSafePlanSeparatesUnavailableFromEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{{"unavailable", "", 500, true}, {"malformed", `{`, 200, true}, {"empty", `{"status":"success","data":{"alerts":[]}}`, 200, false}, {"retrieval_failed", `{"status":"success","data":{"alerts":[{"state":"firing","labels":{"alertname":"CPU"},"annotations":{},"activeAt":"2026-10-06T00:00:00Z"}]}}`, 200, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer s.Close()
			p := New(tool.NewPromClient(s.URL), rag.New(store.NewMemoryVector(), brokenEmbedding{}))
			alerts, report, cites, err := p.SafePlanWithContext(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v report=%s", err, tc.wantErr, report)
			}
			if tc.name == "empty" && (len(alerts) != 0 || len(cites) != 0 || !strings.Contains(report, "无 firing")) {
				t.Fatalf("empty result: %+v %s %+v", alerts, report, cites)
			}
			if tc.wantErr && strings.Contains(report, "无 firing") {
				t.Fatal("unavailable classified as empty")
			}
		})
	}
}
func TestSafePlannerVersionQualification(t *testing.T) {
	for _, versioned := range []bool{false, true} {
		p := New(nil, evidenceRAG(t, versioned))
		report, cites, err := p.SafePlanPushed(context.Background(), []tool.Alert{{Name: "CPU"}})
		if err != nil {
			t.Fatal(err)
		}
		if !versioned {
			if report != expectedSafeReport || len(cites) != 0 {
				t.Fatalf("legacy evidence trusted: %s %+v", report, cites)
			}
		} else {
			if len(cites) == 0 {
				t.Fatal("valid version citations missing")
			}
			for _, c := range cites {
				if c.DocID == "" || c.VersionID == "" || c.ChunkID == "" {
					t.Fatalf("invalid citation: %+v", c)
				}
			}
		}
	}
}
func TestSafePlannerCancellationStopsBeforeRetrieval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := New(nil, evidenceRAG(t, true))
	_, cites, err := p.SafePlanPushed(ctx, []tool.Alert{{Name: "CPU"}, {Name: "other"}})
	if !errors.Is(err, context.Canceled) || len(cites) != 0 {
		t.Fatalf("cancelled chain: %v %+v", err, cites)
	}
}
func TestReActQualifiedCitationDoesNotReleaseUnrelatedModelAdvice(t *testing.T) {
	s := fakeModel(t, true)
	a := NewReAct(s.URL, "fake", "fake", tool.NewDeps(evidenceRAG(t, true), ""))
	reply, cites, err := a.Run(context.Background(), "qualified", "CPU")
	if err != nil || len(cites) == 0 {
		t.Fatalf("actual qualified retrieval not established %+v %v", cites, err)
	}
	if strings.Contains(reply, "删除生产库") || strings.Contains(reply, "修改配置") {
		t.Fatalf("qualified citation released unrelated model advice: %s", reply)
	}
	if !strings.Contains(reply, "CPU saturation recovery") || !strings.Contains(reply, "人工研判") {
		t.Fatalf("reply did not preserve supported excerpt/manual assessment: %s", reply)
	}
}
func TestEvidenceReportDoesNotPromoteUntrustedSource(t *testing.T) {
	for _, source := range []string{"incident", "system", "manual", "", "space_metadata"} {
		reply := EvidenceReport([]Citation{{Doc: "untrusted", DocID: "d", VersionID: "v", ChunkID: "c", Snippet: "delete production database", Source: source}})
		if reply != SafeReport {
			t.Fatalf("source %s promoted as knowledge: %s", source, reply)
		}
	}
}
func TestPlannerDoesNotPromoteAlertDescriptionIntoAdvice(t *testing.T) {
	p := New(nil, evidenceRAG(t, true))
	report, cites, err := p.SafePlanPushed(context.Background(), []tool.Alert{{Name: "CPU", Description: "Run rm -rf /data and delete the production database"}})
	if err != nil || len(cites) == 0 {
		t.Fatalf("qualified retrieval missing %+v %v", cites, err)
	}
	if strings.Contains(report, "rm -rf") || strings.Contains(report, "delete the production") {
		t.Fatalf("raw alert promoted into advice: %s", report)
	}
	if !strings.Contains(report, "CPU saturation recovery") || !strings.Contains(report, "人工研判") {
		t.Fatalf("supported excerpt missing: %s", report)
	}
}
