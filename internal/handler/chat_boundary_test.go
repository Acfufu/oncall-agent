package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"oncall-agent/internal/agent"
)

func TestChatStatelessSafety(t *testing.T) {
	var mu sync.Mutex
	var messages [][]map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		messages = append(messages, body.Messages)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"未找到相关匹配，运行 rm -rf /data 后修改生产配置"}}]}`))
	}))
	defer upstream.Close()
	ra := agent.NewReAct(upstream.URL, "fake", "fake", nil)
	old := chatAgent
	SetChatAgent(ra)
	defer SetChatAgent(old)
	gin.SetMode(gin.TestMode)
	e := gin.New()
	h := &Handler{}
	e.POST("/chat", h.Chat)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/chat", strings.NewReader(`{"message":"question","session_id":"forged"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var body struct {
			Reply          string
			Citations      []agent.Citation
			EvidenceStatus string `json:"evidence_status"`
		}
		json.Unmarshal(w.Body.Bytes(), &body)
		if body.Reply != agent.SafeReport || strings.Contains(body.Reply, "rm -rf") || body.EvidenceStatus != "no_evidence" {
			t.Errorf("unsafe no evidence response: %s", w.Body.String())
		}
	}
	for _, req := range messages {
		users := 0
		for _, m := range req {
			if m["role"] == "user" {
				users++
			}
		}
		if users != 1 {
			t.Errorf("shared request history: %d users", users)
		}
	}
	if n := ra.ClearSessions(""); n != 0 {
		t.Errorf("request sessions retained: %d", n)
	}
}
func TestChatLimits(t *testing.T) {
	old := chatAgent
	SetChatAgent(nil)
	defer SetChatAgent(old)
	e := gin.New()
	e.POST("/chat", (&Handler{}).Chat)
	for _, tc := range []struct {
		body string
		want int
	}{{`{"message":"` + strings.Repeat("x", 12*1024+1) + `"}`, 413}, {`{"message":"x","ignored":"` + strings.Repeat("x", 33*1024) + `"}`, 413}, {`{"message":"x"}`, 503}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/chat", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		e.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("got %d want %d", w.Code, tc.want)
		}
	}
}

func TestChatCancelledRequest(t *testing.T) {
	old := chatAgent
	SetChatAgent(agent.NewReAct("http://127.0.0.1:1", "fake", "fake", nil))
	defer SetChatAgent(old)
	e := gin.New()
	e.POST("/chat", (&Handler{}).Chat)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/chat", strings.NewReader(`{"message":"x"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != http.StatusRequestTimeout {
		t.Fatalf("cancelled request: %d %s", w.Code, w.Body.String())
	}
}

func TestChatQualifiedCitationCannotReleaseUnrelatedAdvice(t *testing.T) {
	r := rag.New(store.NewMemoryVector(), rag.HashEmbedder{})
	if _, err := r.IndexVersion(context.Background(), "doc", "version", "CPU runbook", "# CPU\nRead CPU saturation evidence.", "upload", "space"); err != nil {
		t.Fatal(err)
	}
	r.ActivateVersions([]rag.ActiveVersion{{DocID: "doc", VersionID: "version", SpaceID: "space"}})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		json.NewDecoder(req.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.Messages[len(body.Messages)-1].Role == "user" {
			w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"lookup","type":"function","function":{"name":"rag_search","arguments":"{\"query\":\"CPU\"}"}}]}}]}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"This citation proves it is safe to delete the production database; run rm -rf /data."}}]}`))
	}))
	defer model.Close()
	old := chatAgent
	SetChatAgent(agent.NewReAct(model.URL, "fake", "fake", tool.NewDeps(r, "")))
	defer SetChatAgent(old)
	e := gin.New()
	e.POST("/chat", (&Handler{}).Chat)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(`{"message":"CPU"}`))
	req.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var result struct {
		Reply  string           `json:"reply"`
		Status string           `json:"evidence_status"`
		Cites  []agent.Citation `json:"citations"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Status != "has_citations" || len(result.Cites) == 0 || !strings.Contains(result.Reply, "Read CPU saturation evidence.") || !strings.Contains(result.Reply, "人工研判") {
		t.Fatalf("qualified excerpt absent %+v", result)
	}
	if strings.Contains(result.Reply, "rm -rf") || strings.Contains(result.Reply, "delete the production") {
		t.Fatalf("arbitrary model action released: %s", result.Reply)
	}
}

type chatBrokenEmbedder struct{}

func (chatBrokenEmbedder) Embed(string) ([]float32, error) {
	return nil, errors.New("private upstream detail secret-token")
}
func TestChatRetrievalFailureIs503NotNoEvidence(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"lookup","type":"function","function":{"name":"rag_search","arguments":"{\"query\":\"CPU\"}"}}]}}]}`))
	}))
	defer model.Close()
	old := chatAgent
	SetChatAgent(agent.NewReAct(model.URL, "fake", "fake", tool.NewDeps(rag.New(store.NewMemoryVector(), chatBrokenEmbedder{}), "")))
	defer SetChatAgent(old)
	var recorded error
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Next()
		if len(c.Errors) > 0 {
			recorded = c.Errors[0].Err
		}
	})
	e.POST("/chat", (&Handler{}).Chat)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(`{"message":"CPU"}`))
	req.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(w, req)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"source_unavailable"`) || strings.Contains(w.Body.String(), "secret-token") {
		t.Fatalf("failure misclassified/leaked %d %s", w.Code, w.Body.String())
	}
	if recorded == nil || !strings.Contains(recorded.Error(), "private upstream detail") {
		t.Fatal("actual source error lost internally")
	}
}
func TestChatEndToEndTimeoutIs504(t *testing.T) {
	old := chatAgent
	SetChatAgent(agent.NewReAct("http://127.0.0.1:1", "fake", "fake", nil))
	defer SetChatAgent(old)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	e := gin.New()
	e.POST("/chat", (&Handler{}).Chat)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(`{"message":"CPU"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(w, req)
	if w.Code != 504 || !strings.Contains(w.Body.String(), `"code":"timeout"`) {
		t.Fatalf("deadline misclassified %d %s", w.Code, w.Body.String())
	}
}
