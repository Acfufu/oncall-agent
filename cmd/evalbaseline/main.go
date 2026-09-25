// Command evalbaseline 以检索层直调跑 sample.jsonl 基线：recall@3 + 拒答率。
// 不经 LLM，数秒出结果。expect_doc 为文件名，映射到 demo md 首个一级标题。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/config"
	"oncall-agent/internal/judge"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"oncall-agent/internal/tool"
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
	// 生成层拒答回归（v0.3）：EVAL_GEN=1 时对负例（expect_doc 为空）走一次
	// LLM 诊断判定——引用不相关必须明示“未找到相关匹配”。消耗 LLM token，
	// 默认关。拒答语义已证伪检索层 Floor（7库无可分界），验收挂生成层。
	genOn := strings.TrimSpace(os.Getenv("EVAL_GEN")) == "1"
	// 告警驱动诊断 eval（v0.4，ADR-0005）：EVAL_ALERT=1 切入 fixture 告警集，
	// 走与 POST /alert→Planner.PlanPushed 同参链（query=alertname+description，
	// topK=3），规则断言引用命中 expect_doc；负例拒答仍挂 EVAL_GEN。
	alertOn := strings.TrimSpace(os.Getenv("EVAL_ALERT")) == "1"
	if alertOn {
		runAlertEval(cfg, r, genOn)
		return
	}
	// judge 评分 eval（v0.5，ADR-0006）：EVAL_JUDGE=1 对 alert fixture 正例逐条
	// PlanPushed 出诊断→judge 1-5 打分（纯观察值）。每正例消耗 2 次 LLM 调用
	// （诊断+评分），默认关。
	judgeOn := strings.TrimSpace(os.Getenv("EVAL_JUDGE")) == "1"
	if judgeOn {
		runJudgeEval(cfg, r)
		return
	}
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
	genRefused, genTotal, genErr := 0, 0, 0
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
			genNote := "off"
			if genOn {
				genTotal++
				refused, reply, gerr := genRefusal(cfg, q.Question, hits)
				switch {
				case gerr != nil:
					genErr++
					msg := gerr.Error()
					if len(msg) > 60 {
						msg = msg[:60] + "…"
					}
					genNote = "err:" + msg
				case refused:
					genRefused++
					genNote = "refused"
				default:
					genNote = "leak:" + truncStr(reply, 60)
				}
			}
			fmt.Printf("REFUSE q=%.30s hits=%d top=%.4f gen=%s\n", q.Question, len(hits), topScore(hits), genNote)
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
		"gen_refusal_rate":    safeDiv(genRefused, genTotal),
		"gen_refused":         genRefused,
		"gen_total":           genTotal,
		"gen_err":             genErr,
	})
	fmt.Println(string(out))
}

// alertSample 为 fixture 告警：eval-data/datasets/alerts.jsonl，一行一条。
// expect_doc 空 = 负例（库外告警，须明示无匹配，不硬凑）。
type alertSample struct {
	AlertName   string `json:"alertname"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	ExpectDoc   string `json:"expect_doc"`
}

// runAlertEval 告警驱动诊断 eval（v0.4，ADR-0005）。检索参同
// Planner.diagnose：query=alertname+" "+description，topK=3，与线上
// /alert 同链同参。跑前按 source 清 incident 沉淀——否则 fixture 告警
// 命中上次沉淀、引用自己的报告，eval 自证失真。
func runAlertEval(cfg *config.Config, r *rag.RAG, genOn bool) {
	if err := r.DeleteSource("incident"); err != nil {
		log.Printf("warn: clear incident source before alert eval: %v", err)
	}
	f, err := os.Open("eval-data/datasets/alerts.jsonl")
	if err != nil {
		log.Fatalf("open alert dataset: %v", err)
	}
	defer f.Close()

	hit, total := 0, 0
	genRefused, genTotal, genErr := 0, 0, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var a alertSample
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			log.Fatalf("parse alert dataset: %v", err)
		}
		query := strings.TrimSpace(a.AlertName + " " + a.Description)
		hits, err := r.Search(query, 3)
		if err != nil {
			log.Fatalf("alert search: %v", err)
		}
		if a.ExpectDoc == "" {
			// 负例：检索层必有余弦命中（Floor=0），拒答验收挂生成层（v0.3 同语义）。
			note := "off"
			if genOn {
				genTotal++
				refused, reply, gerr := genRefusal(cfg, query, hits)
				switch {
				case gerr != nil:
					genErr++
					msg := gerr.Error()
					if len(msg) > 60 {
						msg = msg[:60] + "…"
					}
					note = "err:" + msg
				case refused:
					genRefused++
					note = "refused"
				default:
					note = "leak:" + truncStr(reply, 60)
				}
			}
			fmt.Printf("ALERT_REFUSE %.30s hits=%d top=%.4f gen=%s\n", query, len(hits), topOf(hits), note)
			continue
		}
		total++
		want := docTitle("aiops-docs-demo", a.ExpectDoc)
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
		fmt.Printf("ALERT_HIT=%v want=%.16s alert=%.30s top=%.4f\n", ok, want, query, topOf(hits))
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("scan alert dataset: %v", err)
	}
	safeDiv := func(a, b int) float64 {
		if b == 0 {
			return 0
		}
		return float64(a) / float64(b)
	}
	out, _ := json.Marshal(map[string]any{
		"alert_recall_at_3": safeDiv(hit, total),
		"alert_hit":         hit,
		"alert_total":       total,
		"alert_gen_refused": genRefused,
		"alert_gen_total":   genTotal,
		"alert_gen_err":     genErr,
	})
	fmt.Println(string(out))
}

// runJudgeEval judge 评分 eval（v0.5，ADR-0006）：对 alerts.jsonl 正例逐条
// PlanPushed 出诊断（与线上 /alert worker 同链同参）→ judge.Score 1-5 打分。
// 跑前先按 source 清 incident 沉淀防自证循环（同 runAlertEval：否则 fixture
// 告警命中上次沉淀、judge 给自指报告打分）。评分失败计 err，不计入均分。
func runJudgeEval(cfg *config.Config, r *rag.RAG) {
	if err := r.DeleteSource("incident"); err != nil {
		log.Printf("warn: clear incident source before judge eval: %v", err)
	}
	f, err := os.Open("eval-data/datasets/alerts.jsonl")
	if err != nil {
		log.Fatalf("open alert dataset: %v", err)
	}
	defer f.Close()

	threshold := cfg.Judge.LowThreshold
	if threshold <= 0 {
		threshold = 3
	}
	ctx := context.Background()
	planner := agent.New(nil, r)
	sum, n, lowN, errN := 0, 0, 0, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var a alertSample
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			log.Fatalf("parse alert dataset: %v", err)
		}
		if a.ExpectDoc == "" {
			continue // 负例不评分：无匹配诊断没有质量可言
		}
		alerts := []tool.Alert{{Name: a.AlertName, Severity: a.Severity, Description: a.Description}}
		diagnosis, citations := planner.PlanPushed(ctx, alerts)
		score, reason, jerr := judge.Score(ctx, cfg.OpenAI, diagnosis, citations)
		if jerr != nil {
			errN++
			msg := jerr.Error()
			if len(msg) > 60 {
				msg = msg[:60] + "…"
			}
			fmt.Printf("JUDGE %.30s err=%.60s\n", a.AlertName, msg)
			continue
		}
		n++
		sum += score
		lowMark := ""
		if score < threshold {
			lowN++
			lowMark = " LOW"
		}
		fmt.Printf("JUDGE %.30s score=%d/5%s reason=%.40s\n", a.AlertName, score, lowMark, reason)
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("scan alert dataset: %v", err)
	}
	safeDiv := func(a, b int) float64 {
		if b == 0 {
			return 0
		}
		return float64(a) / float64(b)
	}
	out, _ := json.Marshal(map[string]any{
		"alert_judge_avg":           safeDiv(sum, n),
		"alert_judge_total":         n,
		"alert_judge_low_count":     lowN,
		"alert_judge_low_threshold": threshold,
		"alert_judge_err":           errN,
	})
	fmt.Println(string(out))
}

func topOf(rs []rag.Result) float32 {
	if len(rs) == 0 {
		return 0
	}
	return rs[0].Score
}

// genRefusal 生成层拒答判定：把检索引用原样喂给 LLM 诊断，回复含
// “未找到相关匹配”视为拒答成功（与 react 诊断语义同源）。
func genRefusal(cfg *config.Config, question string, hits []rag.Result) (bool, string, error) {
	var sb strings.Builder
	sb.WriteString("你是运维诊断助手（只读）。仅可依据下列知识库引用作答；引用与问题不相关或不足以支撑诊断时，必须明示“未找到相关匹配”，不得编造处置步骤，不得硬凑不相关引用作答。\n\n问题：" + question + "\n")
	if len(hits) == 0 {
		sb.WriteString("\n知识库引用：（检索无结果）\n")
	}
	for i, h := range hits {
		fmt.Fprintf(&sb, "\n%d.【%s】%s\n", i+1, h.Doc, truncStr(h.Snippet, 300))
	}
	body, _ := json.Marshal(map[string]any{
		"model": cfg.OpenAI.Model,
		"messages": []map[string]string{
			{"role": "user", "content": sb.String()},
		},
		"temperature": 0,
		"max_tokens":  256,
	})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.OpenAI.APIBase, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(cfg.OpenAI.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.OpenAI.APIKey))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	var rsp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&rsp); err != nil {
		return false, "", err
	}
	if len(rsp.Choices) == 0 {
		return false, "", fmt.Errorf("empty choices")
	}
	reply := rsp.Choices[0].Message.Content
	return strings.Contains(reply, "未找到相关匹配"), reply, nil
}

func truncStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
