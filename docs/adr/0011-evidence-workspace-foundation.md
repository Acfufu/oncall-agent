# ADR-0011 Evidence Workspace Foundation

Status: Accepted（本次用户明确授权 M0–M2，2026-10-06）

保留 Go/Gin、Redis/asynq、Qdrant、Prometheus、OTel 与只读工具边界；增加 SQLite 单实例事实持久层（database/sql，锁定 SQLite driver），以事务保存告警生命周期、观测、run、版本/chunk、检索与引用、run_events、outbox。图是可重建读模型。SQL UNIQUE admission、规范化排序 labels + UTC startsAt 与事件集合/policy 哈希消除重投；缺 startsAt 使用5分钟桶并显示 windowed。attempt 版本栅栏拒绝旧写入。

文档 staging→索引→事务激活，旧版本/历史片段保留；当前检索按 SQL 活动版本过滤。Embedding 空间由 provider/model/dimension/config 指纹固定；持久模式 embed/Qdrant 故障显式错误，不切 hash/memory，不自动删除集合。评测必须独立资源与所有权守卫，不运行旧业务清库路径。

SQLite run+outbox 先提交，再投递 Redis；接受后短暂投递失败仍202 pending，可恢复。Redis仍硬执行依赖，无进程内生产诊断回退。通知独立消费者及持久 payload/status，不承诺外部 exactly-once 费用或通知。

凭据从环境变量取值，console 与 AM 分权；浏览器 HttpOnly短会话+Origin/CSRF，缺凭据 fail closed。聊天默认无状态；无本轮合格证据确定性安全返回，不输出无依据操作建议。默认 auto_ingest=false，incident来源不参与默认可信检索。

Supersedes: ADR-0005默认自动沉淀；ADR-0006仅Redis入队接受/内存报告；ADR-0007 HTTP无鉴权；ADR-0008通知不记录状态；ADR-0010 JSON作为唯一事实源。旧JSON仅显式非破坏导入来源，绝不启动覆盖；旧ADR保持历史原文。trace包外API保持不变。
