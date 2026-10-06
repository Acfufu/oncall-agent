package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"oncall-agent/internal/agent"
	"oncall-agent/internal/tool"
	"testing"
)

func TestChatOriginalQueryAndEnvironmentQualification(t *testing.T) {
	e, h := newWorkspaceHTTPTest(t)
	_, _, err := h.Service.PutDocument(context.Background(), "", "CPUHigh runbook", "# CPUHigh\nOnly CPUHigh recorded knowledge", "upload", "test", "", "qualified-doc")
	if err != nil {
		t.Fatal(err)
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Messages []struct{ Role string } }
		json.NewDecoder(r.Body).Decode(&b)
		w.Header().Set("Content-Type", "application/json")
		if b.Messages[len(b.Messages)-1].Role == "user" {
			w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"c","type":"function","function":{"name":"rag_search","arguments":"{\"query\":\"CPUHigh\"}"}}]}}]}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"引用已提供，请立即改变生产配置"}}]}`))
	}))
	defer model.Close()
	old := chatAgent
	defer SetChatAgent(old)
	SetChatAgent(agent.NewReAct(model.URL, "test", "test", tool.NewDeps(h.Service.RAG, "")))
	e.POST("/chat", (&Handler{}).Chat)
	for _, tc := range []struct {
		query, env string
		eligible   bool
	}{{"ShardingChaos", "test", false}, {"CPUHigh", "prod", false}, {"CPUHigh", "test", true}} {
		body, _ := json.Marshal(map[string]string{"message": tc.query, "environment": tc.env})
		status, result := wsTestRequest(t, e, "POST", "/chat", string(body), nil)
		requireWSStatus(t, status, 200, result)
		cites := result["citations"].([]any)
		if (len(cites) > 0) != tc.eligible {
			t.Fatalf("query=%s env=%s unrelated/inapplicable citation %+v", tc.query, tc.env, result)
		}
		if !tc.eligible && result["reply"] != agent.SafeReport {
			t.Fatal(result)
		}
	}
}
