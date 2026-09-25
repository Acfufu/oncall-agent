// Package mcpserver 对外 MCP server（ADR-0007）：反向暴露三只读
// （time_now/rag_search/prometheus_query），白名单与只读语义不变——handler
// 直接桥接 tool.Deps.ExecWithContext，白名单外一律拒绝。传输双形态共用本
// Server：/mcp StreamableHTTP（挂 Gin）与 serve --mcp STDIO。
package mcpserver

import (
	"context"
	"net/http"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"oncall-agent/internal/rag"
	"oncall-agent/internal/tool"
)

// Version 随发版更新；MCP 客户端列表里可见。
const Version = "v0.5.0"

// New 装配 MCP server：tool.Definitions() 转 sdk Tool——InputSchema 必须
// type:object（go-sdk 对缺失 schema 直接 panic），现有 Parameters 天然满足。
func New(r *rag.RAG, promURL string) *sdkmcp.Server {
	deps := tool.NewDeps(r, promURL)
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "oncall-agent", Version: Version}, nil)
	for _, d := range tool.Definitions() {
		def := d
		srv.AddTool(&sdkmcp.Tool{
			Name:        def.Function.Name,
			Description: def.Function.Description,
			InputSchema: def.Function.Parameters,
		}, func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			out, _, err := deps.ExecWithContext(ctx, req.Params.Name, string(req.Params.Arguments))
			if err != nil {
				// 工具级错误（含白名单拒绝）打包进结果而非协议错误：
				// LLM 客户端能看到并自纠，连接不断。
				return &sdkmcp.CallToolResult{
					Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "error: " + err.Error()}},
					IsError: true,
				}, nil
			}
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: out}},
			}, nil
		})
	}
	return srv
}

// StreamableHTTPHandler 供 Gin 以 gin.WrapH 挂载（SDK 返回 http.Handler）。
func StreamableHTTPHandler(srv *sdkmcp.Server) http.Handler {
	return sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil)
}

// RunStdio 以 STDIO 传输阻塞运行（v1.8.0 无 ServeStdio 辅助）；日志走 stderr
// 不污染 stdout 协议通道。
func RunStdio(srv *sdkmcp.Server, ctx context.Context) error {
	return srv.Run(ctx, &sdkmcp.StdioTransport{})
}
