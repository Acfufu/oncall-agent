package main

import (
	"context"
	"fmt"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
	"os"
	"strings"
)

// Default evaluation has no network or business configuration access. Persistent evaluation is deliberately unsupported until resource ownership provisioning exists.
func evalStore() (*store.VectorStore, error) {
	mode := strings.TrimSpace(os.Getenv("EVAL_STORAGE"))
	if mode != "" && mode != "memory" {
		return nil, fmt.Errorf("persistent evaluation refused: no owned-resource provisioner; business configuration is never loaded")
	}
	if os.Getenv("EVAL_REMOTE") == "1" {
		return nil, fmt.Errorf("remote legacy evaluation disabled; use isolated production-surface harness")
	}
	s := store.NewMemoryVector()
	if err := s.EnsureCompatible(context.Background(), rag.Dim); err != nil {
		return nil, err
	}
	return s, nil
}
