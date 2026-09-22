// Package tool 只读工具白名单（v0.1）：time_now / rag_search / prometheus_query。
// 写操作禁入，无沙箱无流式。白名单外调用一律拒绝。
package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Allowed 为工具白名单。
var Allowed = []string{"time_now", "rag_search", "prometheus_query"}

// IsAllowed 白名单校验。
func IsAllowed(name string) bool {
	for _, a := range Allowed {
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

// Definitions 返回三只读工具的 function-calling 定义。
func Definitions() []Definition {
	return []Definition{
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
}

// errDeny 白名单外拒绝错误。
func errDeny(name string) error {
	return fmt.Errorf("tool %q not allowed (whitelist: %s)", name, strings.Join(Allowed, ","))
}

// argsOf 解析工具参数 JSON。
func argsOf(raw string, v any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	return json.Unmarshal([]byte(raw), v)
}
