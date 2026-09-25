# ADR 0007 — 对外 MCP server 暴露（v0.5）

- Date: 2026-09-26
- Status: Accepted（2026-09-26 立项拷问拍板；传输缝承 ADR-0004，方向由 client 扩为双向）

## Context

ADR-0004 已把三只读工具 MCP client 化（官方 go-sdk，STDIO/StreamableHTTP 双传输 + Definitions manifest 缝）。v0.5 立项拍板反向暴露：让外部 MCP 客户端（Claude Desktop / MCP inspector / 平台）直接调 oncall-agent 的三只读，对标 k8sgpt `serve --mcp`。对外 API 面一旦有客户端依赖即难撤回，故立 ADR。

## Decision

- **双传输共享一套 handler**：`/mcp` StreamableHTTP 端点挂现有 Gin（服务器常驻场景、远程客户端）；`serve --mcp` STDIO 模式（本地客户端直连，「Claude Desktop 里连 oncall-agent」为分发招牌 demo）。tool handler 一套，Definitions manifest 缝复用，不新增发行物。
- **只暴露三只读**：time_now / rag_search / prometheus_query。不含诊断 agent 本身，不含任何写路径——白名单外一律拒绝的语义对 MCP 入口同样生效。
- **鉴权不新设**：与现有 API（/chat 全裸）口径一致，同为只读面；README known limitation 标注。
- **MCP server 进程不需要 LLM Key**：三工具只碰 Qdrant/Prometheus/时钟，STDIO 模式可在无 Key 环境跑。

## Alternatives

- 只上 /mcp HTTP 端点：面最小，但 Claude Desktop 本地 STDIO 玩法缺席，对标 k8sgpt 打折。
- 只上 STDIO 子命令：字面对标，但服务器常驻场景（AM 推告警已跑着）用不上。
- 独立 mcpserver 二进制：cmd/server 以子命令切模式即可复用组装与配置装载，拒。

## Consequences

- 两片：/mcp 端点+handler 桥接 / serve --mcp STDIO 模式。
- 验收关：MCP inspector / Claude Desktop 双传输连通；三只读工具列出；写调用被拒。
- 风险：无鉴权 /mcp 暴露 Qdrant/Prometheus 查询面（与 /chat 同级风险，文档标注）；go-sdk server 侧 API 随版本漂（同 client 侧既有风险）。
