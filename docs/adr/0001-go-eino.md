# ADR 0001 — 选 Go + Eino

- Date: 2026-09-22
- Status: Accepted

## Context

开源产品，对标 yin (Go) / wangsai (Java)。要传播广、部署轻。

## Decision

Go + CloudWeGo Eino。复用 yin 的 ReAct / Plan-Execute 骨架。

## Alternatives

- Java + Spring AI：企业集成强，但重，且 wangsai 已占位。
- Python + LangChain：生态强，但发行/部署弱于 Go。

## Consequences

- 须自补 Hybrid/BM25 在 Go 侧成本。
- compose 预置 Qdrant + Prometheus + demo 知识。
