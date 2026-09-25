# ADR 0006 — 诊断队列化与异步告警契约（v0.5）

- Date: 2026-09-26
- Status: Accepted（2026-09-26 立项拷问拍板；修订 ADR-0005「同步执行、同步返回」条款，降级 ADR-0003「仅 Key 必填」开箱承诺）

## Context

v0.4 验收记录在案：同步长诊断可超 Alertmanager 投递超时触发重试，demo 靠 repeat_interval=4h 缓解，job 化异步点名留 v0.5。异步化同时是后续一切的地基——运行时自评分要挂异步完成后的 /reports 条目上。立项拷问中用户拍板走重依赖路线：Redis + asynq 硬依赖，接受「开箱即用」词条降级，不做进程内回退。

## Decision

- **队列**：Redis + asynq 硬依赖，compose 预置 redis；单一执行形态，不提供进程内回退、不做双模式。
- **契约**：POST /alert 单一异步语义——入队即回 `202 {id, status:queued}`；诊断完成后落 /reports，条目含 queued/running/done/failed 状态；console 改轮询；BREAKING 写 README 迁移说明。Plan-Execute 执行链不变：Planner.PlanPushed 同参直调链为 eval 保留，不经 HTTP 入口。
- **AM 重试就此消掉**：webhook 秒回 2xx，投递不再超时，v0.4 的 repeat_interval 缓解手段降级为纯控频。
- **重启语义**：asynq 任务持久在 Redis，重启后在途任务可重投，幂等靠事件沉淀同题覆盖；/reports 环仍内存态，重启丢历史报告（与 v0.4 口径一致）。
- **自评分（同 ADR 内拍）**：judge 对每条完成的诊断打 1-5 分，**纯观察值**——挂 /reports 条目，不驱动任何行为；judge 失败降级无分，不挡诊断主链。低分仅作布尔标记 + console 高亮；外发送达明确留 v0.6 通知写回（拷问中已拆两步，防范围蔓延）。

## Alternatives

- 进程内 goroutine（拷问推荐项）：零新依赖、与 /reports 内存环同级定位，但用户拍板要生产正确姿势，接受词条降级与多一容器。
- SQLite/badger 持久队列：少一个服务且重启不丢，但引入持久层自养；asynq+Redis 是更标准的组合。
- 异步默认 + ?sync=1 / 配置双模式：两条语义两份验收，且同步路径下 AM 重试问题原样存活，拒。

## Consequences

- 「开箱即用」词条降级（CONTEXT.md 已改）：LLM Key 与 Redis 必填，Qdrant/Prometheus/Alertmanager/Redis 全预置；裸 `go run` 开发需先 compose 起 redis。
- 四片：asynq 接入+队列封装 / /alert 异步契约+reports 状态 / console 轮询 / 自评分+低分标记。
- 验收关：compose 起 redis；POST /alert 秒回 202；AM 真流转零重试；拔 LLM Key 活验降级无分不挡链；EVAL_ALERT 直调链回归同级。
- 风险：多一容器资源变重；Redis 挂则告警诊断全停（单点，demo 定位如实标注）；BREAKING 断 v0.4 同步返回的老客户端。
