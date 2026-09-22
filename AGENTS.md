# AGENTS.md — oncall-agent v0.1

Go 1.25 + Gin + CloudWeGo Eino. OpenAI兼容 + Qdrant + Prometheus. 只读闭环优先。

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

- `cmd/server/main.go` 入口，Gin路由组装
- `internal/` 分层：`config/` `handler/` `agent/` `rag/` `store/` `tool/` (新建按此来)
- `config/config_template.json` 唯一配置模板，不提交`config.json`
- `aiops-docs-demo/` demo知识，一篇一故障，标题即故障名
- `eval-data/datasets/sample.jsonl` 评测集，`{question,expect_doc,severity}`
- `docs/adr/0001-0003` 选型锁死，改需先改ADR
- `prometheus/prometheus.yml` 本地抓取配置

## 约定

- API v0.1：`GET /ping` `POST /upload` `POST /chat` `GET /plan`。全小写JSON，错误`{"error":"..."}`。
- Agent：ReAct（time/rag/prometheus只读）+ Plan-Execute（拉告警→检索→报告）。写操作禁入v0.1。
- RAG：Qdrant单路稠密召回 + 标题加权。无Hybrid/Rerank（v0.2）。
- 诊断必须带引用片段，无匹配明示无匹配，不编造。见CONTEXT.md。
- 配置经`api_base+model+key`，默认OpenAI协议。Embedding本地Ollama `nomic-embed-text`优先。
- 前端极简单页即可：聊天+诊断按钮+引用展示，不堆控制台（v0.2）。
- 沙箱默认关。告警只读，不确认不静默。

## 改代码前

- 读`CONTEXT.md`术语 + `docs/ROADMAP.md`分期 +对应ADR。
- v0.1外需求（Hybrid/控制台/企业库/完整评测）直接拒，标v0.2+。
- 新增工具必须只读，写工具需ADR批准。

## 验证

- `gofmt -l .` 干净，`go vet ./...` 过，`go run ./cmd/server` + `curl /ping` 通。
- 入库→检索链：upload一篇demo md，chat问出引用片段。
- commit用semantic：`feat:` `fix:` `docs:` `chore:`，原子提交。
