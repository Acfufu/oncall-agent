package main

import (
	"oncall-agent/internal/store"
	"testing"
)

func TestEvalDefaultsToMemory(t *testing.T) {
	t.Setenv("EVAL_STORAGE", "")
	t.Setenv("EVAL_REMOTE", "")
	s, err := evalStore()
	if err != nil || !s.IsMemOnly() {
		t.Fatalf("default eval not isolated: %v", err)
	}
	if err := s.Upsert(store.Point{ID: "sentinel", Embedding: []float32{1}}); err != nil {
		t.Fatal(err)
	}
}
func TestEvalRefusesPersistentAndRemote(t *testing.T) {
	for _, mode := range []string{"persistent", "qdrant", "production"} {
		t.Setenv("EVAL_STORAGE", mode)
		if _, err := evalStore(); err == nil {
			t.Fatal("unowned persistent eval accepted")
		}
	}
	t.Setenv("EVAL_STORAGE", "memory")
	t.Setenv("EVAL_REMOTE", "1")
	if _, err := evalStore(); err == nil {
		t.Fatal("uncontrolled remote eval accepted")
	}
}
