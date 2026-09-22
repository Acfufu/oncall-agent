// Package store 封装 Qdrant HTTP(6333) 建 collection / 写 point / 查 topK。
// 无 Qdrant 可用时自动降级为内存 map，保证可跑可测（v0.1 单路稠密召回）。
package store

import (
	"bytes"
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

// Point 为 Qdrant 点结构：id/title/content/embedding。
type Point struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Embedding []float32 `json:"embedding"`
}

// ScoredPoint 为检索命中。
type ScoredPoint struct {
	Point Point   `json:"point"`
	Score float32 `json:"score"`
}

// Store 优先走 Qdrant HTTP，失败时走内存 fallback。
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

// NewMemoryVector 纯内存向量 Store（测试 / 无 Qdrant 环境）。
func NewMemoryVector() *VectorStore {
	s := NewVector("http://127.0.0.1:6333", DefaultCollection)
	s.memOnly = true
	return s
}

// IsMemOnly 是否处于内存 fallback 模式。
func (s *VectorStore) IsMemOnly() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memOnly
}

func (s *VectorStore) fallback() {
	s.mu.Lock()
	s.memOnly = true
	s.mu.Unlock()
}

func (s *VectorStore) doJSON(method, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, s.baseURL+path, &buf)
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

// EnsureCollection 建 collection（已存在则忽略），失败降级内存。
func (s *VectorStore) EnsureCollection(vectorSize int) error {
	if vectorSize <= 0 {
		vectorSize = 64
	}
	body := map[string]any{
		"vectors": map[string]any{"size": vectorSize, "distance": "Cosine"},
	}
	var out map[string]any
	err := s.doJSON(http.MethodPut, "/collections/"+s.collection, body, &out)
	if err != nil {
		// collection 已存在时 Qdrant 返回 409，视为成功。
		if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "409") {
			return nil
		}
		s.fallback()
		return nil
	}
	return nil
}

// Upsert 写 point，同时镜像一份到内存供 fallback 用。
func (s *VectorStore) Upsert(p Point) error {
	s.mu.Lock()
	if s.mem == nil {
		s.mem = make(map[string]Point)
	}
	s.mem[p.ID] = p
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
					"title":   p.Title,
					"content": p.Content,
				},
			},
		},
	}
	var out map[string]any
	if err := s.doJSON(http.MethodPut, "/collections/"+s.collection+"/points?wait=true", body, &out); err != nil {
		s.fallback()
		return nil
	}
	return nil
}

// Search 查 topK。Qdrant 不可用或查失败时走内存余弦；无匹配返回空，不编造。
func (s *VectorStore) Search(query []float32, topK int) ([]ScoredPoint, error) {
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
		return s.searchMem(query, topK), nil
	}

	body := map[string]any{
		"vector":       query,
		"limit":        topK,
		"with_payload": true,
	}
	var resp struct {
		Result []struct {
			ID      any            `json:"id"`
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
			Vector  []float32      `json:"vector"`
		} `json:"result"`
	}
	if err := s.doJSON(http.MethodPost, "/collections/"+s.collection+"/points/search", body, &resp); err != nil {
		s.fallback()
		return s.searchMem(query, topK), nil
	}
	if len(resp.Result) == 0 {
		return nil, nil
	}
	out := make([]ScoredPoint, 0, len(resp.Result))
	for _, r := range resp.Result {
		title, _ := r.Payload["title"].(string)
		content, _ := r.Payload["content"].(string)
		out = append(out, ScoredPoint{
			Point: Point{
				ID:        fmt.Sprintf("%v", r.ID),
				Title:     title,
				Content:   content,
				Embedding: r.Vector,
			},
			Score: r.Score,
		})
	}
	return out, nil
}

func (s *VectorStore) searchMem(query []float32, topK int) []ScoredPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.mem) == 0 {
		return nil
	}
	scored := make([]ScoredPoint, 0, len(s.mem))
	for _, p := range s.mem {
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
