package rag

import (
	"context"
	"strings"
	"unicode"
)

type environmentKey struct{}

func WithEnvironment(ctx context.Context, env string) context.Context {
	if env == "" {
		env = "unknown"
	}
	return context.WithValue(ctx, environmentKey{}, env)
}
func Environment(ctx context.Context) string {
	if v, ok := ctx.Value(environmentKey{}).(string); ok {
		return v
	}
	return "unknown"
}

// LexicalAnchor is a conservative observed overlap policy, not a semantic confidence score.
// Meaningful ASCII identifiers or two non-generic Chinese pairs must occur in the source text.
func LexicalAnchor(query, text string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	text = strings.ToLower(text)
	if query == "" {
		return false
	}
	if len([]rune(query)) >= 3 && strings.Contains(text, query) {
		return true
	}
	words := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	stop := map[string]bool{"what": true, "why": true, "how": true, "the": true, "this": true, "that": true, "please": true, "with": true, "does": true, "query": true, "question": true, "error": true, "问题": true, "故障": true, "怎么": true, "如何": true, "什么": true, "请问": true, "排查": true, "处理": true, "是否": true, "原因": true}
	pairs := map[string]bool{}
	for _, word := range words {
		if stop[word] {
			continue
		}
		ascii := true
		for _, r := range word {
			if r > 127 {
				ascii = false
				break
			}
		}
		if ascii && len(word) >= 3 && strings.Contains(text, word) {
			return true
		}
		if !ascii {
			rs := []rune(word)
			for i := 0; i+1 < len(rs); i++ {
				pair := string(rs[i : i+2])
				if !stop[pair] && strings.Contains(text, pair) {
					pairs[pair] = true
				}
			}
		}
	}
	return len(pairs) >= 2
}
