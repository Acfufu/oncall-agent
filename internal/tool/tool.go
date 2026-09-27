// Package tool 只读工具白名单（v0.1）：time_now / rag_search / prometheus_query，
// v0.7 起可按 config 门控追加 deploy_events（ADR-0009）。
// 写操作禁入，无沙箱无流式。白名单外调用一律拒绝。
package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

// baseTools 三只读基础白名单；deploy_events 由 repo 门控追加（ADR-0009）。
var baseTools = []string{"time_now", "rag_search", "prometheus_query"}

// AllowedFor 返回 repo 配置下的白名单：repo 空=白名单缩回三只读（默认关，
// 非空=追加 deploy_events，工具不设 repo 参数——作用域在配置层锁死）。
func AllowedFor(repo string) []string {
	if strings.TrimSpace(repo) == "" {
		return baseTools
	}
	return append(append([]string(nil), baseTools...), "deploy_events")
}

// IsAllowed 白名单校验（repo 感知）。
func IsAllowed(repo, name string) bool {
	for _, a := range AllowedFor(repo) {
		if a == name {
			return true
		}
	}
	return false
}

// Definition 为 OpenAI chat/completions tools[i].function 结构。
type Definition struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

// FunctionSchema 为 function 定义。
type FunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// DefinitionsFor 返回 repo 配置下的 function-calling 定义：三只读 + repo 门控
// 的 deploy_events（MCP 暴露跟随本单一事实源，ADR-0009）。
func DefinitionsFor(repo string) []Definition {
	defs := []Definition{
		{
			Type: "function",
			Function: FunctionSchema{
				Name:        "time_now",
				Description: "返回当前服务器时间（只读）。无参数。",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		{
			Type: "function",
			Function: FunctionSchema{
				Name:        "rag_search",
				Description: "检索运维知识库 runbook（只读）。返回 {doc,snippet,score} 列表，无匹配返回空。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "检索问题"},
						"top_k": map[string]any{"type": "number", "description": "返回条数，默认 3"},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSchema{
				Name:        "prometheus_query",
				Description: "只读查询 Prometheus 即时向量（GET /api/v1/query）。不做确认/静默等写操作。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "PromQL，如 up 或 alert:xxx"},
					},
					"required": []string{"query"},
				},
			},
		},
	}
	if strings.TrimSpace(repo) == "" {
		return defs
	}
	return append(defs, Definition{
		Type: "function",
		Function: FunctionSchema{
			Name:        "deploy_events",
			Description: "只读拉取配置仓库最近变更（GitHub commits+deployments 合成时间线，v0.7 变更富化）。参数 since/until 可选（RFC3339，缺省最近 24 小时）；仓库由服务端配置锁定，不接受 repo 参数。不做任何写操作。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"since": map[string]any{"type": "string", "description": "起始时间 RFC3339，可选，缺省最近 24 小时"},
					"until": map[string]any{"type": "string", "description": "结束时间 RFC3339，可选，缺省现在"},
				},
			},
		},
	})
}

// errDeny 白名单外拒绝错误（文案随 repo 配置收缩，ADR-0009）。
func errDeny(repo, name string) error {
	return fmt.Errorf("tool %q not allowed (whitelist: %s)", name, strings.Join(AllowedFor(repo), ","))
}

// argsOf 解析工具参数 JSON。
func argsOf(raw string, v any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	return json.Unmarshal([]byte(raw), v)
}
