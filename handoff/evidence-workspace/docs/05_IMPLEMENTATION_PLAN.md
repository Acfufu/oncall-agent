# 05 · 执行计划与里程碑

`../plan/tasks.json` 是同一计划的机器可读版。所有任务初始状态pending；本包没有实现任何目标仓库任务。任务状态只在实际执行后更新，不因读完规范而变成done。

v1.1补齐7页概念图，但保留T00–T23共24个任务及原有依赖。T13增加七页公共shell/导航和真实能力状态；T18/T22增加UI-05逐页截图核对。任务的reference_screen_ids指向design/screens.json；第10章规定每页范围，不因图片新增M3实施或自动处置能力。

## 1. 工作树与包位置

建议将此包放到仓库 `handoff/evidence-workspace/`，把实施记录放在 `docs/execution/evidence-workspace/`。不要将本包 README/规则自动复制覆盖项目根文件。新ADR编号先检查占用。

目标结构示意（可与现有命名协调，不要求重复已有包）：

```text
cmd/server/                         保留入口，装配领域服务与新路由
internal/persistence/               SQLite migrations / repositories
internal/incident/                  身份、admission、生命周期
internal/evidence/                  资格门控、引用关联、图读模型
internal/knowledge/                 文档版本与索引协调
internal/queue/                     保留asynq；恢复投递/通知消费者
internal/handler/                   legacy与api/v1 adapter
internal/tool/                      只读Prometheus范围查询
web/app/                           新Vite应用
  src/app/                         路由、认证、应用shell
  src/features/incidents/           列表/工作台
  src/features/evidence/            graph + inspector + accessible list
  src/features/timeline/            stage attempts
  src/features/knowledge/           文档与历史版本
  src/features/metrics/             M2
  src/features/topology/            M2
  src/lib/api/                      DTO解码、请求取消、查询cache
  src/styles/                       令牌、基础样式
  tests/                           fixtures、component、Playwright
web/dist/                          构建产物，构建流程生成
```

是否使用现有store目录承载SQLite、现有handler统一API等属于低风险实施决定。不得为了匹配示意结构移动一切文件。服务器静态资源路径用go:embed或已有嵌入方式；构建Go时前端产物存在，缺产物必须明确失败/回退配置，而不是白屏。

## 2. 任务表

| 任务 | 内容 | 依赖 | 完成证据 |
|---|---|---|---|
| T00 | 记录HEAD、工作树、原测试/依赖；重核R01–R15 | 无 | baseline.md，逐项状态 |
| T01 | 新ADR/ROADMAP/CONTEXT；冻结ID、状态、API、视觉边界 | T00 | ADR diff，contract decision log |
| T02 | 禁自动删库、隔离eval、默认关闭auto_ingest | T01 | sentinel/启动不DELETE回归 |
| T03 | SQLite migrations/repository、版本与运行逻辑实体 | T02 | migration/restart/repository测试 |
| T04 | 文档staging/激活/删除与BM25恢复 | T03 | 更新缩短、失败窗口、重启测试 |
| T05 | embedding空间/维度、Qdrant错误与context、readiness | T02 | 故障注入+无静默回退 |
| T06 | auth/session/CSRF、输入和速率限制、stateless chat | T01 | 匿名/AM/浏览器/MCP权限测试 |
| T07 | admission幂等、run_incidents、outbox投递、恢复状态 | T03,T06 | 并发重投、Redis故障窗口测试 |
| T08 | worker attempt、run_events、逐告警检索与报告来源 | T07,T05 | 真实阶段记录、失败传播、隔离引用 |
| T09 | 实际/chat与Planner证据门控、错误引用/库外测试 | T04,T08 | 实际服务路径fake模型回归 |
| T10 | 通知outbox、完整payload、独立消费者、重试显示 | T08 | 一次入队失败可恢复、不重复诊断 |
| T11 | MCP transport共享与关闭、统一认证 | T06 | initialize→GET→POST→close |
| T12 | M0集成回归及API兼容基线 | T04,T05,T09,T10,T11 | go/隔离integration日志，R01–R15结果 |
| T13 | 前端shell、tokens、明确demo、路由与响应式 | T01 | 原图对照截图；无live假数据fallback |
| T14 | /api/v1读模型、过滤、pagination、脱敏与版本快照 | T03,T08,T09 | DTO/schema/contract tests |
| T15 | 事件证据图+边检查器+原始引用+可访问列表 | T13,T14 | 来源可点、无悬空边、旧响应不串线 |
| T16 | 真实时间线、stage attempts、notification状态 | T13,T14,T10 | retry/skipped/failed/恢复截图 |
| T17 | 文档管理UI、版本查看、删除确认与失败态 | T04,T06,T13 | 文档生命周期浏览器E2E |
| T18 | M1集成、视觉验收、只读UI上线开关与旧UI回退 | T12,T15,T16,T17 | fixtures→real flow、截图+异常状态 |
| T19 | M2服务依赖文件/schema、拓扑读API、潜在影响 | T18 | 循环/跨环境/未知来源校验 |
| T20 | M2受限metrics模板/query_range、缺失数据规范 | T18 | 窗口/series/NaN/timeout测试 |
| T21 | M2拓扑与指标联动、真实统计与时间窗 | T19,T20 | 同一事件/同一时间窗联动截图 |
| T22 | 最终故障、安全、窄屏与性能预算验收 | T21 | 实测环境、原始日志、剩余限制 |
| T23 | 备份迁移回滚演练、README和交付清单 | T18,T22 | 干净环境启动、rollback证明 |

## 3. 里程碑退出条件

**M0：** 不毁库、不假成功、知识更新可控、实际路径证据不足时安全返回、入队/通知可恢复、依赖故障可见、只读访问边界真实生效。R01–R15未关闭的项明确blocked，不能给真实开放访问。

**M1：** 一条真实测试告警进入持久任务；UI能看到它的真实检索/引用/时间线；重启后仍可浏览。原图结构在主视口接近，所有可见动作有效，演示与真实状态不会混合。演示fixture可以先完成，但不算真实闭环完成。

**M2：** 服务依赖有声明来源，指标来自受限模板；缺失/超时不伪造数值；关联不冒充因果。指标和拓扑未配置也可以正确运行M1。

**最终：** 新UI开关可回退、迁移不破坏旧数据、旧HTTP接口有测试、README不再夸大引用/拒答/通知可靠性，所有blocked/deferred显式列出。

## 4. 并行边界

T01完成后，可并行T02/T05/T06/T13；核心实体、DTO、SQL migration由单一责任线程维护。T13可以用fixtures，不绕过T12开放live。T15/T16/T17可在契约稳定后并行。T19/T20可并行；T21统一联动。

避免多个子任务同时改 `cmd/server/main.go`、AGENTS、核心DTO或同一migration。每次合并后跑contract tests，防止前端私自把pending/done字段换成另一套枚举。

## 5. 提交与验证纪律

按仓库已有Conventional Commits规范小步提交：docs决策→破坏性守卫→持久层→生命周期→证据→API→UI→集成。失败测试应先证明缺陷或场景，再提交修复；不要求为每行样式编写单元测试。

只有在需要更改真实数据、公开部署或接入收费/写权限时请求额外授权。实现中的低风险命名、hook拆分、组件抽取可自主选择并记ADR/implementation notes。

不要直接运行旧 `go mod tidy` 作为无条件首步；它会改依赖。先 `go mod download` /已有锁文件模式，只有明确需要时tidy并审查diff。前端确认当前兼容版本后生成并提交单一lockfile，后续使用npm ci。
