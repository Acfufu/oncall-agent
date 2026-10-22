# 09 · 来源、证据边界与追踪

## 1. 核对情况

v1.0编制时于2026-10-05通过GitHub连接器读取main分支，HEAD为`2e97742aae610b52535999a9851683320c349c6e`；本次读取现有AGENTS并确认其ADR/ROADMAP先行要求。之前对话已经读取控制台、Planner、告警结构和RAG字段，本包据此定义增量模型。

v1.0完整读取已提供审查报告，将原始图和复现附件保留。v1.1检查旧ZIP确实只有1张原图，新增6张后共7张；更新索引、入口和逐页例外并验证文件完整性，未再次查询远端仓库。本次没有修改目标仓库，没有启动其服务，没有重跑原项目build/test/race，也没有测量LLM/数据库线上行为。

有关React Flow、Prometheus和Semantica参考查阅于2026-10-05，仅用官方文档/项目材料说明接口与设计借鉴；库版本、许可证与具体SDK方法仍由Codex实施时锁定核对。

## 2. 来源索引

| ID | 来源 | 使用位置 |
|---|---|---|
| SRC-01 | GitHub main分支读取，固定commit | baseline、执行起点 |
| SRC-02 | `reference/audit/REVIEW.zh-CN.md`及原摘录复现 | R01–R15、风险触发条件、验证限制 |
| SRC-03 | `design/reference/ui-concept-original.png` | 事件工作台及公共shell基准；图中数据不是事实 |
| SRC-04 | Semantica根README | 分清图基础设施与纯前端绘图库 |
| SRC-05 | Semantica `explorer/README.md`、此前读取的package.json | Explorer工作区、导出后独立试验、交互借鉴 |
| SRC-06 | oncall-agent固定commit的AGENTS.md | 现有结构、约定、先改ADR/ROADMAP |
| SRC-07 | React Flow官方Layouting文档 | 外置布局、ELK适配决策 |
| SRC-08 | 固定commit的internal/agent/planner.go、handler/alert.go、rag/rag.go、web/console.html | 真实运行步骤、引用字段、旧控制台数据范围 |
| SRC-09 | Prometheus官方HTTP API | query_range参数、浮点字符串/缺失值规范 |
| SRC-10 | `design/reference/01-dashboard.png` | 仪表盘概念图；只读/数值语义按第10章修正 |
| SRC-11 | `design/reference/03-evidence-graph.png` | 证据图谱概念图；全局能力仍M3 |
| SRC-12 | `design/reference/04-metrics.png` | 指标联动概念图；单位/时间/来源约束 |
| SRC-13 | `design/reference/05-knowledge-base.png` | 知识库概念图；版本/来源/管理边界 |
| SRC-14 | `design/reference/06-evaluation.png` | 评测概念图；结果证据和隔离要求 |
| SRC-15 | `design/reference/07-settings.png` | 设置概念图；凭据与危险操作修正 |

机器可读URL、查阅时间、commit等见 `../sources.json`。设计预算、限额、字段和新API是本包规范决定，不以外部来源伪装成已实现事实。

## 3. 需求追踪

| 对话需求/结论 | 本包落点 | 默认阶段 |
|---|---|---|
| 审查潜在问题 | 01可靠性清单；原始报告与复现 | M0 |
| 增加类似Semantica的图 | 02证据关系模型；04graph/Inspector；08后续试验 | M1/M2，框架接入M3 |
| 可视化不能伪造因果 | 02关系basis/verification；06 EVD/DATA测试 | 全阶段 |
| 执行时间线 | 02 run_events/attempt；04阶段展示；03events API | M1 |
| 服务拓扑与指标联动 | 03 M2 API；04联动；07源配置 | M2 |
| 文档版本/来源可追溯 | 02staging/active/历史引用；知识库UI | M0/M1 |
| 重新设计UI并参考生成图 | 原图随包；04视觉/布局/语义例外；tokens | M1/M2 |
| 每个界面出概念图并全部打包 | 7张PNG；design/screens.json；第10章逐页映射；UI-05 | 设计参考齐备，功能范围仍M0–M2 |
| 可交给Codex | CODEX_START、任务DAG、数据/API契约、验收和回滚 | 全阶段 |
| 避免过度设计 | 00非目标、阶段门槛、08延后清单 | 全阶段 |

## 4. 交付状态的准确说法

本包准备好了执行材料、机器可读夹具和验证脚本；不是已经完成软件升级。`validation/package-validation.txt`只证明包内引用、DAG与夹具检查结果，不能证明项目运行性能、视觉还原度或模型正确性。原复现结果也没有转化成“修复通过”。
