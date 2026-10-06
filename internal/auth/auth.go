// Package auth implements the shared console and Alertmanager HTTP boundary.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const cookieName = "oncall_session"

type session struct {
	csrf             string
	expires, created time.Time
}
type window struct {
	start time.Time
	count int
}
type Auth struct {
	console, webhook       [32]byte
	hasConsole, hasWebhook bool
	localHTTP              bool
	ttl                    time.Duration
	maxSessions            int
	mu                     sync.Mutex
	sessions               map[string]session
	limits                 map[string]window
}
type Option func(*Auth)

func WithLocalhostHTTP(enabled bool) Option { return func(a *Auth) { a.localHTTP = enabled } }
func WithSessionLimits(ttl time.Duration, max int) Option {
	return func(a *Auth) {
		if ttl > 0 {
			a.ttl = ttl
		}
		if max > 0 {
			a.maxSessions = max
		}
	}
}
func New(consoleToken, webhookToken string, opts ...Option) *Auth {
	a := &Auth{console: sha256.Sum256([]byte(consoleToken)), webhook: sha256.Sum256([]byte(webhookToken)), hasConsole: consoleToken != "", hasWebhook: webhookToken != "", ttl: 8 * time.Hour, maxSessions: 100, sessions: map[string]session{}, limits: map[string]window{}}
	for _, o := range opts {
		o(a)
	}
	return a
}
func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure random unavailable")
	}
	return hex.EncodeToString(b)
}
func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func (a *Auth) bearer(c *gin.Context) (scope string, present bool) {
	h := c.GetHeader("Authorization")
	if h == "" {
		return "", false
	}
	parts := strings.Fields(h)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", true
	}
	sum := sha256.Sum256([]byte(parts[1]))
	if a.hasConsole && subtle.ConstantTimeCompare(sum[:], a.console[:]) == 1 {
		return "console", true
	}
	if a.hasWebhook && subtle.ConstantTimeCompare(sum[:], a.webhook[:]) == 1 {
		return "webhook", true
	}
	return "", true
}
func fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": message, "code": code, "retryable": status == 429 || status == 503, "request_id": randomID()})
}
func response(c *gin.Context, data any) {
	c.JSON(200, gin.H{"data": data, "meta": gin.H{"request_id": randomID(), "as_of": time.Now().UTC().Format(time.RFC3339Nano), "data_state": "fresh", "mode": "live"}})
}
func (a *Auth) allow(key string, max int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, v := range a.limits {
		if now.Sub(v.start) >= time.Minute {
			delete(a.limits, k)
		}
	}
	if len(a.limits) >= 10000 {
		if _, exists := a.limits[key]; !exists {
			return false
		}
	}
	v := a.limits[key]
	if v.start.IsZero() {
		v.start = now
	}
	v.count++
	a.limits[key] = v
	return v.count <= max
}
func (a *Auth) rate(c *gin.Context, key string, max int) bool {
	if a.allow(key, max) {
		return true
	}
	c.Header("Retry-After", "60")
	fail(c, 429, "rate_limited", "request rate exceeded")
	return false
}
func localhost(host string) bool {
	h := host
	if x, _, err := net.SplitHostPort(host); err == nil {
		h = x
	}
	h = strings.Trim(h, "[]")
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
func (a *Auth) originOK(c *gin.Context, required bool) bool {
	origin := c.GetHeader("Origin")
	if origin == "" {
		return !required
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Host != c.Request.Host {
		return false
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	return u.Scheme == scheme
}
func (a *Auth) cookieSecure(c *gin.Context) bool {
	return !(a.localHTTP && localhost(c.Request.Host) && c.Request.TLS == nil)
}
func (a *Auth) getSession(c *gin.Context) (string, session, bool) {
	id, err := c.Cookie(cookieName)
	if err != nil {
		return "", session{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[id]
	if ok && time.Now().After(s.expires) {
		delete(a.sessions, id)
		ok = false
	}
	return id, s, ok
}
func (a *Auth) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, present := a.bearer(c)
		id := "console"
		if present {
			if scope == "" {
				if !a.hasConsole && !a.hasWebhook {
					fail(c, 503, "auth_not_configured", "authentication not configured")
				} else {
					fail(c, 401, "unauthorized", "authentication required")
				}
				return
			}
			if scope == "webhook" && (c.Request.Method != "POST" || c.Request.URL.Path != "/alert") {
				fail(c, 403, "forbidden", "credential scope denied")
				return
			}
			id = scope
		} else {
			if !a.hasConsole {
				fail(c, 503, "auth_not_configured", "console authentication not configured")
				return
			}
			sid, s, ok := a.getSession(c)
			if !ok {
				fail(c, 401, "unauthorized", "authentication required")
				return
			}
			if a.cookieSecure(c) && c.Request.TLS == nil {
				fail(c, 403, "secure_transport_required", "HTTPS required")
				return
			}
			if c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS" {
				if !a.originOK(c, true) || !equal(c.GetHeader("X-CSRF-Token"), s.csrf) {
					fail(c, 403, "csrf_rejected", "Origin or CSRF verification failed")
					return
				}
			}
			id = "session:" + sid
			scope = "console"
		}
		c.Set("auth_scope", scope)
		c.Set("auth_identity", id)
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" {
			if !a.rate(c, "read:"+id, 120) {
				return
			}
		}
		if c.Request.Method == "POST" && strings.HasPrefix(c.Request.URL.Path, "/api/v1/incidents/") && strings.HasSuffix(c.Request.URL.Path, "/runs") {
			if !a.rate(c, "runs:"+id, 10) {
				return
			}
		}
		c.Next()
	}
}
func (a *Auth) Register(e *gin.Engine) {
	e.POST("/api/v1/auth/session", a.login)
	g := e.Group("/api/v1/auth", a.Middleware())
	g.GET("/session", a.current)
	g.DELETE("/session", a.logout)
}
func (a *Auth) login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !a.hasConsole {
		fail(c, 503, "auth_not_configured", "console authentication not configured")
		return
	}
	ip, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		ip = c.Request.RemoteAddr
	}
	if !a.rate(c, "login:"+ip, 5) {
		return
	}
	scope, _ := a.bearer(c)
	if scope != "console" {
		fail(c, 401, "unauthorized", "console authentication required")
		return
	}
	if !a.originOK(c, false) {
		fail(c, 403, "csrf_rejected", "Origin verification failed")
		return
	}
	secure := a.cookieSecure(c)
	if secure && c.Request.TLS == nil {
		fail(c, 403, "secure_transport_required", "HTTPS required")
		return
	}
	id, csrf := randomID(), randomID()
	now := time.Now()
	s := session{csrf: csrf, created: now, expires: now.Add(a.ttl)}
	a.mu.Lock()
	for k, v := range a.sessions {
		if now.After(v.expires) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= a.maxSessions {
		oldID := ""
		var oldest time.Time
		for k, v := range a.sessions {
			if oldID == "" || v.created.Before(oldest) {
				oldID = k
				oldest = v.created
			}
		}
		delete(a.sessions, oldID)
	}
	a.sessions[id] = s
	a.mu.Unlock()
	http.SetCookie(c.Writer, &http.Cookie{Name: cookieName, Value: id, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: int(a.ttl.Seconds()), Expires: s.expires})
	response(c, gin.H{"authenticated": true, "csrf_token": csrf, "expires_at": s.expires.UTC().Format(time.RFC3339), "capabilities": []string{"console", "readonly_tools"}})
}
func (a *Auth) current(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	_, s, ok := a.getSession(c)
	csrf := ""
	var expiry any
	if ok {
		csrf = s.csrf
		expiry = s.expires.UTC().Format(time.RFC3339)
	}
	response(c, gin.H{"authenticated": true, "csrf_token": csrf, "expires_at": expiry, "capabilities": []string{"console", "readonly_tools"}})
}
func (a *Auth) logout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, _, _ := a.getSession(c)
	a.mu.Lock()
	delete(a.sessions, id)
	a.mu.Unlock()
	http.SetCookie(c.Writer, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.cookieSecure(c), SameSite: http.SameSiteStrictMode, MaxAge: -1})
	response(c, gin.H{"authenticated": false})
}
