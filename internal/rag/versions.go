package rag

import (
	"context"
	"crypto/md5"
	"fmt"
	"oncall-agent/internal/store"
)

// VersionChunk is an immutable indexing projection, persisted by the fact store.
type VersionChunk struct {
	ID, DocID, VersionID, Title, Snippet, Source, SpaceID, Environment string
	Embedding                                                          []float32
}
type ActiveVersion struct{ DocID, VersionID, SpaceID string }

func (r *RAG) IndexVersion(ctx context.Context, docID, versionID, title, md, source, spaceID string) ([]VersionChunk, error) {
	if docID == "" || versionID == "" || spaceID == "" {
		return nil, fmt.Errorf("document, version and embedding space required")
	}
	cs := ChunkMarkdown(title, md)
	if len(cs) > 512 {
		return nil, fmt.Errorf("document exceeds 512 chunks")
	}
	out := make([]VersionChunk, 0, len(cs))
	for i, c := range cs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v, err := EmbedContext(ctx, r.embed, c.Title+"\n"+c.Snippet)
		if err != nil {
			return nil, err
		}
		r.projectionMu.RLock()
		err = r.validateSpace(spaceID, len(v))
		r.projectionMu.RUnlock()
		if err != nil {
			return nil, err
		}
		sum := md5.Sum([]byte(fmt.Sprintf("%s#%s#%d", docID, versionID, i)))
		id := fmt.Sprintf("%x", sum)
		vc := VersionChunk{ID: id, DocID: docID, VersionID: versionID, Title: title, Snippet: c.Snippet, Source: source, SpaceID: spaceID, Embedding: v}
		if err := r.store.UpsertWithContext(ctx, store.Point{ID: id, DocID: docID, VersionID: versionID, SpaceID: spaceID, Doc: title, Title: c.Title, Content: "【" + title + "】" + c.Snippet, Source: source, Embedding: v}); err != nil {
			return nil, err
		}
		out = append(out, vc)
	}
	r.RestoreChunks(out)
	return out, nil
}

// RestoreChunks does not activate staging chunks. Call ActivateVersions from the SQL snapshot.
func (r *RAG) RestoreChunks(cs []VersionChunk) {
	r.projectionMu.Lock()
	defer r.projectionMu.Unlock()
	for _, c := range cs {
		r.chunks[c.ID] = c
		r.bm.upsert(c.ID, c.Title, c.Title, c.Snippet, c.Source)
	}
}
func (r *RAG) ActivateVersions(vs []ActiveVersion) {
	r.projectionMu.Lock()
	defer r.projectionMu.Unlock()
	r.versioned = true
	r.active = make(map[string]ActiveVersion, len(vs))
	for _, v := range vs {
		r.active[v.DocID] = v
	}
}
func (r *RAG) eligible(id, source string) bool {
	if source == "incident" || source == "space_metadata" {
		return false
	}
	c, known := r.chunks[id]
	if !known {
		return !r.versioned
	}
	v, ok := r.active[c.DocID]
	return ok && v.VersionID == c.VersionID && v.SpaceID == c.SpaceID
}
func (r *RAG) currentFilter() map[string]any {
	f := map[string]any{"must_not": []map[string]any{{"key": "source", "match": map[string]any{"value": "incident"}}, {"key": "source", "match": map[string]any{"value": "space_metadata"}}}}
	if r.versioned {
		vs := make([]map[string]any, 0, len(r.active))
		for _, v := range r.active {
			vs = append(vs, map[string]any{"must": []map[string]any{{"key": "version_id", "match": map[string]any{"value": v.VersionID}}, {"key": "doc_id", "match": map[string]any{"value": v.DocID}}, {"key": "space_id", "match": map[string]any{"value": v.SpaceID}}}})
		}
		if len(vs) == 0 {
			vs = append(vs, map[string]any{"key": "version_id", "match": map[string]any{"value": "__no_active_version__"}})
		}
		f["should"] = vs
	} else {
		f["must"] = []map[string]any{{"key": "version_id", "match": map[string]any{"value": ""}}}
	}
	return f
}
func (r *RAG) SearchWithContext(ctx context.Context, query string, topK int) ([]Result, error) {
	if topK <= 0 {
		topK = 5
	}
	return r.searchFusedContext(ctx, query, topK*2, topK)
}
func (r *RAG) SearchPoolWithContext(ctx context.Context, query string, poolN int) ([]Result, error) {
	if poolN <= 0 {
		poolN = 8
	}
	return r.searchFusedContext(ctx, query, poolN*2, poolN)
}

func (r *RAG) SetEmbeddingSpace(spaceID string, dimension int) {
	r.projectionMu.Lock()
	defer r.projectionMu.Unlock()
	r.spaceID = spaceID
	r.dimension = dimension
}
func (r *RAG) validateSpace(spaceID string, dimension int) error {
	if r.spaceID != "" && r.spaceID != spaceID {
		return fmt.Errorf("embedding space mismatch")
	}
	if r.dimension > 0 && dimension != r.dimension {
		return fmt.Errorf("embedding dimension mismatch: expected=%d actual=%d", r.dimension, dimension)
	}
	return nil
}

// DeleteVersionWithContext removes only the disposable retrieval projection, not historical SQL facts.
func (r *RAG) DeleteVersionWithContext(ctx context.Context, versionID string) error {
	if err := r.store.DeleteByVersionWithContext(ctx, versionID); err != nil {
		return err
	}
	r.projectionMu.Lock()
	defer r.projectionMu.Unlock()
	r.bm.mu.Lock()
	defer r.bm.mu.Unlock()
	for id, c := range r.chunks {
		if c.VersionID == versionID {
			r.bm.removeLocked(id)
			delete(r.chunks, id)
		}
	}
	return nil
}
