// Command evalbaseline 以检索层直调跑 sample.jsonl 基线：recall@3 + 拒答率。
// 不经 LLM，数秒出结果。expect_doc 为文件名，映射到 demo md 首个一级标题。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"oncall-agent/internal/config"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
)

type sample struct {
	Question  string `json:"question"`
	ExpectDoc string `json:"expect_doc"`
	Severity  string `json:"severity"`
}

func docTitle(demoDir, file string) string {
	if file == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(demoDir, file))
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "# ") && len(strings.TrimSpace(strings.TrimPrefix(t, "# "))) > 0 {
			return strings.TrimSpace(strings.TrimPrefix(t, "# "))
		}
	}
	return strings.TrimSuffix(file, ".md")
}

func main() {
	cfg, err := config.Load("config/config.json")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	httpPort := cfg.Qdrant.Port
	if httpPort == 6334 {
		httpPort = 6333
	}
	s := store.NewVectorFromHostPort(cfg.Qdrant.Host, httpPort, cfg.Qdrant.Collection)
	r := rag.New(s, rag.SelectEmbedder(cfg.Embedder.Host, cfg.Embedder.Port, cfg.Embedder.Model))

	f, err := os.Open("eval-data/datasets/sample.jsonl")
	if err != nil {
		log.Fatalf("open dataset: %v", err)
	}
	defer f.Close()

	hit, n, refuseOK, refuseN := 0, 0, 0, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var q sample
		if err := json.Unmarshal([]byte(line), &q); err != nil {
			log.Fatalf("parse dataset: %v", err)
		}
		hits, err := r.Search(q.Question, 3)
		if err != nil {
			log.Fatalf("search: %v", err)
		}
		if q.ExpectDoc == "" {
			refuseN++
			if len(hits) == 0 {
				refuseOK++
			}
			fmt.Printf("REFUSE q=%.30s hits=%d\n", q.Question, len(hits))
			continue
		}
		n++
		want := docTitle("aiops-docs-demo", q.ExpectDoc)
		ok := false
		for _, h := range hits {
			if h.Doc == want {
				ok = true
				break
			}
		}
		if ok {
			hit++
		}
		fmt.Printf("HIT=%v want=%.16s q=%.30s\n", ok, want, q.Question)
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("scan dataset: %v", err)
	}
	out, _ := json.Marshal(map[string]any{
		"recall_at_3":  float64(hit) / float64(n),
		"hit":          hit,
		"total":        n,
		"refusal_rate": float64(refuseOK) / float64(refuseN),
		"refused":      refuseOK,
		"refuse_total": refuseN,
	})
	fmt.Println(string(out))
}
