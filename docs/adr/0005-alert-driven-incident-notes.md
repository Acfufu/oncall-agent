# ADR 0005 — 告警驱动诊断入口 + 事件沉淀（v0.4）

- Date: 2026-09-26
- Status: Accepted（2026-09-26 立项拍板，依据 docs/research/2026-09-26-v040-survey.md 两路调研）

## Context

v0.3 已收口（只读白名单闭环 + MCP/OTel 验收过）。调研显示：Alertmanager webhook 是所有活跃开源项目（k8sgpt/HolmesGPT/keep/versus-incident）的共同告警入口；商业产品的安全共识是「只读诊断可全自动、执行必须人审」——本项目只读定位踩在正确一侧；知识回流飞轮（诊断结论反哺知识库）是 incident.io/Cleric 等的标配成长机制。当前 /plan 是手动拉模式，缺「告警推进来」一环。

## Decision

- **POST /alert**：兼容 Alertmanager webhook payload（`alerts[]` 数组，也接受单条对象），同步执行、同步返回结构化诊断报告（alerts/diagnosis/citations/receivedAt）。与 GET /plan 共用 Plan-Execute 同一条链（Planner 增加显式告警入参），两个入口壳一条执行链。
- **同步起步**，不做 job 化异步。代价如实记录：Alertmanager webhook 有投递超时且失败重试，诊断耗时超限会触发 AM 重试导致重复诊断；v0.4 在 README 标注 AM 侧 group_interval/repeat_interval 配置注意事项，job 化异步留 v0.5。
- **GET /reports**：内存环存最近 N（默认 20）条告警驱动诊断，供人查看；控制台加区块。理由：同步模式下报告返回给 AM 会被丢弃，必须有人类可见的落点。
- **compose 预置 Alertmanager** + demo 告警规则（repeat_interval=4h 控重复诊断成本），AM route 指向 oncall-agent:8819。验收形态是 Prometheus 规则→AM→/alert 全真实流转。
- **事件沉淀（Incident Note）**：告警驱动诊断完成后，报告自动入库，`source=incident`；同题（alertname）覆盖——先删后写守「一篇一故障」；`auto_ingest` 配置开关默认开；检索降权系数默认 0.5 可配。沉淀是诊断管线的**后置写入**，**不是 agent 工具**——三只读白名单不变，agent 侧仍无写路径。
- **防自证循环**：诊断 eval 跑前先按 source 清 incident 沉淀——否则 fixture 告警会命中上次沉淀、引用自己的报告，eval 失真。
- 诊断引用返回体增加 `source` 字段，人可分辨证据来自 runbook 还是事件沉淀。

## Alternatives

- 异步 job 化起步（行业标准形态）：需 job 存储/状态机/查询端点，demo 不直观，v0.4 版本预算装不下，推 v0.5。
- 人工显式回写（POST /knowledge 确认后才入库）：知识库纯度最高，但用户拍板选自动+降权双轨——对标 incident.io 全自动反哺，以降权 + 可整体清除（DeleteBySource 现成）控污染。
- deploy_events 第四只读工具（行业验证的最高价值 RCA 信号）：demo 环境无真实变更源可拉，「全真实开箱」约束下难活验，推 v0.5 先定源。

## Consequences

- 四片：/alert 入口+payload 解析 / reports 环+console / AM compose 真流转 / 沉淀入库+降权+eval。
- 验收关：AM 真流转报告可查；诊断 eval 规则断言全绿；降权/开关/清沉淀活验；既有回归不破。
- 风险：同步长诊断被 AM 重试（文档缓解）；incident 沉淀低质内容混入检索（降权+开关+清除三闸）；报告环为内存态，重启即失（demo 定位可接受）。
