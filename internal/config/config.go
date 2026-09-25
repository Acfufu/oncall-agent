package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config mirrors config/config_template.json. All JSON tags lowercase per API v0.1.
type Config struct {
	Server     ServerConfig     `json:"server"`
	OpenAI     OpenAIConfig     `json:"openai"`
	Qdrant     QdrantConfig     `json:"qdrant"`
	Embedder   EmbedderConfig   `json:"embedder"`
	Prometheus PrometheusConfig `json:"prometheus"`
	Knowledge  KnowledgeConfig  `json:"knowledge"`
	Queue      QueueConfig      `json:"queue"`
}

// QueueConfig 诊断队列旋钮（ADR-0006）：Redis 硬依赖，asynq 诊断队列地址。
type QueueConfig struct {
	RedisAddr string `json:"redis_addr"`
}

// KnowledgeConfig 事件沉淀旋钮（ADR-0005）：auto_ingest 自动入库开关，
// incident_weight 检索降权系数（缺省 true/0.5）。
type KnowledgeConfig struct {
	AutoIngest     bool    `json:"auto_ingest"`
	IncidentWeight float32 `json:"incident_weight"`
}

type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type OpenAIConfig struct {
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	APIBase string `json:"api_base"`
}

type QdrantConfig struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Collection string `json:"collection"`
}

type EmbedderConfig struct {
	Host  string `json:"host"`
	Port  int    `json:"port"`
	Model string `json:"model"`
}

type PrometheusConfig struct {
	URL string `json:"url"`
}

// Default returns v0.1 local defaults (ports 8819/6334/11434/:9090).
// OpenAI key/model intentionally empty: caller must supply via file or env.
func Default() Config {
	return Config{
		Server:     ServerConfig{Host: "0.0.0.0", Port: 8819},
		OpenAI:     OpenAIConfig{APIBase: "https://api.openai.com/v1"},
		Qdrant:     QdrantConfig{Host: "127.0.0.1", Port: 6334, Collection: "oncallagent"},
		Embedder:   EmbedderConfig{Host: "127.0.0.1", Port: 11434, Model: "nomic-embed-text"},
		Prometheus: PrometheusConfig{URL: "http://localhost:9090"},
		Knowledge:  KnowledgeConfig{AutoIngest: true, IncidentWeight: 0.5},
		Queue:      QueueConfig{RedisAddr: "localhost:6379"},
	}
}

// Load reads JSON config from path (default config/config.json when empty),
// applies OPENAI_API_KEY env override, and errors when key missing.
func Load(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		path = "config/config.json"
	}
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if v := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); v != "" {
		cfg.OpenAI.APIKey = v
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8819
	}
	if isMissingKey(cfg.OpenAI.APIKey) {
		return nil, fmt.Errorf("openai.api_key missing: fill config/config.json or set OPENAI_API_KEY")
	}
	return &cfg, nil
}

func isMissingKey(k string) bool {
	s := strings.TrimSpace(k)
	if s == "" {
		return true
	}
	switch strings.ToLower(s) {
	case "your-key", "your_key", "your-key-here", "your_key_here":
		return true
	}
	return false
}
