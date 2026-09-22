// Package rag 占位 embedding：接口 Embed(text) -> []float32，
// Ollama 调用为实装骨架（标准库 http），失败时回退确定性 Hash 向量，保证可跑。
package rag

import (
	"bytes"
	"encoding/json"
	"hash/fnv"
	"math"
	"net/http"
	"strings"
	"time"
)

// Dim 为占位向量维度，store.EnsureCollection 用同一值。
const Dim = 64

// Embedder 将文本映射为稠密向量。
type Embedder interface {
	Embed(text string) ([]float32, error)
}

// OllamaEmbedder 调本地 Ollama nomic-embed-text（/api/embeddings）。
type OllamaEmbedder struct {
	BaseURL string // 如 http://127.0.0.1:11434
	Model   string // 如 nomic-embed-text
	Client  *http.Client
}

// NewOllamaEmbedder 构造 Ollama embedder。
func NewOllamaEmbedder(baseURL, model string) *OllamaEmbedder {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	if model == "" {
		model = "nomic-embed-text"
	}
	return &OllamaEmbedder{BaseURL: strings.TrimRight(baseURL, "/"), Model: model, Client: &http.Client{Timeout: 10 * time.Second}}
}

// Embed 优先调 Ollama，失败回退 HashEmbed（保证离线可跑）。
func (o *OllamaEmbedder) Embed(text string) ([]float32, error) {
	vec, err := o.embedRemote(text)
	if err == nil && len(vec) > 0 {
		return vec, nil
	}
	return HashEmbed(text), nil
}

func (o *OllamaEmbedder) embedRemote(text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": o.Model, "prompt": text})
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Post(o.BaseURL+"/api/embeddings", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Embedding, nil
}

// HashEmbedder 纯离线确定性 embedder（测试 / 无 Ollama 环境）。
type HashEmbedder struct{}

func (HashEmbedder) Embed(text string) ([]float32, error) { return HashEmbed(text), nil }

// HashEmbed 确定性分桶向量 + L2 归一，空文本返回零向量。
func HashEmbed(text string) []float32 {
	vec := make([]float32, Dim)
	toks := strings.Fields(strings.ToLower(text))
	if len(toks) == 0 {
		return vec
	}
	for _, t := range toks {
		h := fnv.New32a()
		_, _ = h.Write([]byte(t))
		idx := int(h.Sum32() % uint32(Dim))
		vec[idx] += 1
		// 叠加字符级信息，缓解纯词桶碰撞。
		for _, r := range t {
			idx2 := (idx + int(r)) % Dim
			vec[idx2] += 0.1
		}
	}
	var n float64
	for _, v := range vec {
		n += float64(v) * float64(v)
	}
	if n == 0 {
		return vec
	}
	n = math.Sqrt(n)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / n)
	}
	return vec
}
