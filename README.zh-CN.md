# oncall-agent

**按 runbook 回答的值班代理，有引用可查——没有匹配就直说没有。**

告警进，带引用的诊断出：Go 服务把 Prometheus 告警接到 Hybrid 检索的
runbook 库，基于 OpenAI 兼容 LLM、Qdrant 与 Prometheus。

文档 · [快速开始](#快速开始10-分钟) · [路由](#路由) · [为什么要带引用](#为什么要带引用)

[English](./README.md) · 简体中文

> **注意**
> 每次诊断都带来源。知识库没有匹配就明确声明无匹配——绝不编造处置步骤。

```bash
cp config/config_template.json config/config.json  # 填入你的 LLM Key
docker compose up -d
go run ./cmd/server
```

```text
oncall-agent v0.1 listening on 0.0.0.0:8819 (memonly=false, Qdrant 正常时)
```

## 快速开始（10 分钟）

前置条件：Go 1.25+、Docker、git 和一个 LLM Key。其余全部由 compose 预置：
Qdrant、Prometheus、OTel Collector 与 Jaeger。

```bash
cp config/config_template.json config/config.json  # 填 api_key，唯一必填项
cp .env.example .env
docker compose up -d        # Qdrant :6333 + Prometheus :9090 + OTLP :4317 + Jaeger :16686
go mod tidy && go run ./cmd/server
```

### 验证跑通

```bash
curl -s http://localhost:8819/ping
```

```json
{"status":"ok"}
```

然后打开 `http://localhost:8819` 用控制台，或直接调 API：

```bash
curl -s -X POST http://localhost:8819/chat \
  -H 'content-type: application/json' \
  -d '{"message":"CPU 使用率超 90%，怎么排查？"}'
```

返回包含回答与 `citations`（`{doc, snippet}`）。引用为空即表示库中无匹配。

### 接下来三步

1. **载入演示库。** `POST /reindex` 导入 `aiops-docs-demo/` 下 7 篇
   runbook（CPU宕机、服务不可用、OOM、磁盘、P99、MQ、TLS）。
2. **跑基线。** `EVAL_NORERANK=1 go run ./cmd/evalbaseline`
   回放 32 个种子问题：30 个可回答项 recall@3 为 1.0，
   2 个库外探测项拒答 0/2。
3. **看一次诊断。** `GET /plan` 拉取 firing 的 Prometheus 告警并返回
   带引用的报告；调用链可在 `:16686` 的 Jaeger 里看到。

## 路由

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/ping` | 健康检查，`{"status":"ok"}` |
| `POST` | `/chat` | ReAct 多轮对话，只读工具，回答带引用 |
| `GET` | `/plan` | Plan-Execute：拉告警 → 检索 → 带引用的报告 |
| `POST` | `/upload` | 入库一篇 Markdown runbook |
| `GET` | `/list` | 列出已入库标题 |
| `DELETE` | `/delete` | 从注册表删除标题 |
| `POST` | `/reindex` | 重载演示库 |
| `GET` | `/metrics` | Prometheus 抓取端点（不进 trace） |

控制台挂在 `/`；上一代单页保留在 `/v01`。

## 为什么要带引用

值班建议只有能核查才有用。约束直接钉在工具层：

- **只读工具白名单。** `time_now`、`rag_search`、`prometheus_query`——
  名单之外一律由 `IsAllowed` 拒绝。
- **Hybrid 检索。** 稠密向量 + 内存 BM25（CJK 二元组，RRF k=60），
  融合后标题加权 ×1.5。
- **Floor 门控的诚实。** 相似度 floor 滤掉弱命中；低于它就报无匹配，
  不猜。
- **可观测的深度。** Gin 服务 span + 每个工具 span 经 OTLP 进 Jaeger；
  请求指标直接从 `/metrics` 抓。

## 架构

```text
Prometheus 告警 ──▶ /plan ──▶ Hybrid RAG ──▶ 带引用的报告
值班提问 ──▶ /chat（ReAct + 3 个只读工具）──▶ 带引用的回答
Runbook .md ──▶ /upload · /reindex ──▶ Qdrant + BM25 镜像
```

```text
compose: qdrant（:6333）· prometheus（:9090）· otel-collector（:4317/:4318）· jaeger（:16686）
app: :8819 · embedder 默认：本地 Ollama nomic-embed-text（:11434）· LLM：OpenAI 兼容 api_base + model + key
```

目录按 `internal/` 分层：`config`、`handler`、`agent`、`rag`、
`store`、`tool`、`observability`、`trace`。选型钉在
`docs/adr/0001-0004`；各版本范围见 `docs/ROADMAP.md`。

## 配置与运维

只有 `openai.api_key` 必填，其余走模板默认值：

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `server` | `0.0.0.0:8819` | 服务地址 |
| `openai.api_base` + `model` + `api_key` | OpenAI 兼容 | 任何兼容端点可用 |
| `qdrant` | `127.0.0.1:6334`，collection `oncallagent` | HTTP 探测 `:6333`；失败回退内存 |
| `embedder` | `127.0.0.1:11434`，`nomic-embed-text` | 启动时自动探测向量维度 |
| `prometheus.url` | `http://localhost:9090` | 告警 + 查询来源 |

不要提交 `config/config.json`——它已在 gitignore 里。MCP 工具路由
（`modelcontextprotocol/go-sdk`）与相似度 floor 按
`docs/adr/0004-mcp-otel.md` 分阶段落地；沙箱保持关闭，告警动作保持
只读（不确认、不静默）。

## 已知局限

- 拒答靠 floor 门控，不完美：当前样本里库外探测 0/2 拒答（floor 默认值）。
- Rerank 只在评测链路跑，不进线上 `/chat`。
- 暂无 license 文件声明。
- `delete` 只清注册表；向量彻底删除是 v0.3 项。

## 开发

```bash
gofmt -l .            # 必须干净
go vet ./...          # 必须过
go build ./...        # 必须过
EVAL_NORERANK=1 go run ./cmd/evalbaseline   # 检索基线
```

提交用 Conventional Commits（`feat:`、`fix:`、`docs:`、`chore:`），一次
一个原子改动。
