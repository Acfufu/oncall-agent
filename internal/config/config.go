package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
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
	Judge      JudgeConfig      `json:"judge"`
	Notify     NotifyConfig     `json:"notify"`
	Deploy     DeployConfig     `json:"deploy"`
	Reports    ReportsConfig    `json:"reports"`
	Storage    StorageConfig    `json:"storage"`
	Auth       AuthConfig       `json:"auth"`
	UI         UIConfig         `json:"ui"`
	Topology   FileConfig       `json:"topology"`
	Metrics    MetricsConfig    `json:"metrics"`
}

// ReportsConfig 报告环持久化旋钮（v0.8/ADR-0010）：persist_path 空=关闭，
// 默认 data/reports.json。
type StorageConfig struct {
	SQLitePath string `json:"sqlite_path"`
}
type AuthConfig struct {
	ConsoleTokenEnv string `json:"console_token_env"`
	WebhookTokenEnv string `json:"webhook_token_env"`
	LocalhostHTTP   bool   `json:"localhost_http"`
}
type UIConfig struct {
	Mode          string `json:"mode"`
	LegacyEnabled bool   `json:"legacy_enabled"`
	LegacyDefault bool   `json:"legacy_default"`
}
type FileConfig struct {
	File string `json:"file"`
}
type MetricsConfig struct {
	TemplatesFile string `json:"templates_file"`
}
type ReportsConfig struct {
	PersistPath string `json:"persist_path"`
}

// QueueConfig 诊断队列旋钮（ADR-0006）：Redis 硬依赖，asynq 诊断队列地址。
// 默认 127.0.0.1 而非 localhost——macOS 上 localhost 常解析为 ::1，Docker
// Desktop 端口发布仅 IPv4，asynq 直连会拒（v0.5 验收实测）。
type QueueConfig struct {
	RedisAddr string `json:"redis_addr"`
	Namespace string `json:"namespace"`
	RedisDB   int    `json:"redis_db"`
}

// JudgeConfig 诊断自评分旋钮（ADR-0006）：低分阈值（1-5 分制，score<阈值
// 记 low_score）；judge 复用 openai 段的 LLM 配置，无独立模型项。
type JudgeConfig struct {
	LowThreshold int `json:"low_threshold"`
}

// NotifyConfig 通知写回旋钮（ADR-0008）：诊断终态（low_score 或 failed）报告
// POST 到的通用 webhook 地址。空 URL=通知关闭（零值即关，同 judge 阈值<=0 口径）。
type NotifyConfig struct {
	WebhookURL string `json:"webhook_url"`
}

// DeployConfig 变更富化旋钮（ADR-0009）：GitHub 只读变更源（commits+deployments）。
// 空 repo=deploy_events 工具不注册——白名单缩回三只读、ReAct 提示词不出现、
// MCP 不暴露（零值即关，同 notify 空 URL 口径）；token 可选，public 仓库匿名即可。
type DeployConfig struct {
	GitHubRepo  string `json:"github_repo"`
	GitHubToken string `json:"github_token"`
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

// Default returns local defaults (ports 8819/6334/11434/:9090)。Host 仅绑
// 回环：无鉴权 API 与全接口暴露不相容（F05），对外服务需显式改 host。
// OpenAI key/model intentionally empty: caller must supply via file or env.
func Default() Config {
	return Config{
		Server:     ServerConfig{Host: "127.0.0.1", Port: 8819},
		OpenAI:     OpenAIConfig{APIBase: "https://api.openai.com/v1"},
		Qdrant:     QdrantConfig{Host: "127.0.0.1", Port: 6334, Collection: "oncallagent_workspace_v1"},
		Embedder:   EmbedderConfig{Host: "127.0.0.1", Port: 11434, Model: "nomic-embed-text"},
		Prometheus: PrometheusConfig{URL: "http://localhost:9090"},
		Knowledge:  KnowledgeConfig{AutoIngest: false, IncidentWeight: 0.5},
		Queue:      QueueConfig{RedisAddr: "127.0.0.1:6379", Namespace: "workspace"},
		Judge:      JudgeConfig{LowThreshold: 3},
		Notify:     NotifyConfig{}, // webhook_url 空=关闭（ADR-0008）
		Deploy:     DeployConfig{}, // github_repo 空=deploy_events 不注册（ADR-0009）
		Metrics:    MetricsConfig{TemplatesFile: "config/metric_templates.json"},
		Storage:    StorageConfig{SQLitePath: "data/oncall-agent.sqlite"},
		Auth:       AuthConfig{ConsoleTokenEnv: "ONCALL_CONSOLE_TOKEN", WebhookTokenEnv: "ONCALL_WEBHOOK_TOKEN"},
		UI:         UIConfig{Mode: "live", LegacyEnabled: true},
		Reports:    ReportsConfig{PersistPath: "data/reports.json"}, // 空串=关闭（ADR-0010）
	}
}

// Load reads JSON config from path (default config/config.json when empty),
// applies OPENAI_API_KEY env override, and errors when key missing.
func Load(path string) (*Config, error) {
	cfg, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	if isMissingKey(cfg.OpenAI.APIKey) {
		return nil, fmt.Errorf("openai.api_key missing: fill config/config.json or set OPENAI_API_KEY")
	}
	return cfg, nil
}

// LoadMCP 为 serve --mcp 模式读配置（ADR-0007）：MCP server 只暴露三只读
// 查询工具、不碰 LLM，故不校验 api_key；其余语义同 Load。
func LoadMCP(path string) (*Config, error) {
	return loadFile(path)
}

func loadFile(path string) (*Config, error) {
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
	// Explicit container/test overrides do not rewrite the user's configuration file.
	for _, override := range []struct {
		name   string
		target *string
	}{
		{"ONCALL_SERVER_HOST", &cfg.Server.Host}, {"ONCALL_SQLITE_PATH", &cfg.Storage.SQLitePath},
		{"ONCALL_REDIS_ADDR", &cfg.Queue.RedisAddr}, {"ONCALL_QUEUE_NAMESPACE", &cfg.Queue.Namespace},
		{"ONCALL_QDRANT_HOST", &cfg.Qdrant.Host}, {"ONCALL_QDRANT_COLLECTION", &cfg.Qdrant.Collection},
		{"ONCALL_EMBEDDER_HOST", &cfg.Embedder.Host}, {"ONCALL_PROMETHEUS_URL", &cfg.Prometheus.URL},
		{"ONCALL_TOPOLOGY_FILE", &cfg.Topology.File}, {"ONCALL_METRICS_TEMPLATES_FILE", &cfg.Metrics.TemplatesFile},
	} {
		if v := strings.TrimSpace(os.Getenv(override.name)); v != "" {
			*override.target = v
		}
	}
	if v := os.Getenv("ONCALL_LOCALHOST_HTTP"); v != "" {
		b, e := strconv.ParseBool(v)
		if e != nil {
			return nil, fmt.Errorf("invalid ONCALL_LOCALHOST_HTTP")
		}
		cfg.Auth.LocalhostHTTP = b
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8819
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
