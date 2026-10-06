package agent

import (
	"context"
	"errors"
	"fmt"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/tool"
	"strings"
)

// SafeReport is the deterministic domain result when this run has no qualified knowledge.
const SafeReport = "当前未找到可用于处置的合格知识，请人工研判。"

type toolFailure struct{ err error }

func (e *toolFailure) Error() string { return e.err.Error() }
func (e *toolFailure) Unwrap() error { return e.err }
func qualifiedHit(h rag.Result) bool {
	return strings.TrimSpace(h.DocID) != "" && strings.TrimSpace(h.VersionID) != "" && strings.TrimSpace(h.ChunkID) != "" && strings.TrimSpace(h.Snippet) != "" && (h.Source == "upload" || h.Source == "demo")
}
func citationFromHit(h rag.Result) Citation {
	return Citation{Doc: h.Doc, Snippet: h.Snippet, DocID: h.DocID, VersionID: h.VersionID, ChunkID: h.ChunkID, Source: h.Source}
}

// IsSourceUnavailable retains the private tool failure and original wrapped dependency error.
func IsSourceUnavailable(err error) bool {
	var failure *toolFailure
	return errors.As(err, &failure) || errors.Is(err, tool.ErrSourceUnavailable)
}

// EvidenceReport publishes only deterministic original excerpts. A citation never authorizes unrelated generated instructions.
func EvidenceReport(cites []Citation) string {
	qualified := []Citation{}
	for _, c := range cites {
		if qualifiedHit(rag.Result{DocID: c.DocID, VersionID: c.VersionID, ChunkID: c.ChunkID, Snippet: c.Snippet, Source: c.Source}) {
			qualified = append(qualified, c)
		}
	}
	if len(qualified) == 0 {
		return SafeReport
	}
	var b strings.Builder
	b.WriteString("根据知识库匹配到以下内容（本轮原文摘录，适用性与根因尚未核验，请人工研判）：\n")
	for i, c := range qualified {
		fmt.Fprintf(&b, "\n%d. 【%s】\n%s\n", i+1, c.Doc, trunc(c.Snippet, 500))
	}
	return b.String()
}

// Qualification uses the original user query, never a model-selected tool query.
func qualifiedQuery(ctx context.Context, query string, h rag.Result) bool {
	if !qualifiedHit(h) || !rag.LexicalAnchor(query, h.Doc+"\n"+h.Snippet) {
		return false
	}
	env := rag.Environment(ctx)
	return h.Environment == "" || h.Environment == "unknown" || h.Environment == "all" || h.Environment == env
}
