# 确定性工作区夹具

所有JSON都标记`meta.mode=demo`，时间固定，环境名demo-prod，来源带synthetic。它们仅供mock adapter、契约测试、视觉测试与显式演示。不是真实故障观测，不证明当前系统已经实现对应能力。

| 文件 | 重点 |
|---|---|
| 01-evidence-ready.json | M1事实型证据链，2个版本化引用，任务成功但故障活跃 |
| 02-no-evidence.json | 安全no_evidence完成结果，不夹带未经支持的处置 |
| 03-source-unavailable.json | 依赖故障，不应伪装no_evidence或正常空数据 |
| 04-retry-succeeded.json | 首次检索失败、第二次成功；阶段成功但保留失败attempt |
| 05-resolved.json | 恢复由单独来源事件决定，不是诊断任务完成决定 |
| 06-partial-graph.json | 返回截断、省略数量与partial标识 |
| 07-notification-pending.json | 诊断已完成，通知仍pending；二者独立 |
| 08-hypothesis-with-sources.json | M2候选假设、观测与来源，inferred虚线且待验证，无概率 |

WorkspaceSnapshot是前端组合读模型：REST里incident/run/graph/events可分别加载，测试mock须按03文档路径从同一fixture拆分返回，不要求新增一个庞大全量API。summary为selected_incident范围，仅一个事件的统计，不当全局运营数字。

夹具里的source_ref是不透明定位符，不能由浏览器当作可执行协议。document版本、chunk与引用绑定是测试数据，不是完整文章复制。

验证：在包根执行`python scripts/validate_handoff.py`；需要Python及jsonschema4.x。脚本会明确失败而非跳过schema校验。
