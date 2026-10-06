# CONTEXT — oncall-agent (self)

## Glossary

- **告警 (Alert)**: Prometheus firing 实例。含 name/severity/description/labels/startsAt。不确认不静默，处置始终由人执行。
- **知识 (Runbook)**: 人工审定的 Markdown 运维手册。一篇一故障，标题即故障名，权重高于正文。demo 预置与 upload 同属此类。
- **事件沉淀 (Incident Note)**: AI 诊断报告的自动入库（v0.4 起）。source=incident，信任等级低于知识，检索降权，同题覆盖，可按 source 整体清除。沉淀是诊断管线的后置写入，不是工具。
- **告警驱动诊断 (Alert-driven Diagnosis)**: 告警经 webhook 推入自动触发的诊断（v0.4 起）。与手动入口同一条执行链，产出结构化诊断报告。
- **诊断 (Diagnosis)**: 引用知识生成的处置报告。必须带引用片段，无匹配则明示无匹配，不编造。
- **工具 (Tool)**: Agent 可调的只读查询。时间/文档检索/指标与告警查询。白名单外一律拒绝，写操作不作为工具。
- **会话 (Session)**: 一串多轮对话的 ID。服务端保历史（LRU 上限 256，超限逐最旧），客户端传 Id 复用，`DELETE /session` 可清空（v0.7.4 起）。
- **开箱即用 (Out-of-box)**: `compose up` 后即玩。LLM Key 与 Redis 必填（v0.5 起诊断队列硬依赖 Redis，词条自 v0.4 的「仅 Key 必填」降级），Qdrant/Prometheus/Alertmanager/demo 知识全预置。
- **评测 (Eval)**: 证明检索/生成可信的 sample 集 + 脚本。v0.1 占位，v0.2 对比报告。
- **拒答 (Refusal)**: 库外或无匹配的问题明示无匹配、不编造。语义落生成层（v0.3 起）；检索层 Floor 门控只作旋钮保留，不承担拒答验收。
- **诊断队列 (Diagnosis Queue)**: 告警诊断的异步执行（v0.5 起）。Redis 硬依赖，POST /alert 入队秒回，诊断完成后落 /reports。与手动入口同一条执行链。
- **诊断自评分 (Diagnosis Self-score)**: judge 对诊断的 1-5 评估（v0.5 起）。纯观察值，挂 /reports 条目，不驱动任何行为；评分失败降级无分，不挡主链。
- **低分标记 (Low-score Flag)**: 自评分低的布尔标记（v0.5 起）。仅 /reports 与控制台高亮可见；把低分送达给人由[[通知写回]]承接（ADR-0008）。
- **通知写回 (Notification Write-back)**: 低分或 failed 诊断经 webhook 送达（v0.6 立项，ADR-0008）。outbound 单向告知，不是 remediation——不确认不静默不处置；at-least-once，通知失败不挡诊断主链。
- **变更富化 (Deploy Enrichment)**: 诊断时经只读工具拉取最近部署/提交事件作旁证上下文（v0.7 立项，ADR-0009）。配置门控：未配变更源不注册；产出是观察信号，入报告独立字段，不是知识引用，不驱动处置；查询失败降级不挡诊断主链。

## Vision (deferred)

完整控制台 + 企业级知识库 (版本/权限/去重) + 完整评测 + Hybrid+Rerank 全量，属 v0.2+，不在 v0.1。

---
Migrated from `/Users/acfufu/Codehub/opencode/CONTEXT.md` on 2026-09-22.
Source decisions: `docs/adr/0001-0003`.

## Evidence Workspace（2026-10-06 / ADR-0011、0012）

本次已立项 M0可靠性、M1事件证据工作台、M2声明拓扑/受限指标，M3仅规划。SQLite为业务事实源，Qdrant/BM25为检索投影；文档不可变版本、run独立生命周期与证据状态。has_citations不是根因确认，judge不是正确率。默认关闭事件沉淀；聊天默认无状态；HTTP统一轻量认证。旧JSON快照仅迁移输入，保留历史引用。适用的新约定优先于上方历史版本说明。
