# oncall-agent (self)

开源 OnCall 智能值班代理。Go + Eino + OpenAI 兼容 + Qdrant + Prometheus。

开箱：仅 LLM Key 必填，其余 compose 预置。

## Quickstart

```bash
cp config/config_template.json config/config.json  # 填 LLM Key
cp .env.example .env
docker compose up -d        # Qdrant + Prometheus
go mod tidy && go run ./cmd/server
```

- App: `http://localhost:8819` (`/ping`, `/chat`, `/plan`)
- Prometheus: `http://localhost:9090`
- Qdrant: `http://localhost:6333/dashboard`

## API (v0.1)

- `GET /ping` 健康检查
- `POST /upload` markdown runbook 入库
- `POST /chat` 对话 (ReAct, 只读工具)
- `GET /plan` 告警一键诊断 (Plan-Execute, 只读)

## Docs

- `CONTEXT.md` 术语
- `docs/adr/` 决策
- `docs/COMPARISON.md` 对标两仓库差异
- `docs/ROADMAP.md` 分期路线

## Scope

v0.1 最小闭环：单路召回 + 极简单页 + sample 评测占位 + 沙箱默认关。
v0.2+ 见 ROADMAP (Hybrid/Rerank/完整控制台/企业级知识库)。
