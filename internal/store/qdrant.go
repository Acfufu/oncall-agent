// Package store 封装 Qdrant HTTP(6333) 建 collection / 写 point / 查 topK。
// 持久模式失败显式返回；内存模式只能由测试显式构造。
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultCollection 与 config_template.json 保持一致。
const DefaultCollection = "oncallagent"

// Point 为 Qdrant 点结构：id/title/content/embedding + doc/source。
// Doc 为所属文档名（删除与 reindex 同步的键），Source 标记来源
// demo/upload（reindex 只清 demo，不伤上传文档）。
type Point struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Doc       string    `json:"doc"`
	Source    string    `json:"source"`
	Embedding []float32 `json:"embedding"`
	DocID     string    `json:"doc_id,omitempty"`
	VersionID string    `json:"version_id,omitempty"`
	SpaceID   string    `json:"space_id,omitempty"`
}

// ScoredPoint 为检索命中。
type ScoredPoint struct {
	Point Point   `json:"point"`
	Score float32 `json:"score"`
}

// VectorStore uses Qdrant in persistent mode, with no automatic memory fallback.
// 注：包内 memory 文档 Store 见 store.go；本类型为向量 Store，专供 rag 用。
type VectorStore struct {
	baseURL    string
	collection string
	client     *http.Client

	mu      sync.RWMutex
	mem     map[string]Point
	memOnly bool
}

// NewVector 以 Qdrant HTTP 地址构造，如 "http://127.0.0.1:6333"。
func NewVector(baseURL, collection string) *VectorStore {
	if collection == "" {
		collection = DefaultCollection
	}
	return &VectorStore{
		baseURL:    baseURL,
		collection: collection,
		client:     &http.Client{Timeout: 5 * time.Second},
		mem:        make(map[string]Point),
	}
}

// NewVectorFromHostPort 兼容 host+http端口构造（HTTP 默认 6333）。
func NewVectorFromHostPort(host string, port int, collection string) *VectorStore {
	if port == 0 {
		port = 6333
	}
	return NewVector(fmt.Sprintf("http://%s:%d", host, port), collection)
}

// NewMemoryVector explicitly constructs a test-only memory projection.
func NewMemoryVector() *VectorStore {
	s := NewVector("http://127.0.0.1:6333", DefaultCollection)
	s.memOnly = true
	return s
}

// IsMemOnly reports an explicitly configured in-memory store.
func (s *VectorStore) IsMemOnly() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memOnly
}

func (s *VectorStore) doJSON(method, path string, body any, out any) error {
	return s.doJSONContext(context.Background(), method, path, body, out)
}
func (s *VectorStore) doJSONContext(ctx context.Context, method, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant %s %s: %d %s", method, path, resp.StatusCode, string(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
	}
	return nil
}

// EnsureCollection is the compatibility wrapper for non-destructive dimension validation.
func (s *VectorStore) EnsureCollection(vectorSize int) error {
	return s.EnsureCompatible(context.Background(), vectorSize)
}

// VectorSize 查 collection 当前向量维度；不存在或内存模式返回 0。
func (s *VectorStore) VectorSize() (int, error) {
	if s.IsMemOnly() {
		return 0, fmt.Errorf("mem only")
	}
	var out struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors struct {
						Size int `json:"size"`
					} `json:"vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	if err := s.doJSON(http.MethodGet, "/collections/"+s.collection, nil, &out); err != nil {
		return 0, err
	}
	return out.Result.Config.Params.Vectors.Size, nil
}

// RecreateCollection refuses destructive legacy recreation.
func (s *VectorStore) RecreateCollection(vectorSize int) error {
	return fmt.Errorf("automatic collection recreation disabled; migrate into an owned new collection")
}

// Upsert writes to the configured projection and propagates persistent failure.
func (s *VectorStore) Upsert(p Point) error { return s.UpsertWithContext(context.Background(), p) }
func (s *VectorStore) UpsertWithContext(ctx context.Context, p Point) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.mem == nil {
		s.mem = make(map[string]Point)
	}
	if s.memOnly {
		s.mem[p.ID] = p
	}
	memOnly := s.memOnly
	s.mu.Unlock()

	if memOnly {
		return nil
	}
	body := map[string]any{
		"points": []map[string]any{
			{
				"id":     p.ID,
				"vector": p.Embedding,
				"payload": map[string]any{
					"chunk_id": p.ID,
					"title":    p.Title,
					"content":  p.Content,
					"doc":      p.Doc,
					"source":   p.Source,
					"doc_id":   p.DocID, "version_id": p.VersionID, "space_id": p.SpaceID,
				},
			},
		},
	}
	var out map[string]any
	if err := s.doJSONContext(ctx, http.MethodPut, "/collections/"+s.collection+"/points?wait=true", body, &out); err != nil {
		return err
	}
	return nil
}

// Search returns topK, propagating Qdrant failures.
func (s *VectorStore) Search(query []float32, topK int) ([]ScoredPoint, error) {
	return s.SearchWithContext(context.Background(), query, topK)
}
func (s *VectorStore) SearchWithContext(ctx context.Context, query []float32, topK int) ([]ScoredPoint, error) {
	return s.SearchFiltered(ctx, query, topK, nil)
}
func (s *VectorStore) SearchFiltered(ctx context.Context, query []float32, topK int, filter map[string]any) ([]ScoredPoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if topK <= 0 {
		topK = 5
	}
	if len(query) == 0 {
		return nil, nil
	}
	s.mu.RLock()
	memOnly := s.memOnly
	s.mu.RUnlock()
	if memOnly {
		return s.searchMemFiltered(query, topK, filter), nil
	}

	body := map[string]any{
		"vector":       query,
		"limit":        topK,
		"with_payload": true,
	}
	if filter != nil {
		body["filter"] = filter
	}
	var resp struct {
		Result []struct {
			ID      any            `json:"id"`
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
			Vector  []float32      `json:"vector"`
		} `json:"result"`
	}
	if err := s.doJSONContext(ctx, http.MethodPost, "/collections/"+s.collection+"/points/search", body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Result) == 0 {
		return nil, nil
	}
	out := make([]ScoredPoint, 0, len(resp.Result))
	for _, r := range resp.Result {
		title, _ := r.Payload["title"].(string)
		content, _ := r.Payload["content"].(string)
		doc, _ := r.Payload["doc"].(string)
		source, _ := r.Payload["source"].(string)
		id := payloadString(r.Payload, "chunk_id")
		if id == "" {
			id = fmt.Sprintf("%v", r.ID)
			if len(id) == 36 {
				id = strings.ReplaceAll(id, "-", "")
			}
		}

		out = append(out, ScoredPoint{
			Point: Point{
				ID:      id,
				Title:   title,
				Content: content,
				Doc:     doc,
				Source:  source,
				DocID:   payloadString(r.Payload, "doc_id"), VersionID: payloadString(r.Payload, "version_id"), SpaceID: payloadString(r.Payload, "space_id"),
				Embedding: r.Vector,
			},
			Score: r.Score,
		})
	}
	return out, nil
}

// deleteByFilter 按 payload 精确匹配删点。Qdrant 删失败返回错误（不降级内存：
// 删除必须如实报告，静默降级会假装删掉而向量仍在）；内存镜像同步清理。
func (s *VectorStore) deleteByFilter(key, value string) error {
	return s.deleteByFilterContext(context.Background(), key, value)
}
func (s *VectorStore) deleteByFilterContext(ctx context.Context, key, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	for id, p := range s.mem {
		if matchPoint(p, key, value) {
			delete(s.mem, id)
		}
	}
	memOnly := s.memOnly
	s.mu.Unlock()
	if memOnly {
		return nil
	}
	body := map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{"key": key, "match": map[string]any{"value": value}},
			},
		},
	}
	var out map[string]any
	return s.doJSONContext(ctx, http.MethodPost, "/collections/"+s.collection+"/points/delete?wait=true", body, &out)
}

func matchPoint(p Point, key, value string) bool {
	switch key {
	case "doc":
		return p.Doc == value
	case "source":
		return p.Source == value
	case "version_id":
		return p.VersionID == value
	}
	return false
}

// DeleteByDoc 删除某文档（payload.doc 精确匹配）的全部点。
// 旧数据（v0.3 前写入，payload 无 doc 字段）不命中，需 reindex 重建。
func (s *VectorStore) DeleteByDoc(doc string) error {
	if strings.TrimSpace(doc) == "" {
		return fmt.Errorf("doc required")
	}
	return s.deleteByFilter("doc", doc)
}

// DeleteBySource 删除某来源（demo/upload）的全部点，reindex 同步用。
func (s *VectorStore) DeleteBySource(source string) error {
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("source required")
	}
	return s.deleteByFilter("source", source)
}

func (s *VectorStore) searchMem(query []float32, topK int) []ScoredPoint {
	return s.searchMemFiltered(query, topK, nil)
}
func (s *VectorStore) searchMemFiltered(query []float32, topK int, filter map[string]any) []ScoredPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.mem) == 0 {
		return nil
	}
	scored := make([]ScoredPoint, 0, len(s.mem))
	for _, p := range s.mem {
		if !matchesFilter(p, filter) {
			continue
		}
		// R01：混维度守卫——embedder 降级/恢复过渡期内存镜像可能 64/768 混存，
		// cosine 截断到 min(len) 会产出假分数；维度不符的点直接跳过。
		if len(p.Embedding) != len(query) {
			continue
		}
		sim := cosine(query, p.Embedding)
		if sim <= 0 || math.IsNaN(float64(sim)) {
			continue
		}
		scored = append(scored, ScoredPoint{Point: p, Score: sim})
	}
	if len(scored) == 0 {
		return nil
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored
}

func cosine(a, b []float32) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

func payloadString(p map[string]any, key string) string { v, _ := p[key].(string); return v }

// EnsureCompatible never deletes an existing collection. Missing collections may be created.
func (s *VectorStore) EnsureCompatible(ctx context.Context, dim int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if dim <= 0 {
		return fmt.Errorf("embedding dimension required")
	}
	if s.IsMemOnly() {
		return nil
	}
	var out struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors struct {
						Size int `json:"size"`
					} `json:"vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	err := s.doJSONContext(ctx, http.MethodGet, "/collections/"+s.collection, nil, &out)
	if err != nil {
		if !strings.Contains(err.Error(), ": 404 ") {
			return err
		}
		var res map[string]any
		return s.doJSONContext(ctx, http.MethodPut, "/collections/"+s.collection, map[string]any{"vectors": map[string]any{"size": dim, "distance": "Cosine"}}, &res)
	}
	if out.Result.Config.Params.Vectors.Size != dim {
		return fmt.Errorf("embedding dimension mismatch: collection=%d configured=%d; migrate into a new collection", out.Result.Config.Params.Vectors.Size, dim)
	}
	return nil
}

// EnsureSpace stores a non-retrievable marker in the collection; an existing collection without a marker requires migration.
func (s *VectorStore) EnsureSpace(ctx context.Context, spaceID string) error {
	if spaceID == "" {
		return fmt.Errorf("embedding space required")
	}
	if s.IsMemOnly() {
		return nil
	}
	const marker = "00000000-0000-0000-0000-000000000001"
	var out struct {
		Result struct {
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	err := s.doJSONContext(ctx, http.MethodGet, "/collections/"+s.collection+"/points/"+marker+"?with_payload=true", nil, &out)
	if err == nil {
		if payloadString(out.Result.Payload, "space_id") != spaceID {
			return fmt.Errorf("embedding space mismatch; migrate into a new collection")
		}
		return nil
	}
	if !strings.Contains(err.Error(), ": 404 ") {
		return err
	}
	var count struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}
	if err := s.doJSONContext(ctx, http.MethodPost, "/collections/"+s.collection+"/points/count", map[string]any{"exact": true}, &count); err != nil {
		return err
	}
	if count.Result.Count != 0 {
		return fmt.Errorf("existing collection has no embedding space metadata; explicit migration required")
	}
	var size struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors struct {
						Size int `json:"size"`
					} `json:"vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	if err := s.doJSONContext(ctx, http.MethodGet, "/collections/"+s.collection, nil, &size); err != nil {
		return err
	}
	if size.Result.Config.Params.Vectors.Size <= 0 {
		return fmt.Errorf("invalid collection dimension")
	}
	return s.UpsertWithContext(ctx, Point{ID: marker, Source: "space_metadata", SpaceID: spaceID, Embedding: make([]float32, size.Result.Config.Params.Vectors.Size)})
}
func matchesFilter(p Point, f map[string]any) bool {
	if f == nil {
		return true
	}
	check := func(c map[string]any) bool {
		if _, ok := c["must"]; ok {
			return matchesFilter(p, c)
		}
		k, _ := c["key"].(string)
		m, _ := c["match"].(map[string]any)
		v, _ := m["value"].(string)
		var actual string
		switch k {
		case "source":
			actual = p.Source
		case "version_id":
			actual = p.VersionID
		case "space_id":
			actual = p.SpaceID
		case "doc_id":
			actual = p.DocID
		}
		return actual == v
	}
	if cs, ok := f["must_not"].([]map[string]any); ok {
		for _, c := range cs {
			if check(c) {
				return false
			}
		}
	}
	if cs, ok := f["must"].([]map[string]any); ok {
		for _, c := range cs {
			if !check(c) {
				return false
			}
		}
	}
	if cs, ok := f["should"].([]map[string]any); ok {
		for _, c := range cs {
			if check(c) {
				return true
			}
		}
		return false
	}
	return true
}

func (s *VectorStore) DeleteByDocWithContext(ctx context.Context, doc string) error {
	if strings.TrimSpace(doc) == "" {
		return fmt.Errorf("doc required")
	}
	return s.deleteByFilterContext(ctx, "doc", doc)
}
func (s *VectorStore) DeleteBySourceWithContext(ctx context.Context, source string) error {
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("source required")
	}
	return s.deleteByFilterContext(ctx, "source", source)
}

func (s *VectorStore) DeleteByVersionWithContext(ctx context.Context, versionID string) error {
	if strings.TrimSpace(versionID) == "" {
		return fmt.Errorf("version required")
	}
	return s.deleteByFilterContext(ctx, "version_id", versionID)
}
