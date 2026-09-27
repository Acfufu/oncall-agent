# ADR 0009 — deploy_events 变更富化：第四只读白名单工具（v0.7）

- Date: 2026-09-27
- Status: Accepted（2026-09-27 v0.7 立项拷问拍板；承接 v0.4 调研商业路排 2「行业验证的最高价值 RCA 信号」，顺延三期后立项；沙箱 ADR 编号让位改顺延 0010）

## Context

v0.4 调研（docs/research/2026-09-26-v040-survey.md）把「变更富化」列为行业验证的最高价值 RCA 信号，v0.4→v0.5→v0.6 三期顺延。诊断上下文至今只有知识 + 指标：告警 fire 时「最近改了什么」这条最高频根因线索缺席。ROADMAP 挂闸门「需先定真实变更源」，v0.7 立项拷问逐分支拍板（源/工具语义/配置门控/报告落形/可观测/验收），本文锁形状。

## Decision

- **变更源：GitHub API 只读拉取**。commits + deployments（GitHub deployments API，Argo 等 CD 实际上报端点）合成一条时间线；GitLab/Jenkins 等留后续适配源，不上核。
- **工具语义：`deploy_events`**。repo 从 config 读，工具不设 repo 参数——防 agent 把它变成任意仓库探测器，作用域在配置层锁死；参数仅 `since`/`until`（RFC3339 可选，缺省最近 24h，告警诊断按 startsAt 取窗）；两类事件各 10 条截断防 token 失控；无缓存直调，失败返回错误文本由 agent 自行降级，不挡诊断主链（与 prometheus_query 同缝）。
- **配置门控注册**：config 新增 `deploy` 段 `{github_repo, github_token}`（token 可选，public 仓库匿名即可）；repo 空 = 工具不注册——白名单缩回三只读、ReAct 提示词不出现、MCP 不暴露。
- **MCP 暴露跟随白名单（单一事实源）**：ADR-0007「只暴露三只读」字面由本 ADR 松绑为「只暴露白名单内工具」——配置了 repo 的进程 /mcp 与 serve --mcp 自动带第四只，不维护两份清单；白名单外一律拒绝、鉴权不新设口径不变。
- **报告落形：additive 观察字段，不算引用**。/reports 条目新增可选字段 `deploy_events`（env/sha/message/time 扁平数组，未调用或空查得缺省/空数组），纯观察值（与 score 同地位）；不并入 citations——citations 语义锁「人工审定的知识片段」不动，拒答判定与 eval 断言不受搅动；知识无匹配仍明示拒答，deploy 事件照常入字段作旁证。notify webhook 载荷与 /reports 同形状，字段 additive 透传，按 report id 幂等的接收端无感。
- **可观测：三计数器**。`deploy_events_calls_total` / `deploy_events_errors_total` / `deploy_events_total`（成功返回的事件条数），加既有 Tool.exec span；错误面 span status + 错误文本进诊断上下文 + slog 三重可见。
- **边界重申**：仍是只读工具，不是 remediation——富化只改诊断上下文，不确认不静默不处置。

## Alternatives

- 通用 POST /deploy 推送入库 + tool 查询（对标 /alert 形态）：任何部署系统可推，但新增写端点 + 存储 + 需有人真推，体量近 v0.4 告警链，拆后续版本，拒。
- k8s API 只读：本仓库 compose 环境，无 k8s，投机，拒。
- repo 开放为工具参数：灵活但白名单语义稀释为任意仓库探测面，拒。
- 注册但调用报「未配置」错误：无 GitHub 的用户每轮诊断多一次浪费调用、提示词被无用工具污染，拒。
- 本地缓存抗限流：多一个状态面；匿名 60 次/时对告警驱动调用量富余，超限走降级缝，拒。
- deploy 事件并入 citations：拒答判定与 alert_recall/负例 eval 断言全要动，引用语义被机器产出污染，拒。
- 只写诊断正文不落结构化字段：不可机读，console/通知消费不到结构化变更信号，「富化」打折，拒。

## Consequences

- 四片：ADR+词条 / tool deploy_events+config 门控 / report 字段+console 渲染+三计数器 / 验收（离线单测 + compose 真栈活验）。
- 出站新依赖 api.github.com：无新入站端口；匿名限流 60 次/时，私有库需 token；GitHub 不可达时诊断降级为无变更上下文（不挡链）。
- 白名单门控默认关（config 空值），全量 eval 与 v0.6 验收同级可比。
- 沙箱 ADR 编号顺延 0010（0009 由本 ADR 占用）。
