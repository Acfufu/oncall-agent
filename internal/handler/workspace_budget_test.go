package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
)

type budgetEmbedder struct {
	calls    atomic.Int32
	deadline chan time.Time
	slow     bool
}

func (b *budgetEmbedder) Embed(string) ([]float32, error) {
	return nil, errors.New("must use EmbedContext")
}
func (b *budgetEmbedder) EmbedContext(ctx context.Context, _ string) ([]float32, error) {
	b.calls.Add(1)
	d, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("missing HTTP total deadline")
	}
	b.deadline <- d
	if b.slow {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	v := make([]float32, 64)
	v[0] = 1
	return v, nil
}
func TestWorkspaceWriteTotalBudgetAndInheritedDeadline(t *testing.T) {
	for _, path := range []string{"/api/v1/documents", "/upload", "/api/v1/knowledge/reindex-demo", "/reindex"} {
		for _, slow := range []bool{false, true} {
			t.Run(path+"/slow="+map[bool]string{false: "false", true: "true"}[slow], func(t *testing.T) {
				e, h := newWorkspaceHTTPTest(t)
				embed := &budgetEmbedder{deadline: make(chan time.Time, 20), slow: slow}
				h.Service.RAG = rag.New(store.NewMemoryVector(), embed)
				h.Service.RAG.SetEmbeddingSpace("test", 64)
				md := "# CPU\n## First\n" + strings.Repeat("first knowledge ", 80) + "\n## Second\n" + strings.Repeat("second knowledge ", 80)
				if len(rag.ChunkMarkdown("CPU", md)) < 2 {
					t.Fatal("fixture must have multiple chunks")
				}
				body, _ := json.Marshal(map[string]string{"title": "CPU", "content": md})
				if strings.Contains(path, "reindex") {
					h.DemoDir = t.TempDir()
					if err := os.WriteFile(filepath.Join(h.DemoDir, "cpu.md"), []byte(md), 0600); err != nil {
						t.Fatal(err)
					}
					body = []byte(`{"confirm":true}`)
				}
				req := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "budget-fixture")
				began := time.Now()
				parent := context.Background()
				cancel := func() {}
				if slow {
					parent, cancel = context.WithTimeout(parent, 2*time.Second)
				}
				defer cancel()
				req = req.WithContext(parent)
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				var result map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				var firstDeadline time.Time
				select {
				case firstDeadline = <-embed.deadline:
				default:
					t.Fatalf("request returned before first embedding: status=%d calls=%d body=%s", rec.Code, embed.calls.Load(), rec.Body.String())
				}
				if slow {
					parentDeadline, _ := parent.Deadline()
					if !firstDeadline.Equal(parentDeadline) {
						t.Fatal("embedding deadline did not inherit the shorter parent deadline")
					}
					if rec.Code != 504 || result["code"] != "timeout" || embed.calls.Load() != 1 {
						t.Fatalf("HTTP budget not visible status=%d calls=%d body=%s", rec.Code, embed.calls.Load(), rec.Body.String())
					}
					if time.Since(began) > 4*time.Second {
						t.Fatal("parent deadline not inherited")
					}
					var n int
					if err := h.Service.DB.SQL.QueryRow("SELECT count(*) FROM chunks").Scan(&n); err != nil || n != 0 {
						t.Fatalf("later chunks written n=%d err=%v", n, err)
					}
				} else {
					if rec.Code < 200 || rec.Code >= 300 {
						t.Fatalf("unexpected HTTP %d %s", rec.Code, rec.Body.String())
					}
					remaining := firstDeadline.Sub(began)
					if remaining < 29*time.Second || remaining > 31*time.Second {
						t.Fatalf("not 30s total budget %s", remaining)
					}
					for len(embed.deadline) > 0 {
						if d := <-embed.deadline; !d.Equal(firstDeadline) {
							t.Fatal("per-chunk timeout reset total budget")
						}
					}
				}
				t.Logf("L1 actual Gin HTTP path=%s slow=%v status=%d embed_calls=%d result=%s", path, slow, rec.Code, embed.calls.Load(), rec.Body.String())
			})
		}
	}
}
