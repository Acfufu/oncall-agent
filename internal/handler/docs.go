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
	if err := h.RAG.AddDoc(req.Title, req.Content); err != nil {
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
// v0.1: only drops the handler registry entry; vectors already upserted
// stay in store (store 无 delete 接口，本片不加)。
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
	n, ok := h.removeTitle(title)
	if !ok {
		c.JSON(http.StatusNotFound, errJSON("not found"))
		return
	}
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
func (h *Handler) loadDemo() (int, error) {
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
		if err := h.RAG.AddDoc(title, content); err != nil {
			return 0, err
		}
		fresh[title] = content
	}
	return h.resetTitles(fresh), nil
}
