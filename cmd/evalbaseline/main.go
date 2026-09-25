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
	"strconv"
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

	// 预热：同进程 AddDoc 填满 BM25 内存镜像（Qdrant upsert 按 ID 幂等）。
	entries, err := os.ReadDir("aiops-docs-demo")
	if err != nil {
		log.Fatalf("read demo dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join("aiops-docs-demo", e.Name()))
		if err != nil {
			log.Fatalf("read demo: %v", err)
		}
		title := strings.TrimSuffix(e.Name(), ".md")
		for _, ln := range strings.Split(string(b), "\n") {
			t := strings.TrimSpace(ln)
			if strings.HasPrefix(t, "# ") && strings.TrimSpace(strings.TrimPrefix(t, "# ")) != "" {
				title = strings.TrimSpace(strings.TrimPrefix(t, "# "))
				break
			}
		}
		if err := r.AddDoc(title, string(b), "demo"); err != nil {
			log.Fatalf("warmup: %v", err)
		}
	}

	f, err := os.Open("eval-data/datasets/sample.jsonl")
	if err != nil {
		log.Fatalf("open dataset: %v", err)
	}
	defer f.Close()

	// rerank 列（v0.2 试水，仅评测链路）：候选池→LLM rerank→top3→命中判定。
	// 环境覆盖：RERANK_BASE_URL / RERANK_MODEL / RERANK_POOL(默认8) / EVAL_LIMIT(>0 截断前N问，smoke用)。
	ranker := rag.NewRanker(os.Getenv("RERANK_BASE_URL"), os.Getenv("RERANK_MODEL"), "lm-studio")
	poolN := 8
	if v := strings.TrimSpace(os.Getenv("RERANK_POOL")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			poolN = n
		}
	}
	limit := 0
	if v := strings.TrimSpace(os.Getenv("EVAL_LIMIT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	noRerank := strings.TrimSpace(os.Getenv("EVAL_NORERANK")) == "1"
	if v := strings.TrimSpace(os.Getenv("EVAL_FLOOR")); v != "" {
		if f, err := strconv.ParseFloat(v, 32); err == nil && f > 0 {
			r.Floor = float32(f)
		}
	}
	topScore := func(rs []rag.Result) float32 {
		if len(rs) == 0 {
			return 0
		}
		return rs[0].Score
	}

	hit, n, refuseOK, refuseN := 0, 0, 0, 0
	rhit, rn, rerr := 0, 0, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	count := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if limit > 0 && count >= limit {
			break
		}
		count++
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
			fmt.Printf("REFUSE q=%.30s hits=%d top=%.4f\n", q.Question, len(hits), topScore(hits))
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
		// 第三列：候选池→rerank→与 hybrid 序 RRF 护栏融合→top3→命中判定。失败记 rerr，原序不炸。
		rok := false
		rnote := ""
		pool, err := r.SearchPool(q.Question, poolN)
		if err != nil {
			rerr++
			rnote = "pool_err"
		} else if noRerank {
			rnote = "skipped"
		} else {
			rr, rrkErr := ranker.Rerank(q.Question, pool, 0)
			if rrkErr != nil {
				rerr++
				msg := rrkErr.Error()
				if len(msg) > 80 {
					msg = msg[:80] + "…"
				}
				rnote = "rerank_fallback:" + msg
			}
			fused := rag.RerankFused(pool, rr, 3)
			rn++
			for _, h := range fused {
				if h.Doc == want {
					rok = true
					rhit++
					break
				}
			}
		}
		_ = rnote
		fmt.Printf("HIT=%v want=%.16s q=%.30s top=%.4f | RERANK_HIT=%v pool=%d %s\n", ok, want, q.Question, topScore(hits), rok, len(pool), rnote)
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("scan dataset: %v", err)
	}
	safeDiv := func(a, b int) float64 {
		if b == 0 {
			return 0
		}
		return float64(a) / float64(b)
	}
	out, _ := json.Marshal(map[string]any{
		"recall_at_3":         safeDiv(hit, n),
		"hit":                 hit,
		"total":               n,
		"refusal_rate":        safeDiv(refuseOK, refuseN),
		"refused":             refuseOK,
		"refuse_total":        refuseN,
		"rerank_recall_at_3":  safeDiv(rhit, rn),
		"rerank_hit":          rhit,
		"rerank_total":        rn,
		"rerank_fallback_err": rerr,
		"rerank_pool":         poolN,
	})
	fmt.Println(string(out))
}
