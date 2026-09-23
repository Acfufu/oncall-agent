# ADR 0004 — MCP/OTel范围（v0.3首刀）

- Date: 2026-09-22
- Status: Proposed（待批）

## Context

v0.2已关（recall@3=1.0，拒答接受0/2，控制台切console）。v0.3拍板：MCP/OTel优先，三只读全MCP化，Jaeger trace全套。沙箱受限执行另立ADR-0005。

## Decision

- MCP SDK锁官方 `modelcontextprotocol/go-sdk`，`mark3labs/mcp-go`仅存量兼容。
- 三只读全走MCP client：`prometheus_query`→官方prometheus-mcp（query/range_query/series/labels/targets/alerts+docs，只读，dangerous关），`rag_search`→docs/Qdrant MCP，`time_now`→MCP化但本地兜底。
- 接法：同机STDIO `CommandTransport`，远程StreamableHTTP，SSE不新用。自研`Exec`白名单保留作鉴权/熔断层，`Definitions`作manifest缝，`NewDeps/NewPromClient`作注入缝。Prom双头（Firing/Query）合一织入。
- OTel：trace经OTLP gRPC→collector→Jaeger（all-in-one:16686）；metrics经`/metrics`给Prometheus 9090直抓，不经collector。
- 埋点：`otelgin.Middleware`链首（滤/ping /metrics）；根span `ReAct.Run`/`Planner.Plan`，子span `Exec`单工具+`RAG.Search`+Prom HTTP；Eino手写`callbacks.Handler`（OnStart/End/Error包span，gen_ai语义打token/耗时）。采样dev Always，prod ParentBased 0.1。

## Consequences

- 四片：MCP client+Exec切流 / collector+Jaeger compose / Gin+Provider埋点 / Eino handler+metrics。
- 验收：16686见chat→rag→tool树；9090查到`http.server.request.duration`+`rag_hits_total`。
- 风险：Eino无原生OTel（手写handler随版本漂）；Prometheus MCP需单测只读；time走MCP纯为统一，延迟+1跳。
