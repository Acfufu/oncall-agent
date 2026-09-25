package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// GET /ping -> {"status":"ok"}
func (h *Handler) Ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type uploadReq struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// POST /upload {title,content} -> {"title":...,"count":n}
func (h *Handler) Upload(c *gin.Context) {
	var req uploadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errJSON("invalid json"))
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || strings.TrimSpace(req.Content) == "" {
		c.JSON(http.StatusBadRequest, errJSON("title and content required"))
		return
	}
	if err := h.RAG.AddDoc(req.Title, req.Content, "upload"); err != nil {
		c.JSON(http.StatusInternalServerError, errJSON("store failed"))
		return
	}
	n := h.saveTitle(req.Title, req.Content)
	c.JSON(http.StatusOK, gin.H{"title": req.Title, "count": n})
}

// GET /list -> {"titles":[...],"count":n}
func (h *Handler) List(c *gin.Context) {
	titles := h.listTitles()
	if titles == nil {
		titles = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"titles": titles, "count": len(titles)})
}

type deleteReq struct {
	Title string `json:"title"`
}

// DELETE /delete?title=xxx (or JSON body {title}) -> {"title":...,"count":n}
// v0.3: 删全——store 按 payload.doc 过滤真删向量 + BM25 镜像清理 + 注册表摘除；
// store 删失败返回 500 且注册表不动。仅注册表在册文档可删（跨进程残留向量
// 不在册，返回 404）。
func (h *Handler) Delete(c *gin.Context) {
	title := strings.TrimSpace(c.Query("title"))
	if title == "" {
		var req deleteReq
		if err := c.ShouldBindJSON(&req); err == nil {
			title = strings.TrimSpace(req.Title)
		}
	}
	if title == "" {
		c.JSON(http.StatusBadRequest, errJSON("title required"))
		return
	}
	if !h.hasTitle(title) {
		c.JSON(http.StatusNotFound, errJSON("not found"))
		return
	}
	if err := h.RAG.DeleteDoc(title); err != nil {
		c.JSON(http.StatusInternalServerError, errJSON("store delete failed"))
		return
	}
	n, _ := h.removeTitle(title)
	c.JSON(http.StatusOK, gin.H{"title": title, "count": n})
}

// POST /reindex -> reload aiops-docs-demo/ -> {"count":n}
func (h *Handler) Reindex(c *gin.Context) {
	n, err := h.loadDemo()
	if err != nil {
		c.JSON(http.StatusInternalServerError, errJSON("reindex failed"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": n})
}

// ReindexLoad is the exported boot-time form of loadDemo.
func (h *Handler) ReindexLoad() (int, error) { return h.loadDemo() }

// loadDemo reads every *.md under DemoDir into rag + registry.
// Title: first "# heading" wins, else filename sans ext.
// v0.3 同步语义：先清全部 source=demo 的旧向量（含目录已消失文档与同名
// 陈旧 chunk），再重灌当前目录；source=upload 的上传文档不动。
func (h *Handler) loadDemo() (int, error) {
	if err := h.RAG.DeleteSource("demo"); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(h.DemoDir)
	if err != nil {
		return 0, err
	}
	fresh := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(h.DemoDir, e.Name()))
		if err != nil {
			return 0, err
		}
		content := string(b)
		title := strings.TrimSuffix(e.Name(), ".md")
		for _, line := range strings.Split(content, "\n") {
			if t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "# ")); strings.HasPrefix(strings.TrimSpace(line), "# ") && t != "" {
				title = t
				break
			}
		}
		if err := h.RAG.AddDoc(title, content, "demo"); err != nil {
			return 0, err
		}
		fresh[title] = content
	}
	return h.resetTitles(fresh), nil
}
