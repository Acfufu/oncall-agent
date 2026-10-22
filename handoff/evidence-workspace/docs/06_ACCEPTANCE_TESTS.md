# 06 · 验收与测试计划

## 1. 测试等级与证据

L0：静态检查/契约/纯函数；L1：fake LLM、fake HTTP、临时SQLite的业务测试；L2：一次性Redis/Qdrant/Prometheus环境集成；L3：Playwright UI与视觉对照；L4：可选真实模型/真实受控数据验证。L0/L1成功不能写成L2/L4已完成。

本包只校验了交接材料本身；目标项目的测试由Codex执行。付费模型测试默认不执行，除非已有明确可用测试配置和用户授权预算。历史 `reference/audit/repro` PASS是缺陷复现，不是修复验收。

## 2. 必测场景

| ID | 场景 | 判定 |
|---|---|---|
| SEC-01 | 无凭据访问chat/upload/graph/evidence/mcp；AM token访问管理API | 拒绝；有效身份正常路径可用；缺key fail closed |
| SEC-02 | 跨站cookie写请求、Markdown脚本/危险链接、metrics任意URL | 被阻断；原文作为文本可读；不执行注入内容 |
| SEC-03 | 两浏览器聊天及伪造session_id | 默认无状态，无跨会话历史；启用历史时严格绑定身份 |
| REL-01 | 50并发相同告警重投、map/数组换序、随机report_id变化 | 同一business key只一条run/outbox；同id响应；人工复诊幂等；跨桶fallback限制有测试 |
| REL-02 | upload→reindex demo→重启→list/search/delete | 自定义文档始终可管理；删除后当前检索无残留 |
| REL-03 | V1→V2短文；写向量中断；激活后立即崩溃 | 当前只查到活动版本，失败保留V1，旧报告仍引用V1 |
| REL-04 | embed超时/不同模型同维度/不同维度/Qdrant500 | 不混用空间、不永久memOnly、不报durable success |
| REL-05 | 配置embedding维度与旧collection不匹配启动 | 不调用DELETE；清晰报错/迁移提示；旧库完整 |
| REL-06 | 业务库sentinel存在，运行eval与清理 | 业务sentinel、计数、配置不变；只处理本次eval自建资源 |
| REL-07 | Prometheus超时/500/非法JSON/真正空列表 | 前三者source_unavailable，最后才是no_alerts；日志/状态一致 |
| REL-08 | notification入队失败一次、发送后崩溃、两个慢诊断 | 仍可恢复，payload含完整原告警；不重复诊断；独立通知worker能推进 |
| REL-09 | MCP初始化、带同session_id的GET/SSE、POST、关闭 | transport会话一致，鉴权不破坏正常握手 |
| REL-10 | 超大body/告警批次、客户端取消、worker超时 | 明确413/429/timeout；取消后不再启动后续embedding/写入 |
| EVD-01 | 模型不检索但建议修改配置；免责声明+操作文本；历史有引用本轮无 | 生产handler路径不放行建议，稳定no_evidence安全结果 |
| EVD-02 | 两条告警不同来源、引用不存在chunk、图混入另一个run | 不串线；无效引用被拒；每条可见边可追到source_ref |
| EVD-03 | 无引用/低分/未评分报告、同一事件自引用 | 不进入默认可信检索；保存报告仍正常；source与verification分开 |
| DATA-01 | succeeded诊断但告警仍firing、纯resolved、迟到firing | run不代表恢复；恢复不产生新诊断；迟到消息不回滚较晚状态 |
| DATA-02 | run后文档更新/撤下；查看旧图/旧片段 | 使用旧版本快照，明示历史/撤下；不重新检索替换旧证据 |
| DATA-03 | admission提交后未投递崩溃、过期worker晚返回、重复事件 | outbox可恢复，栅栏保护终态，事件sequence去重，费用exactly-once不作承诺 |
| API-01 | 新旧DTO、空/null/未知状态、分页换过滤 | 契约通过；缺数据非0；旧done/score兼容；错误可见 |
| UI-01 | 主视口与原图对比 | 侧栏/三列/底部结构与深色视觉保留；不是背景图；语义例外有记录 |
| UI-02 | 每个可见按钮、无权限/无来源/错误/演示模式 | 所有动作可执行或明确禁用；演示永不混入live；无硬编码运营数字 |
| UI-03 | 390px、768px、1280px；键盘/减弱动画/灰阶 | 能完成选事件→证据→来源；不靠hover/颜色唯一辨认；无全页横向挤压 |
| UI-04 | A→B快速切换、旧请求晚回、断线/恢复/后退 | 不串事件，保持选择/视口，stale显示上次成功时间 |
| UI-05 | 按screens.json逐一打开7张原图并核对对应页面 | 提交7页截图/能力状态与语义例外记录；不复用单页截图冒充全页；未接入/只读/deferred明确可见；不将图片虚构功能扩成需求 |
| VIS-01 | 检索失败后重试成功、judge关闭、通知pending | 阶段终态自洽；retry详情可见；skipped不算成功完成 |
| M2-01 | 拓扑循环/未知服务/跨环境/无来源 | 结构/namespace校验；声明/观测可区分；只称潜在影响 |
| M2-02 | query_range缺点/NaN/Inf/超窗口/超series/timeout | gap/null不是0；拒绝超预算；显示时间窗/step/来源；不暗示因果 |
| QA-01 | 相同用例经过真实/chat与/alert服务路径 | 不使用独立提示词替代；断言业务结果而非关键词；固定模型fake可重复 |
| PERF-01 | 默认60节点120边、上限300节点600边、长列表 | 测量UI交互/布局/内存，不每tick重布局；截断与降级明确 |
| OPS-01 | 干净目录启动、升级、备份恢复、UI回退 | 不丢业务数据，不删旧collection，AM回调路径有效，外部端口不意外暴露 |

## 3. 命令参考

先确认仓库工具链要求（现基线Go1.25）。以下是目标实施后应运行的命令，不声称当前环境已运行：

```bash
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
go build ./...
# 集成测试仅用实施后新增的、隔离命名空间配置
# 旧eval完成T02前，不运行EVAL_ALERT/EVAL_JUDGE
```

前端在实现后提供并文档化：

```bash
cd web/app
npm ci
npm run typecheck
npm run lint
npm run test
npm run build
npm run test:e2e
```

若race或浏览器依赖不可用，记录具体工具错误、受影响项与替代覆盖，不把跳过写成PASS。修改UI不要求无意义的100%覆盖率，但关键操作/错误路径必须走浏览器。

## 4. 视觉基线与截图

fixture模式固定时钟、时区、随机种子、run_id与layout输入；关闭动画，等待fonts-ready和graph-layout-ready。拍摄原始视口1586×992、1440×900、1280×800、390×844。

七个主页面分别对照第10章与各自原图记录截图、实现范围和差异。另至少截图：正常证据图、选中引用检查器、no_evidence、source_unavailable、retried_success、resolved、窄屏证据列表/抽屉、知识版本删除状态、M2缺数据与真实样本。

原概念图不是数值正确的golden fixture：先记录04文档列出的语义差异。自动diff用于发现偏移，不设“任意小百分比通过即全正确”的伪门槛。人工逐项查布局、字体、节点标签、source绑定、时间线和移动路径。截图路径写入verification，不用渲染成功代替截图目检。

## 5. 性能目标（设计预算，尚未测量）

在记录过硬件/浏览器的桌面测试环境，默认60节点图布局目标<500ms，节点选中到Inspector可交互目标<100ms；过滤不阻塞主线程长时间，300节点时可分批/Worker布局。未达到则先限制邻域/重布局次数，不能删引用骗快。

时间线按事件分页，长列表虚拟化按实际需要引入；首次只加载当前路由的图/指标库，不把Sigma、ECharts和所有编辑器一次打包。离开路由释放resize observer、layout worker、计时器与fetch。性能数字须输出实测，不写宣传指标。

## 6. 交付证据结构

`docs/execution/evidence-workspace/verification.md`逐测试列：test_id、命令、环境、时间、PASS/FAIL/BLOCKED、日志文件、截图、已知限制。T00–T23也记录commit/file路径。未配置真实模型可以交付fake验证版，但必须明确没有经过真实模型质量评估。
