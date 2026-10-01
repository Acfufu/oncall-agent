package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"oncall-agent/internal/tool"
)

// R08：会话表 LRU 上限——此前 sessions map 只增不减无 TTL/淘汰，唯一
// session_id 永久占内存；且 CONTEXT「可清空」在 API 面无处兑现。
func TestSessionsLRUCapped(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer llm.Close()
	ra := NewReAct(llm.URL, "", "m", tool.NewDeps(nil, ""))

	for i := 0; i < 300; i++ {
		if _, _, err := ra.Run(context.Background(), fmt.Sprintf("s%03d", i), "q"); err != nil {
			t.Fatal(err)
		}
	}
	if len(ra.sessions) > MaxSessions {
		t.Fatalf("sessions map uncapped: %d > %d", len(ra.sessions), MaxSessions)
	}
	// LRU 序：s000-s043 应被逐出，触达 s044 后再进新会话，逐出的是 s045。
	for _, id := range []string{"s044", "s045"} {
		if _, ok := ra.sessions[id]; !ok {
			t.Fatalf("%s should survive initial cap", id)
		}
	}
	if _, _, err := ra.Run(context.Background(), "s044", "q"); err != nil { // touch
		t.Fatal(err)
	}
	if _, _, err := ra.Run(context.Background(), "s300", "q"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ra.sessions["s044"]; !ok {
		t.Fatal("touched session s044 must survive")
	}
	if _, ok := ra.sessions["s045"]; ok {
		t.Fatal("untouched oldest s045 must be evicted")
	}
}

// ClearSessions：指定 id 清 1 个，空 id 清全部，返回清除数。
func TestClearSessions(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer llm.Close()
	ra := NewReAct(llm.URL, "", "m", tool.NewDeps(nil, ""))
	for _, id := range []string{"a", "b", "c"} {
		if _, _, err := ra.Run(context.Background(), id, "q"); err != nil {
			t.Fatal(err)
		}
	}
	if n := ra.ClearSessions("b"); n != 1 {
		t.Fatalf("clear one: want 1, got %d", n)
	}
	if len(ra.sessions) != 2 {
		t.Fatalf("want 2 sessions left, got %d", len(ra.sessions))
	}
	if n := ra.ClearSessions(""); n != 2 {
		t.Fatalf("clear all: want 2, got %d", n)
	}
	if len(ra.sessions) != 0 || len(ra.order) != 0 {
		t.Fatalf("sessions not cleared: %d/%d", len(ra.sessions), len(ra.order))
	}
}

// 端点级回归（handler.SessionClear 逻辑同形，这里验 agent 侧契约形状）。
func TestClearSessionsUnknownID(t *testing.T) {
	ra := NewReAct("", "", "", tool.NewDeps(nil, ""))
	if n := ra.ClearSessions("nope"); n != 0 {
		t.Fatalf("unknown session: want 0, got %d", n)
	}
}
