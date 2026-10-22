# 给 Codex 的执行入口

你正在维护 `Acfufu/oncall-agent`。请按本交接包完成增量升级，而不是从零创建一个同名项目。目标是可靠、证据优先的 Oncall Evidence Workspace。除非用户另行改变范围，执行 M0、M1、M2，在每个里程碑交付可启动切片；M3 只保留文档，不实现。

## 先读取

1. 仓库 `AGENTS.md`、`CONTEXT.md`、`docs/ROADMAP.md`、相关 ADR，以及当前变更。
2. 本包 `README.md` 与 `docs/00_CONTEXT_AND_DECISIONS.md`。
3. `docs/01_RELIABILITY_BACKLOG.md`、`docs/02_ARCHITECTURE_AND_DATA.md`、`docs/03_API_CONTRACT.md`。
4. **实际打开全部7页概念图**：先按 `design/screens.json` / `design/gallery.html` 查看事件工作台与六张分页面原图，阅读 `docs/04_UI_VISUAL_CONTRACT.md` 和 `docs/10_SCREEN_REFERENCE_MAP.md`。事件工作台校准公共shell；实施每页时必须对照该页图片，不能其余六页凭空设计。
5. `docs/05_IMPLEMENTATION_PLAN.md`、`docs/06_ACCEPTANCE_TESTS.md`、`plan/tasks.json`。

## 第一次操作

- `git status --short`、`git rev-parse HEAD`；记录当前 Go/Node/npm 版本、现有测试命令与依赖。
- 不覆盖用户未提交改动，不创建与现有结构平行的第二套后端，不执行 `git reset --hard` 或生产清库。
- 核对旧审查基线 `2e97742aae610b52535999a9851683320c349c6e`。对 R01–R15 分别记录：仍存在 / 已修复且有测试 / 当前不适用 / 待验证；不能把静态审查当作已跑集成测试。
- 先建立项目内 `docs/execution/evidence-workspace/`，写入 baseline、task-status 和 verification 记录。将本包任务 ID 作为追踪 ID，不必创建重复的任务系统。
- 仓库旧规则要求选型变更先改 ADR。本需求已扩展范围：先新增后续可用编号的 ADR，并更新 ROADMAP、CONTEXT、AGENTS 相关段落。不得无说明地绕过旧约定，也不得因为“原路线图没列”而拒绝本次已明确提出的升级。

## 执行原则

保留 Go / Gin、原有 Agent 和 Redis / Qdrant / Prometheus / OTel；增加 SQLite 作为单机业务事实持久层。前端采用 React + TypeScript + Vite，事件证据图用 React Flow + ELK；ECharts 在有真实指标接口后按需接入。构建后由 Go 托管，不增加生产 Node 服务。

先解决 M0 的数据破坏、静默降级、幂等和证据保护，再接真实数据可视化。可以并行做带“演示数据”标签的 UI 切片，但不得用演示成功状态掩盖后端缺陷。新的契约字段、SQL 和前端 adapter 由同一接口任务先冻结。

用户界面需保留概念图的深色海军蓝、共同左导航/顶栏、各页特定内容布局；事件工作台保留事件列、中央证据画布、右检查器、底部时间线。不是泛化 BI 大屏；不是整张图片的 HTML 包装；不得仅完成不可交互截图。

v1.1新增图只补齐视觉输入，不解锁自动处置、生产清库、企业权限平台或M3全局图。严格遵守第10章逐页修正：移除自动关闭/重启行为、无依据置信度、无凭据保护的配置表单；评测百分比与秒数分开绘制。图片中的虚构运营数值不得变成线上常量。

所有生产图的节点/边、耗时、来源、评分和状态必须来自业务记录。无证据时返回确定性安全结果，不保留未经支持的操作建议；工具仍只读。M1 只连接告警、真实检索、文档版本和报告；指标与假设节点仅在已有真实来源时显示。

遵循既有原子提交习惯，每项修复附最小对应回归。独立测试环境验证 Redis/Qdrant/Prometheus；禁止直接跑尚未隔离的旧 eval 命令。固定模型的摘要、judge 或检索分数都不能冒充正确率。

## 最终需要交付

- 能运行的 Go 服务 + 构建好的新前端 + 显式 demo 模式。
- 新旧接口兼容测试、文档版本/索引恢复、告警幂等与持久运行记录。
- 事件图、可访问证据列表、来源检查器、时间线；M2 增加声明式拓扑与受限指标查询。
- 全7页参考视口截图/能力状态清单（UI-05）与窄屏截图、逐页视觉例外列表、真实测试日志，以及未完成事项；M3全局图只展示deferred说明，不扩大范围。
- README 启动、配置、迁移、备份和回滚说明；代码与文档同时更新。

## 自主决策与停止条件

低风险细节按文档默认决定并记录，不反复询问已决定问题。缺真实 LLM key 或外部服务时，用显式 fake/integration profile 继续不受阻的任务；将未实测项标记 blocked，不能编造“全部通过”。

仅在需要删除/覆盖现有业务数据、公开部署、采购外部服务、接入新的写操作，或发现无法兼容的用户改动时停止相关任务并说明冲突。不得用“需要更多讨论”替代可执行的默认实施。

每个里程碑汇报：提交/文件、实际执行命令与结果、截图、剩余风险、下一批任务。最终区分 `implemented / verified / blocked / deferred`，不要只说“完成”。
