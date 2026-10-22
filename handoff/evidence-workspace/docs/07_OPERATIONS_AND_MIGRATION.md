# 07 · 配置、迁移与回滚

## 1. 运行形态

本阶段单实例Go服务 + SQLite持久目录 + Redis/Qdrant/Prometheus。React编译成静态资源由Go托管；开发用Vite同源代理，生产不运行Vite/Node。默认不公开公网。

完整compose方案新增/装配app service，容器内可监听0.0.0.0，但宿主端口发布为 `127.0.0.1:8819:8819`；AM通过容器网络 `app:8819/alert` 加入站认证。Redis/Qdrant/Prometheus等端口同样只在需要时发布到loopback。不要以“app在容器内监听所有接口”误判为宿主公开，亦不要把宿主0.0.0.0开放当作AM回调修复。

本地 `go run` 在loopback监听时，Docker内的AM未必能直接访问宿主loopback。该开发模式先使用受认证手动推送或/plan；需要完整AM链路时用compose app service或明确受限的网关路径，并实测。README不得保留一个必然连不通的组合。

## 2. 配置增量（建议键，需与现有config struct对齐）

| 配置 | 默认/规则 |
|---|---|
| `storage.mode` | persistent；显式demo与生产配置分离 |
| `storage.sqlite_path` | `./data/oncall-agent.sqlite`，数据目录持久化 |
| `auth.console_token_env` | 环境变量名，不是token正文 |
| `auth.webhook_token_env` | 独立AM凭据环境变量名 |
| `auth.session_ttl` | 8h；重启清会话可接受但不清业务数据 |
| `knowledge.auto_ingest` | false |
| `knowledge.embedding_space_id` | 显式模型/维度/配置版本指纹 |
| `ui.mode` | live；demo必须单独显式启用并持续可见 |
| `ui.legacy_enabled` | 首次迁移保持true以支持/legacy回退 |
| `limits.*` | 03文档默认值，可调但有上界 |
| `topology.file` | 未配置则能力关闭；M2受控文件 |
| `metrics.templates_file` | 未配置则不提供指标查询；M2受控模板 |

保留原config模板作为唯一live配置结构入口；样例只放变量名和假值。禁止将用户config.json、.env、SQLite真实数据、真实告警或token写入Git。demo fixture明确没有密钥，可入库。

## 3. 初次迁移

1. **dry-run盘点。** 输出现有collection、维度/模型信息、可导出payload数量、文档源类别、报告可见数量，列出无法恢复的项；此步不DELETE不reindex。
2. **备份。** Qdrant用受支持的snapshot/export；旧进程仍活着时保存可见/reports输出（历史内存环以外的数据无法凭空恢复）。SQLite启用WAL时用backup API/一致性checkpoint流程，不能只copy主db遗漏WAL。
3. **建立新SQL schema。** 记录schema_migrations，启用foreign keys；明确事务/busy timeout/单实例写策略。schema升级失败保持原数据可读或安全停止。
4. **恢复知识。** 优先从原始Markdown重建；若只能从Qdrant片段恢复，标记`legacy_snapshot`与不完整元数据，禁止假装有原版本/审核来源/完整顺序。不能凭标题把不同来源拼成一份已审定runbook。
5. **影子索引。** 新payload schema或embedding_space需要新的collection时创建旁路collection，回填并核对数据；原collection保持只读保留。新SQLite与BM25只将符合资格的版本激活。
6. **切换。** 一致性/引用/检索回归后变更显式配置指向新索引。新UI读取已验证API；旧console移到/legacy、原/v01保留，主路由交给新应用。
7. **观察与回滚。** 验证AM入站、队列、通知、依赖故障、重启恢复。UI回退只切前端；不能用回退UI掩盖schema已变化。代码回退需要与当前schema兼容或恢复预升级备份。

旧schema向量与新schema向量不能不加过滤混在同一检索视图。迁移脚本要能重复执行，有source snapshot/进度记录，不能每次启动自动重跑破坏性迁移。

## 4. 测试资源隔离

每次集成测试创建随机测试run标签，用于独立SQLite临时目录、Qdrant collection、Redis数据库/队列前缀和假webhook。清理前核验标签与ownership manifest；拒绝清理非本次创建资源。

只有名字带test_不够，不能删除他人同前缀资源。eval默认不读取生产collection作为写目标。测试sentinel放在另一模拟业务namespace，验证测试前后不变。

## 5. 演示模式

`fixtures/*.json`只给显式demo路由、mock adapter或测试使用。live fetch失败后展示错误/stale，不导入fixture假装服务正常。demo中的所有KPI从demo集合计算，原图数字不是约束。

demo页面不能提交到live /alert/upload/delete。生产构建可以不包含demo数据；若包含，必须通过显式开关进入，持续标识“演示数据 · 不代表真实运行”。

## 6. 备份、保留与错误语义

首版不自动删历史报告/文档版本；默认由管理员显式导出/保留策略后再清理，后续可加保留上限。磁盘/数据库写失败时新入站返回明确503，不假装接受并丢记录。UI应显示存储故障。

报告引用与事件全文可能包含内部基础设施信息；出包/导出时支持必要脱敏，不在浏览器localStorage长期缓存完整原始证据。清空文档默认是当前检索撤下，不是合规意义不可恢复擦除；UI要准确解释。

## 7. 发布清单

M0先验收；M1用测试环境走真实告警→run→图→来源→时间线；M2未配置指标/拓扑时不影响M1。首次发布保留旧UI入口、配置备份、索引备份和回滚指令。部署到公网或真实生产、删除旧collection均需单独明确授权，本包不授权执行。
