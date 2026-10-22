# oncall-agent → Evidence Workspace：Codex 交接包

**版本：1.1 · 更新日期：2026-10-05 · 交付类型：执行规范、契约、测试夹具与设计参考，不是已实现的软件。**

目标：在现有 Go oncall-agent 上，先修复可靠性与证据语义，再将文本控制台升级成“事件列表 + 证据关系图 + 来源检查器 + 执行时间线”的诊断工作台。保持只读工具边界；首版不引入图数据库、Python 服务或自动修复。

## 开始执行

将本目录放入目标仓库的 `handoff/evidence-workspace/`。先阅读并遵守仓库现有 `AGENTS.md`，再读 [CODEX_START.md](CODEX_START.md)。不覆盖根目录规则，不运行本包去修改远端仓库。

v1.0 编制时核对的 `main`：`2e97742aae610b52535999a9851683320c349c6e`，与上一轮审查基线一致。Codex 执行时必须再次记录本地 HEAD 和工作树状态；提交不同则先重核受影响结论，不强制回退到旧提交。[SRC-01]

**已收敛的默认范围：M0 可靠性 → M1 证据工作台 → M2 服务拓扑与指标。M3 全局图探索与 Semantica / GraphRAG 仅列入后续，不自动执行。**

这里的“默认”是本次交接采用的实施决定，允许用户之后明确修改，不代表用户逐条批准过所有字段或技术细节。

完整单文件规范见 [SPEC.zh-CN.md](SPEC.zh-CN.md)；执行时仍以分文件契约和任务 ID 为准。

**v1.1 已补入全部7张已生成主页面概念图。** [七页索引与逐页实施说明](docs/10_SCREEN_REFERENCE_MAP.md) · [离线图片图集](design/gallery.html)。这是包含v1.0材料的完整替代包，不是补丁包；替换旧交接目录即可。M0–M2范围不变。登录/移动端/所有子标签页和弹窗未单独出图。

## 阅读顺序

| 文件 | 作用 |
|---|---|
| [CODEX_START.md](CODEX_START.md) | 可直接交给 Codex 的执行入口与停止条件 |
| [docs/00_CONTEXT_AND_DECISIONS.md](docs/00_CONTEXT_AND_DECISIONS.md) | 范围、决策、现状与目标差异、ADR 冲突处理 |
| [docs/01_RELIABILITY_BACKLOG.md](docs/01_RELIABILITY_BACKLOG.md) | 上轮 R01–R15 全量修复映射 |
| [docs/02_ARCHITECTURE_AND_DATA.md](docs/02_ARCHITECTURE_AND_DATA.md) | 事实源、实体、幂等、版本、状态机与一致性 |
| [docs/03_API_CONTRACT.md](docs/03_API_CONTRACT.md) | 新旧 API、错误/分页/限额/认证契约 |
| [docs/04_UI_VISUAL_CONTRACT.md](docs/04_UI_VISUAL_CONTRACT.md) | 概念图复刻边界、布局、交互、响应式和设计例外 |
| [docs/05_IMPLEMENTATION_PLAN.md](docs/05_IMPLEMENTATION_PLAN.md) | 里程碑、依赖顺序、提交与并行边界 |
| [docs/06_ACCEPTANCE_TESTS.md](docs/06_ACCEPTANCE_TESTS.md) | 功能、安全、故障、契约和视觉验收 |
| [docs/07_OPERATIONS_AND_MIGRATION.md](docs/07_OPERATIONS_AND_MIGRATION.md) | 单机部署、非破坏迁移、回滚、配置和演示隔离 |
| [docs/08_FUTURE_AND_SEMANTICA.md](docs/08_FUTURE_AND_SEMANTICA.md) | M3 延后范围与 Semantica 试验边界 |
| [docs/09_SOURCES_AND_TRACEABILITY.md](docs/09_SOURCES_AND_TRACEABILITY.md) | 来源、证据限制、需求追踪 |
| [docs/10_SCREEN_REFERENCE_MAP.md](docs/10_SCREEN_REFERENCE_MAP.md) | 全7页图索引、实施范围、逐页语义例外、截图验收 |

## 配套材料

- `design/reference/`：事件工作台原图 + 仪表盘、证据图谱、指标联动、知识库、评测、设置六张新增图，共7张1586×992 PNG，保持原始字节。
- `design/screens.json`：逐图路径、SHA-256、尺寸、来源和任务映射；`design/gallery.html` 为无需联网的图片目录，不是产品原型。
- `design/tokens.css`：建议设计令牌；不是从参考图精确取色得到的事实。
- `contracts/domain.ts`、`contracts/workspace.schema.json`：前后端一致的工作区读模型契约。它们不等于全部数据库定义或全部 API 实现。
- `fixtures/*.json`：确定性的演示/契约测试数据，包含正常、无证据、数据源不可用、重试后成功、已恢复等状态；禁止线上失败时偷偷回退到这些数据。
- `plan/tasks.json`：机器可读任务 DAG 与验收 ID。
- `reference/audit/`：上一轮完整报告及摘录逻辑复现。原复现测试的 PASS 表示复现问题，不表示修复通过。
- `scripts/validate_handoff.py`：校验包内链接、夹具契约、引用关系和任务 DAG；不测试目标应用。
- `scripts/validate_screens.py`：检查7张图片的清单、哈希、PNG尺寸、任务绑定和图集入口。
- `CHANGELOG.md`：v1.0与v1.1的实际差异及未扩大范围说明。
- `validation/`：本交接包校验记录和 SHA-256 清单。

## 必须守住的约束

图是事实的可重建视图，不是独立真源。每条边须有来源；检索分数不是根因概率；候选假设不是已确认原因；运行完成不等于故障恢复；没有历史数据就不画历史趋势。原概念图中示例数字、日期、未校准的 0.72 和含混状态必须按契约纠正。

不做企业级多租户、不新增任意 Shell/Kubernetes 写工具、不用 3D/粒子替代信息、不默认接入整套 Semantica、不为了新 UI 重写 Go 后端。不自动部署、不触碰生产库、不提交密钥。
