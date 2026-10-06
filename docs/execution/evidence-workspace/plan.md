# 执行计划 / T00–T23

审查 architecture_review 的 REVISE 已采纳：T06先于T07，T05接口先于T04接线；事实层写路径唯一，栅栏覆盖引用/终态/通知；通知独立消费者。M0先通过不毁库/明确失败/可靠版本/认证/幂等/证据门控，再开放M1 live；M2依赖M1，M3仅deferred。

1. T00/T01: 当前HEAD、逐项审查和工具基线；ADR0011/12、词汇与API/状态冻结。
2. T02/T05: 严格embed/Qdrant/context、禁止删库和非隔离eval，默认关闭auto_ingest。
3. T03/T04: SQLite事务迁移、不可变版本/chunk、活动版本过滤与BM25恢复。
4. T06/T11: 统一认证、Origin/CSRF、会话限额、无状态聊天、共享MCP transport。
5. T07/T08/T09/T10: 事务admission+outbox、attempt栅栏/逐事件引用、真实阶段、确定性证据保护、独立通知消费者。
6. T12: fake HTTP+临时SQLite业务路径；可用时隔离Redis/Qdrant/Prometheus；记录分级。
7. T13–T18: 七页React+TS shell与实际API接线、图/列表/来源/时间线/文档版本；浏览器截图、语义例外、旧UI回退。
8. T19–T23: 声明拓扑、受限指标查询、真实趋势，边界/故障/窄屏/预算验收、备份回滚与README。

Stop: M0–M2 acceptance逐项implemented/verified；外部依赖或模型缺失单独blocked，不将未跑检查算通过。禁止commit/push/deploy或改写用户配置；无业务清库。

并行所有权：retrieval_reliability拥有rag/store/Prom/eval；auth_boundary拥有auth/chat/MCP；frontend_workspace拥有web/app与dist；主线程拥有SQL事实、业务协调、其他handler、queue、config/main与集成。接口以交接domain.ts及docs03为准。

2026-10-06执行更新：M0业务L1与主仓test/race/vet已通过（后续变更持续刷新），M1真实隔离Go UI已验证上传、无证据及含引用来源；M2配置/指标/拓扑与summary.history已接线。Docker Desktop启动后daemon可用，改为继续隔离L2而非提前标BLOCKED。最终独立architect审查正在执行，UI最终截图/性能与容器构建正在执行。已捕获并修复取消终态、attempt旧引用、通知原始status、知识metadata与历史version创建时间，日志保留红绿。Stop condition不变；M3不实施。

最终增量验证：M0真实隔离Redis/Qdrant/Prometheus/Alertmanager与原问题逐项回归通过；M1实际文档上传→告警→引用/无证据 UI 闭环通过；M2声明拓扑与三个实际受控指标可见。最后独立审查要求修复生命周期跨分页筛选与拓扑来源跨事件残留；修复后重跑浏览器并重新绑定最终构建。M3只保留规划。
