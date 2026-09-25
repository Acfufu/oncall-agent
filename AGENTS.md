# AGENTS.md — oncall-agent（v0.2 落地中）

Go 1.25 + Gin + CloudWeGo Eino. OpenAI兼容 + Qdrant + Prometheus + OTel. 只读闭环优先。

## 跑

```bash
cp config/config_template.json config/config.json  # 填LLM Key，唯一必填
cp .env.example .env
docker compose up -d
go mod tidy && go run ./cmd/server  # :8819
make check  # curl /ping
```

端口：App 8819 / Prometheus 9090 / Qdrant 6333+6334。

## 结构

- `cmd/server/main.go` 入口，Gin路由组装；`cmd/evalbaseline/` 评测基线入口
- `internal/` 分层：`config/` `handler/` `agent/` `rag/` `store/` `tool/` `observability/` `trace/` (新建按此来)
  - `rag/`：主链 + bm25.go(Hybrid) + rerank.go + embed.go；`tool/`：只读白名单执行 + mcp.go 官方SDK传输缝
  - `observability/`：OTel provider（trace→Jaeger，metrics→/metrics）；`trace/`：轻量span封装，包外API冻结
- `config/config_template.json` 唯一配置模板，不提交`config.json`
- `aiops-docs-demo/` demo知识，一篇一故障，标题即故障名
- `eval-data/datasets/sample.jsonl` 评测集，`{question,expect_doc,severity}`
- `web/`：console.html 正式控制台挂 `/`，v0.1极简页留 `/v01`
- `docs/adr/0001-0004` 选型锁死，改需先改ADR
- `prometheus/prometheus.yml` 本地抓取配置

## 约定

- API：`GET /ping` `POST /upload` `POST /chat` `GET /plan` `GET /list` `DELETE /delete` `POST /reindex` `GET /metrics`。全小写JSON，错误`{"error":"..."}`。
- Agent：ReAct（time_now/rag_search/prometheus_query 三只读白名单，白名单外一律拒绝）+ Plan-Execute（拉告警→检索→报告）。
- MCP（ADR-0004）：官方 go-sdk 传输缝，STDIO/StreamableHTTP，失联回退本地直调；白名单与只读语义不变。
- OTel：otelgin 全链埋点，trace 走 OTLP gRPC→collector→Jaeger；metrics 由 Prometheus 直抓 `/metrics`（`/ping` `/metrics` 不建span）。
- RAG：Qdrant稠密召回+标题加权；Hybrid BM25+RRF、rerank（LLM-as-rerank + RerankFused护栏）已落地；余弦Floor门控默认关。
- 诊断必须带引用片段，无匹配明示无匹配，不编造。见CONTEXT.md。
- 配置经`api_base+model+key`，默认OpenAI协议。Embedding本地Ollama `nomic-embed-text`优先。
- 沙箱默认关。告警只读，不确认不静默。

## 改代码前

- 读`CONTEXT.md`术语 + `docs/ROADMAP.md`分期 +对应ADR。
- 未立项需求（企业库等）直接拒，标v0.3+，先补ROADMAP/ADR再动手。
- 新增工具必须只读，写工具需ADR批准。

## 验证

- `gofmt -l .` 干净，`go vet ./...` 过，`go run ./cmd/server` + `curl /ping` 通。
- 入库→检索链：upload一篇demo md，chat问出引用片段。
- 评测链：`go run ./cmd/evalbaseline` 出基线分。
- commit用semantic：`feat:` `fix:` `docs:` `chore:`，原子提交。
