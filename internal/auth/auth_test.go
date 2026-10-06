package auth

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setup(a *Auth) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	a.Register(e)
	g := e.Group("", a.Middleware())
	for _, p := range []string{"/chat", "/upload", "/alert", "/mcp", "/api/v1/incidents/x/runs"} {
		g.POST(p, func(c *gin.Context) { c.Status(200) })
	}
	for _, p := range []string{"/api/v1/evidence/x", "/api/v1/incidents/x/graph", "/mcp"} {
		g.GET(p, func(c *gin.Context) { c.Status(200) })
	}
	return e
}
func request(e *gin.Engine, method, path, token string, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}
func TestPermissionBoundary(t *testing.T) {
	for _, p := range []string{"/chat", "/upload", "/mcp", "/api/v1/evidence/x", "/api/v1/incidents/x/graph"} {
		method := "POST"
		if strings.HasPrefix(p, "/api/") {
			method = "GET"
		}
		for _, tc := range []struct {
			token  string
			status int
		}{{"", 401}, {"am", 403}, {"console", 200}} {
			w := request(setup(New("console", "am")), method, p, tc.token, nil, "", "")
			if w.Code != tc.status {
				t.Fatalf("%s %s got %d", p, tc.token, w.Code)
			}
		}
	}
	e := setup(New("console", "am"))
	if w := request(e, "POST", "/alert", "am", nil, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(setup(New("", "")), "POST", "/chat", "", nil, "", ""); w.Code != 503 {
		t.Fatal(w.Code)
	}
}
func TestCookieCSRF(t *testing.T) {
	e := setup(New("console", "am", WithLocalhostHTTP(true)))
	login := request(e, "POST", "/api/v1/auth/session", "console", nil, "", "")
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal(cookies)
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Secure {
		t.Fatal(cookie)
	}
	var body struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		}
	}
	json.Unmarshal(login.Body.Bytes(), &body)
	if body.Data.CSRF == "" {
		t.Fatal("missing csrf")
	}
	for _, tc := range []struct {
		csrf, origin string
		status       int
	}{{"", "", 403}, {body.Data.CSRF, "http://evil.example", 403}, {body.Data.CSRF, "http://localhost", 200}} {
		if w := request(e, "POST", "/chat", "", cookie, tc.csrf, tc.origin); w.Code != tc.status {
			t.Fatalf("got %d want %d", w.Code, tc.status)
		}
	}
	if w := request(e, "GET", "/api/v1/auth/session", "", cookie, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(e, "DELETE", "/api/v1/auth/session", "", cookie, body.Data.CSRF, "http://localhost"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(e, "GET", "/api/v1/evidence/x", "", cookie, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestRateLimits(t *testing.T) {
	e := setup(New("console", "am", WithLocalhostHTTP(true)))
	for i := 0; i < 6; i++ {
		w := request(e, "POST", "/api/v1/auth/session", "bad", nil, "", "")
		want := 401
		if i == 5 {
			want = 429
		}
		if w.Code != want {
			t.Fatal(i, w.Code)
		}
	}
	e = setup(New("console", "am"))
	for i := 0; i < 121; i++ {
		w := request(e, "GET", "/api/v1/evidence/x", "console", nil, "", "")
		want := 200
		if i == 120 {
			want = 429
		}
		if w.Code != want {
			t.Fatal(i, w.Code)
		}
	}
	e = setup(New("console", "am"))
	for i := 0; i < 11; i++ {
		w := request(e, "POST", "/api/v1/incidents/x/runs", "console", nil, "", "")
		want := 200
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatal(i, w.Code)
		}
	}
}

func TestSecureTransportAndSessionExpiry(t *testing.T) {
	e := setup(New("console", "am"))
	if w := request(e, "POST", "/api/v1/auth/session", "console", nil, "", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	a := New("console", "am", WithLocalhostHTTP(true), WithSessionLimits(time.Millisecond, 1))
	e = setup(a)
	w := request(e, "POST", "/api/v1/auth/session", "console", nil, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	cookie := w.Result().Cookies()[0]
	time.Sleep(2 * time.Millisecond)
	if w = request(e, "GET", "/api/v1/evidence/x", "", cookie, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	a = New("console", "am", WithLocalhostHTTP(true), WithSessionLimits(time.Hour, 1))
	e = setup(a)
	first := request(e, "POST", "/api/v1/auth/session", "console", nil, "", "").Result().Cookies()[0]
	second := request(e, "POST", "/api/v1/auth/session", "console", nil, "", "").Result().Cookies()[0]
	if w = request(e, "GET", "/api/v1/evidence/x", "", first, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w = request(e, "GET", "/api/v1/evidence/x", "", second, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("POST", "https://example.test/api/v1/auth/session", nil)
	r.Header.Set("Authorization", "Bearer console")
	w = httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 200 || !w.Result().Cookies()[0].Secure {
		t.Fatalf("secure login %d", w.Code)
	}
	r = httptest.NewRequest("POST", "http://example.test/api/v1/auth/session", nil)
	r.Header.Set("Authorization", "Bearer console")
	w = httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
