package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
)

// F08：/reindex 运行时重载 demo 不得抹掉 upload 注册表条目——upload 文档
// 必须仍在 /list、/delete 可删；demo 条目按目录现状重建；撞名 demo 覆盖 upload。
func TestReindexPreservesUploadTitles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	demoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(demoDir, "demo_doc.md"), []byte("# Demo Doc\n演示内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := store.NewMemoryVector()
	r := rag.New(s, nil)
	h := New(s, r, demoDir)
	if _, err := h.ReindexLoad(); err != nil {
		t.Fatal(err)
	}
	e := gin.New()
	e.POST("/upload", h.Upload)
	e.POST("/reindex", h.Reindex)
	e.GET("/list", h.List)
	e.DELETE("/delete", h.Delete)

	do := func(method, target, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, target, nil)
		} else {
			req = httptest.NewRequest(method, target, bytes.NewBufferString(body))
		}
		e.ServeHTTP(w, req)
		return w
	}
	listTitles := func(w *httptest.ResponseRecorder) []string {
		var resp struct {
			Titles []string `json:"titles"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("list decode: %v (%s)", err, w.Body.String())
		}
		return resp.Titles
	}
	contains := func(titles []string, want string) bool {
		for _, ti := range titles {
			if ti == want {
				return true
			}
		}
		return false
	}

	if w := do(http.MethodPost, "/upload", `{"title":"My Upload Doc","content":"# My Upload Doc\n上传正文"}`); w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodPost, "/reindex", ""); w.Code != http.StatusOK {
		t.Fatalf("reindex: %d %s", w.Code, w.Body.String())
	}
	titles := listTitles(do(http.MethodGet, "/list", ""))
	if !contains(titles, "My Upload Doc") {
		t.Fatalf("upload title lost after reindex: %v", titles)
	}
	if !contains(titles, "Demo Doc") {
		t.Fatalf("demo title missing after reindex: %v", titles)
	}
	if w := do(http.MethodDelete, "/delete?title=My%20Upload%20Doc", ""); w.Code != http.StatusOK {
		t.Fatalf("delete upload doc after reindex: %d %s", w.Code, w.Body.String())
	}
	titles = listTitles(do(http.MethodGet, "/list", ""))
	if contains(titles, "My Upload Doc") {
		t.Fatalf("deleted upload title still listed: %v", titles)
	}
}
