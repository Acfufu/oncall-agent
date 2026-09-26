# ADR 0008 — 通知写回：低分/失败诊断的 webhook 送达（v0.6）

- Date: 2026-09-27
- Status: Accepted（2026-09-27 v0.6 立项拷问拍板；承接 ADR-0006「低分先标记后送达，外发送达留 v0.6」拆步；沙箱 ADR 编号顺延 0009）

## Context

ADR-0006 拍板拆两步：低分先标记后送达。v0.5 落地后的事实是：judge 低分与 failed 状态只躺在 /reports 内存环里——容量 20 可驱逐、重启丢历史，failed 在队列语义下是黑洞，可能永远无人看见。v0.6 立项拷问逐分支拍板（触发器/渠道/投递/阈值/打包），本文界定「通知不是 remediation」的边界并锁实现形状。

## Decision

- **边界：通知不是 remediation**。纯 outbound 单向告知，不确认不静默不处置（告警词条不变）；通知失败不挡诊断主链，与 judge 同降级姿态。
- **触发器**：低分（score < low_threshold）+ status=failed 两类，只挂异步告警诊断链（worker 出终态）；手动 /chat 是同步对话、提问者本人在场，不通知。
- **渠道：通用 webhook**。POST /reports 条目同形状 JSON 到配置 URL；钉钉/飞书/Slack 适配留适配器层，不上核、不锁厂商。
- **投递：asynq 第二类 task**。MaxRetry 3 + 指数退避；语义 at-least-once，接收端按 report id 幂等去重；task 载荷自包含（完整报告 JSON），/reports 环驱逐与重启丢历史不影响在途通知；重试耗尽记 metric `notification_failed_total` + 日志终态，/reports 不加通知状态字段（观察面不升级为状态机）。
- **阈值不动**：low_threshold 默认 3 保持，config 可调；judge 行为不动（纯观察值）；v0.5 eval 的 2 条 LOW fixture 复用为通知触发测试用例。

## Alternatives

- 只做低分送达（ADR-0006 字面）：failed 黑洞留着，known limitation 不拔，拒。
- IM 原生格式入核：厂商 payload 焊进核心，换 IM 改核，拒。
- fire-and-forget：基础设施故障期恰是低分/failed 连发期，通知永久丢失且叠加环驱逐无处可查，拒。
- at-least-once + 通知状态机（接收端 ack、发送端通知状态流转）：新状态面，demo 定位过重，拒。
- 预先人工审计 judge 严格度再定阈值：空对空；阈值是旋钮，跑后按噪声反馈调更便宜，拒。

## Consequences

- 新增 outbound 面：webhook URL 为出站配置，无新入站端口；接收端鉴权/签名按需后续加。
- Redis 单点覆盖通知链（与诊断队列同单点，无新增风险，ADR-0006 口径不变）。
- 四片：ADR+词条 / webhook client+通知 task / config+README / 验收（LOW fixture 触发、failed 触发、断网重试恢复送达）。
- LICENSE 落 Apache-2.0，README known limitation 清一条；沙箱 ADR 顺延 0009。
