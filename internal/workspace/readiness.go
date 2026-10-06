package workspace

import (
	"context"
	"errors"
	"fmt"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/store"
)

var ErrProbeBusy = errors.New("retrieval projection publication in progress")

// ProbeRetrieval validates the actual remote embedding and vector space. It never resets collections.
// identity is the configured provider/model identity; dimensions are always part of the space ID.
func (s *Service) ProbeRetrieval(ctx context.Context, vectors *store.VectorStore, embed rag.Embedder, identity string) error {
	if !s.knowledgeMu.TryLock() {
		return ErrProbeBusy
	}
	defer s.knowledgeMu.Unlock()
	existing := s.SpaceID
	v, err := rag.EmbedContext(ctx, embed, "dimension probe")
	if err != nil {
		return err
	}
	space := hash(fmt.Sprintf("space-v1:%s:%d", identity, len(v)))
	if existing != space && existing != "" && existing != "unavailable" {
		return fmt.Errorf("configured retrieval space changed: %w", ErrConflict)
	}
	if err = vectors.EnsureCompatible(ctx, len(v)); err != nil {
		return err
	}
	if err = vectors.EnsureSpace(ctx, space); err != nil {
		return err
	}
	if existing == space {
		return nil
	}
	s.SpaceID = space
	s.RAG.SetEmbeddingSpace(space, len(v))
	if err = s.RestoreProjection(ctx); err != nil {
		s.SpaceID = existing
		s.RAG.SetEmbeddingSpace(existing, 0)
		return err
	}
	return nil
}
