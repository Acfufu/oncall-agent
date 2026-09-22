// Hybrid 稀疏路：内存 BM25 + RRF（v0.2）。仅标准库。
//
// CJK 分词选择：中文无空格分界，词典分词需外部依赖（任务禁用新依赖），
// 故用字二元组（bigram）近似词项：单字召回噪音大，三元组稀疏，
// 二元组在短 runbook 片段上 precision/recall 折中最好；ASCII/数字按
// 连续字母数字切英文词（小写）。查询与文档同此切分，保证项空间一致。
package rag

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// RRFK 为 RRF 融合常数 k（经验值 60）。
const RRFK = 60.0

// bm25K1 / bm25B 为 BM25 超参（经典取值）。
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// tokenize 中英混合切分：英文词 + CJK 字二元组，均小写归一。
func tokenize(s string) []string {
	s = strings.ToLower(s)
	var toks []string
	var latin strings.Builder
	var han []rune
	flushLatin := func() {
		if latin.Len() > 0 {
			toks = append(toks, latin.String())
			latin.Reset()
		}
	}
	flushHan := func() {
		for i := 0; i+1 < len(han); i++ {
			toks = append(toks, string(han[i:i+2]))
		}
		han = nil
	}
	for _, r := range s {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if len(han) > 0 {
				flushHan()
			}
			latin.WriteRune(r)
		case unicode.Is(unicode.Han, r):
			flushLatin()
			han = append(han, r)
		default:
			flushLatin()
			if len(han) > 0 {
				flushHan()
			}
		}
	}
	flushLatin()
	flushHan()
	return toks
}

// bm25Doc 为语料镜像条目：ID 与 AddDoc 写入 store 的 point ID 同公式。
type bm25Doc struct {
	Doc     string
	Title   string
	Snippet string
	tf      map[string]int
	length  int
}

// bm25Index 为内存 BM25 倒排（语料镜像）。AddDoc 同步写入，重加覆盖。
type bm25Index struct {
	mu     sync.RWMutex
	docs   map[string]*bm25Doc
	df     map[string]int
	totLen int
}

func newBM25Index() *bm25Index {
	return &bm25Index{docs: make(map[string]*bm25Doc), df: make(map[string]int)}
}

// upsert 写入/覆盖一条。text 取 title+"\n"+snippet，与稠密 embed 输入一致。
func (b *bm25Index) upsert(id, doc, title, snippet string) {
	toks := tokenize(title + "\n" + snippet)
	tf := make(map[string]int, len(toks))
	for _, t := range toks {
		tf[t]++
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.docs[id]; ok {
		for t := range old.tf {
			if b.df[t] <= 1 {
				delete(b.df, t)
			} else {
				b.df[t]--
			}
		}
		b.totLen -= old.length
	}
	for t := range tf {
		b.df[t]++
	}
	b.docs[id] = &bm25Doc{Doc: doc, Title: title, Snippet: snippet, tf: tf, length: len(toks)}
	b.totLen += len(toks)
}

// bm25Hit 为稀疏路命中。
type bm25Hit struct {
	ID    string
	Score float64
}

// search 取 BM25 前 topK。空库/空查询返回 nil。
func (b *bm25Index) search(query string, topK int) []bm25Hit {
	qtoks := tokenize(query)
	if len(qtoks) == 0 || topK <= 0 {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := len(b.docs)
	if n == 0 {
		return nil
	}
	avg := float64(b.totLen) / float64(n)
	if avg == 0 {
		return nil
	}
	qf := make(map[string]int, len(qtoks))
	for _, t := range qtoks {
		qf[t]++
	}
	hits := make([]bm25Hit, 0, n)
	for id, d := range b.docs {
		var s float64
		for t := range qf {
			tf, ok := d.tf[t]
			if !ok || tf == 0 {
				continue
			}
			df := b.df[t]
			idf := math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
			den := float64(tf) + bm25K1*(1-bm25B+bm25B*float64(d.length)/avg)
			s += idf * float64(tf) * (bm25K1 + 1) / den
		}
		if s > 0 {
			hits = append(hits, bm25Hit{ID: id, Score: s})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits
}

// rrfFuse 融合两路排名：score = Σ 1/(k+rank)，rank 从 1 起。
// 只出现在一路的 ID 按该路排名计分，不惩罚（缺席路贡献 0）。
func rrfFuse(denseIDs, bm25IDs []string, k float64) map[string]float64 {
	fused := make(map[string]float64, len(denseIDs)+len(bm25IDs))
	for i, id := range denseIDs {
		fused[id] += 1.0 / (k + float64(i+1))
	}
	for i, id := range bm25IDs {
		fused[id] += 1.0 / (k + float64(i+1))
	}
	return fused
}
