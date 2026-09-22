# ADR 0003 — 分期交付，v0.1 最小闭环

- Date: 2026-09-22
- Status: Accepted

## Context

用户连选最大：双入口 + 全真实 + Rerank + 完整控制台 + 企业级知识库 + 完整评测。一人 v0.1 做不完。

## Decision

- v0.1：告警一键诊断 + 极简对话 + 单路召回 + 极简单页 + upload/list/delete/reindex + eval sample 占位 + 只读 (沙箱默认关)。
- v0.2+：Hybrid/Rerank、完整控制台、企业级知识库、完整评测对比。

## Consequences

- 先开箱跑通拿 star，再堆质量。
- 路线图必须写明，避免被指功能缩水。
