# ADR 0002 — LLM 用 OpenAI 兼容接口

- Date: 2026-09-22
- Status: Accepted

## Context

仅 LLM Key 必填，其他全预置。若锁 DashScope，劝退一半用户。

## Decision

`api_base + model + key` 可配。默认 OpenAI 协议，DashScope 作可选端。

## Consequences

- Embedding 先用 Ollama 本地，云 embedding 作可选项。
- Rerank 须选兼容端可接的模型，v0.2 定。
