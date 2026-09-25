# CONTEXT — oncall-agent (self)

## Glossary

- **告警 (Alert)**: Prometheus firing 实例。含 name/severity/description/labels/startsAt。不确认不静默，处置始终由人执行。
- **知识 (Runbook)**: 人工审定的 Markdown 运维手册。一篇一故障，标题即故障名，权重高于正文。demo 预置与 upload 同属此类。
- **事件沉淀 (Incident Note)**: AI 诊断报告的自动入库（v0.4 起）。source=incident，信任等级低于知识，检索降权，同题覆盖，可按 source 整体清除。沉淀是诊断管线的后置写入，不是工具。
- **告警驱动诊断 (Alert-driven Diagnosis)**: 告警经 webhook 推入自动触发的诊断（v0.4 起）。与手动入口同一条执行链，产出结构化诊断报告。
- **诊断 (Diagnosis)**: 引用知识生成的处置报告。必须带引用片段，无匹配则明示无匹配，不编造。
- **工具 (Tool)**: Agent 可调的只读查询。时间/文档检索/指标与告警查询。白名单外一律拒绝，写操作不作为工具。
- **会话 (Session)**: 一串多轮对话的 ID。服务端保历史，客户端传 Id 复用，可清空。
- **开箱即用 (Out-of-box)**: `compose up` 后即玩。仅 LLM Key 必填，Qdrant/Prometheus/demo 知识全预置。
- **评测 (Eval)**: 证明检索/生成可信的 sample 集 + 脚本。v0.1 占位，v0.2 对比报告。
- **拒答 (Refusal)**: 库外或无匹配的问题明示无匹配、不编造。语义落生成层（v0.3 起）；检索层 Floor 门控只作旋钮保留，不承担拒答验收。

## Vision (deferred)

完整控制台 + 企业级知识库 (版本/权限/去重) + 完整评测 + Hybrid+Rerank 全量，属 v0.2+，不在 v0.1。

---
Migrated from `/Users/acfufu/Codehub/opencode/CONTEXT.md` on 2026-09-22.
Source decisions: `docs/adr/0001-0003`.
