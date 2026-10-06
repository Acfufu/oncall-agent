package mcpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"oncall-agent/internal/auth"
)

func TestAuthenticatedTransportSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	a := auth.New("console", "webhook")
	g := e.Group("", a.Middleware())
	transport := StreamableHTTPHandler(New(nil, "", "", ""))
	g.GET("/mcp", gin.WrapH(transport))
	g.POST("/mcp", gin.WrapH(transport))
	g.DELETE("/mcp", gin.WrapH(transport))
	server := httptest.NewServer(e)
	defer server.Close()
	sessionID := ""
	call := func(method, body, token string) (int, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, method, server.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
			req.Header.Set("Mcp-Protocol-Version", "2025-03-26")
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if id := res.Header.Get("Mcp-Session-Id"); id != "" {
			sessionID = id
		}
		if method == "GET" && res.StatusCode == 200 {
			return res.StatusCode, "stream established"
		}
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	for _, method := range []string{"GET", "POST", "DELETE"} {
		if code, _ := call(method, "", " "); code != 401 {
			t.Fatalf("anonymous %s: %d", method, code)
		}
		if code, _ := call(method, "", "webhook"); code != 403 {
			t.Fatalf("webhook %s: %d", method, code)
		}
	}
	code, body := call("POST", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"boundary-test","version":"1"}}}`, "console")
	if code != 200 || sessionID == "" || !strings.Contains(body, "protocolVersion") {
		t.Fatalf("initialize %d %s session=%q", code, body, sessionID)
	}
	if code, body = call("POST", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "console"); code != 202 {
		t.Fatalf("initialized %d %s", code, body)
	}
	if code, body = call("POST", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, "console"); code != 200 || !strings.Contains(body, "time_now") {
		t.Fatalf("tools/list %d %s", code, body)
	}
	if code, body = call("GET", "", "console"); code != 200 {
		t.Fatalf("GET stream %d %s", code, body)
	}
	if code, body = call("DELETE", "", "console"); code != 204 {
		t.Fatalf("DELETE %d %s", code, body)
	}
}
