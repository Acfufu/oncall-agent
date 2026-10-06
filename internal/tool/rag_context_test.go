package tool

import (
	"context"
	"errors"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"testing"
)

func TestMissingRAGIsUnavailable(t *testing.T) {
	_, _, err := (&RAGDeps{}).RagSearch(`{"query":"cpu"}`)
	if err == nil {
		t.Fatal("missing source returned empty success")
	}
}
func TestRagToolCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &RAGDeps{RAG: rag.New(store.NewMemoryVector(), rag.HashEmbedder{})}
	_, _, err := d.RagSearchWithContext(ctx, `{"query":"cpu"}`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel lost: %v", err)
	}
}
