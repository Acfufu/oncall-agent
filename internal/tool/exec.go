package tool

import "oncall-agent/internal/rag"

// Deps 注入三工具运行时依赖（不改 store/rag/config 结构，只读使用）。
type Deps struct {
	RAG     *RAGDeps
	Prom    *PromDeps
	PromURL string
}

// NewDeps 由 RAG + Prometheus URL 构造依赖。
func NewDeps(r *rag.RAG, promURL string) *Deps {
	return &Deps{RAG: &RAGDeps{RAG: r}, Prom: &PromDeps{URL: promURL}, PromURL: promURL}
}

// Exec 分发白名单工具调用，白名单外拒绝。返回工具结果文本。
// ragHits 非空时为本次 rag_search 命中（调用方收集引用）。
func (d *Deps) Exec(name, argsJSON string) (string, []rag.Result, error) {
	if !IsAllowed(name) {
		return "", nil, errDeny(name)
	}
	switch name {
	case "time_now":
		return TimeNow(), nil, nil
	case "rag_search":
		if d == nil || d.RAG == nil {
			rd := &RAGDeps{}
			return rd.RagSearch(argsJSON)
		}
		return d.RAG.RagSearch(argsJSON)
	case "prometheus_query":
		p := &PromDeps{URL: d.PromURL}
		if d != nil && d.Prom != nil {
			p = d.Prom
		}
		out, err := p.PromQuery(argsJSON)
		return out, nil, err
	default:
		return "", nil, errDeny(name)
	}
}
