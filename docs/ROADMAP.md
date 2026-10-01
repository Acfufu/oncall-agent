# ROADMAP

## v0.1 最小闭环 (只读，可开箱)

- [x] Gin: /ping /upload /chat /plan
- [x] Eino ReAct (time/rag/prometheus只读) + Plan-Execute (拉告警→检索→报告)
- [x] Qdrant单路召回 + 标题加权
- [x] 极简单页 (聊天+诊断按钮+引用)
- [x] upload/list/delete/reindex
- [x] eval sample 20问占位
- [x] compose: qdrant + prometheus + demo知识，Key必填

## v0.2 质量

- [x] Hybrid (BM25+RRF) + rerank
- [x] 完整控制台 (知识库管理)
- [x] 评测报告对比

> 验收关 (2026-09-22，按拍板放行)。
> - Hybrid：recall@3=1.0(30/30)；拒答0/2接受，Floor=0，v0.3再调。
> - 控制台：`/`切console.html，/v01留档；eval填实数；delete半残记v0.3。
> - rerank码合未live验，待RERANK_BASE_URL实测。

## v0.3 收口 (2026-09-26 立项)

主题：还清 v0.2 全部遗留债 + 补 MCP/OTel 验收。沙箱/企业库推 v0.4+。

- [x] 余弦 Floor 标定证伪封存：7 库无可分界（e3109bf），Floor 旋钮保留，拒答验收不挂检索层
- [x] 拒答落生成层：react prompt「引用不相关也明示未找到相关匹配」；evalbaseline EVAL_GEN 负例回归（8a9bec9，实测 2/2）
- [x] delete 删全：store 按 payload doc 过滤删点 + source 标记(demo/upload)；reindex 同步清 demo 消失/陈旧向量（33dc187，活体验收过）
- [x] rerank live 验：qwen3-vl-8b listwise rerank_recall@3=30/30，RerankFused 融合无降级（2026-09-26）
- [x] MCP/OTel 验收 (ADR-0004，首刀 119fe31 + 指标补埋 5c4cf11)：2026-09-26 实测过，ADR 转 Accepted

> 验收关 (2026-09-26 两轮实跑：首轮 LM Studio 未起为降级环境，次轮全量过)。
> - 拒答：生成层 2/2 明示未找到相关匹配（EVAL_GEN，gen_err=0）。
> - delete：upload→chat 引用命中→/delete→/list 摘除→chat 不再引用，活验通过；内部语义有 internal/rag/delete_test.go 回归。
> - recall@3：30/30（nomic-768 经 LM Studio :1234 /v1/embeddings，与 v0.2 基线同级可比；config.json embedder 已指 LM Studio，Ollama 恢复后可改回）。
> - rerank：rerank_recall@3=30/30，rerank_fallback_err=0。
> - MCP/OTel：16686 见 POST /chat→Run.run→Tool.exec:rag_search→RAG.search+ChatModel 六 span 树；9090 实测 `http_server_request_duration_seconds_count{http_route=/chat}` 与 `rag_hits_total`。旁注：Qdrant 旧 collection 为 768 维(nomic)，embedder 降级 hash 64 维时 app 按 memonly 跑（预置行为，非回归）。

## v0.4 告警驱动闭环 (2026-09-26 立项)

主题：告警进来→诊断出去。POST /alert 推送入口 + 诊断 eval 门禁 + 事件沉淀回流。依据 docs/research/2026-09-26-v040-survey.md（开源对标 + 商业趋势两路调研）。

- [x] POST /alert：Alertmanager webhook 兼容 payload，复用 Plan-Execute 同一条链，同步返回结构化诊断报告（619f970）
- [x] GET /reports：内存环存最近 20 条告警驱动诊断；console 加告警诊断区块（619f970）
- [x] compose 预置 Alertmanager + demo 告警规则：Prometheus 规则→AM→/alert 真流转（e4d0570）
- [x] 事件沉淀：诊断报告自动入库 source=incident（同题覆盖、auto_ingest 可开关、检索降权 0.5 可配）；沉淀是管线后置写入，不是 agent 工具（81dea18）
- [x] 诊断 eval：fixture 告警 9 条（7 正例 + 2 负例）规则断言，eval 前清 incident 防自证循环

> 验收关 (2026-09-26 全量活跑：LM Studio nomic-768 + qwen3-vl-8b，Qdrant 真库)。
> - AM 真流转：compose up 后 Prometheus `ContainerOOMKilled` firing → AM → /alert 自动诊断入 /reports，ingested=1；手动 curl POST /alert 同验。
> - 诊断 eval：alert_recall@3=7/7=1.0；负例生成层拒答 2/2 明示未找到相关匹配，alert_gen_err=0（EVAL_ALERT=1 EVAL_GEN=1）。
> - 事件沉淀：Qdrant source=incident 点落库（2 告警 × 3 chunk=6 点）；重推同告警点数不涨（同题覆盖）；citations 透出 source=demo/incident；/metrics 实测 `alert_diagnoses_total` 与 `incident_ingested_total`；internal/rag/incident_test.go 回归降权与按 source 清除。
> - 既有回归不破：recall@3=30/30、rerank_recall@3=30/30（fallback_err=0）、拒答生成层 2/2（gen_err=0），与 v0.3 验收同级可比。
> - 同步形态代价已记录（README 已知局限）：LLM 慢可超 AM 投递超时触发重试，demo 靠 repeat_interval=4h 缓解，job 化异步留 v0.5。

> 推 v0.5：deploy_events 变更富化、对外 MCP server（对标 k8sgpt serve --mcp）、通知写回（需 ADR 界定只读边界）、job 化异步、LLM-as-judge 评分。企业库/沙箱继续搁置（沙箱 ADR 顺延 0006）。

## v0.5 诊断队列化 + 自评分 + MCP server (2026-09-26 立项)

主题：还债 + 质量 + 分发。队列化还 v0.4 记录在案的同步债（ADR-0006），judge 升级诊断质量度量，对外 MCP server 分发三只读（ADR-0007，对标 k8sgpt serve --mcp）。拷问拍板：Redis+asynq 硬依赖、单一异步契约、自评分纯观察值、低分先标记后送达（外发拆 v0.6）。

- [x] 诊断队列化（ADR-0006）：Redis + asynq 硬依赖，compose 预置 redis；POST /alert 单一异步契约——入队秒回 202 {id,status:queued}，诊断落 /reports（queued/running/done/failed）；console 改轮询；BREAKING 写 README
- [x] 运行时自评分 + 低分标记：judge 1-5 分挂 /reports 条目，纯观察值不驱动行为；低分布尔标记 + console 高亮；judge 失败降级无分不挡主链
- [x] judge eval 门控：EVAL_JUDGE=1，alert 集 7 正例出 1-5 分，与规则断言并列进评测报告；复用主 LLM 配置
- [x] 对外 MCP server（ADR-0007）：官方 go-sdk 双传输——Gin /mcp StreamableHTTP + serve --mcp STDIO，共享 tool handler；只暴露三只读；鉴权不新设；MCP 进程不需 LLM Key

> 验收关 (2026-09-26 全量活跑：LM Studio qwen3-vl-8b + nomic-768，compose 真栈；zw 目标 v050-queue-judge-mcp 21/21 步，红绿双证 + qa-executor 逐对对照 COMPARATOR: MATCH，终验 attestation 517427896a9e81d3 在档)。
> - 队列化：compose 起 redis（healthy）；POST /alert 实回 202 {id,status:queued}，worker running→done cites=3 非空；AM 真流转（labels demo:true 毫秒级 startsAt）done ingested=1 且 AM 日志 40m 窗口零 error/retry；验收现场发现并修 redis_addr 默认——macOS localhost 解析 ::1 被 Docker Desktop 仅 IPv4 发布拒连（19fd516）。
> - 自评分：/reports 真实评分 score=1 low_score=true 与 score=3 low_score=false 并存（console 低分红色高亮消费 low_score）；无 key defaults 路径 202→done score=0 诊断正常主链不断；现场 judge 调用失败降级 score=0 活样。
> - judge eval：EVAL_JUDGE=1 7/7 出分（3,1,3,3,3,1,5 含中文理由，2 条 LOW），汇总 alert_judge_avg=2.714 low_count=2 err=0。
> - MCP：SDK 客户端双传输（/mcp + serve --mcp）各列出三只读、rag_search/time_now isError=false、write_file 协议层拒；MCP inspector 完成 /mcp 握手；Claude Desktop GUI 手验留用户侧（inspector+SDK 已覆盖协议兼容）。
> - 既有回归不破：recall@3=30/30、rerank 30/30 fallback_err=0、alert_recall_at_3=7/7、拒答生成层 2/2 gen_err=0，与 v0.4 验收同级可比。

## v0.6 通知写回 (2026-09-27 立项)

主题：把 judge 低分与 failed 诊断送达给人——ADR-0006 拆两步的第二步。ADR-0008 界定「通知不是 remediation」：outbound 单向告知，不确认不静默不处置，通知失败不挡诊断主链。拷问拍板：触发器=低分+failed 只挂异步链（手动 /chat 本人在场不通知）；渠道=通用 webhook（IM 适配留适配层不上核）；投递=asynq 第二类 task at-least-once（MaxRetry 3 退避，载荷自包含，耗尽记 notification_failed_total 不加 /reports 状态字段）；low_threshold 默认 3 不动（v0.5 eval 2 条 LOW fixture 复用为通知触发用例）；license=Apache-2.0。

- [x] 通知触发与 webhook client：worker 终态（low_score 或 failed）入 asynq 通知 task，POST /reports 条目同形状 JSON 到配置 URL
- [x] 投递保证：MaxRetry 3 + 指数退避，at-least-once 接收端按 report id 幂等，重试耗尽 notification_failed_total + 日志终态
- [x] config notify 段（webhook url，空=关闭）+ README 用法与接收端幂等说明
- [x] 验收关：LOW fixture 触发通知活验、failed 触发、断网重试恢复送达活验、license 落档

> 验收关 (2026-09-27 全量活跑：LM Studio qwen3-vl-8b + nomic-768，compose 真栈；zw 目标 v060-notify-webhook 14/14 步，红绿双证+F 活验，终验 attestation 见 .lazyzcode/attestations/)。
> - LOW 送达：POST /alert（CPUHighUsage，judge 实测打 1 分 low_score=true）→ 常驻 sink 秒收与 /reports 条目同形状 JSON（id/score/low_score/citations 一致），一次送达无重试。
> - 断网恢复：webhook 先指死端口，投递 attempt 1/4→2/4→3/4 连接拒绝（5s/10s 指数退避可见），sink 起来后第 4/4 次送达，notification_sent_total=1、failed_total=0。
> - failed 触发：worker 链全降级是 ADR-0006 锁定语义、failed 无自然生产者——panic 恢复落 failed 终态为唯一真实失败面；故障注入四例（go test -run Notify）全过（载荷 status=failed、报告永停 running 的黑洞随 N6 关闭）。
> - 回归同级：alert_recall@3=7/7、负例拒答 2/2 gen_err=0；recall@3=30/30、rerank 30/30 fallback_err=0；gofmt/vet 净；LICENSE Apache-2.0 双语链接在档。

## v0.7 变更富化 (2026-09-27 立项)

主题：deploy_events 第四只读白名单工具——告警时间窗 × 最近变更并成诊断上下文。v0.4 调研「行业验证的最高价值 RCA 信号」顺延三期后立项。拷问拍板：源=GitHub API 只读（commits+deployments 合成时间线）；repo 从 config 读、工具不设参；配置门控注册（repo 空=白名单缩回三只读、提示词不出现）；MCP 暴露跟随白名单单一事实源（ADR-0007「只暴露三只读」字面松绑）；报告 additive 字段不算 citations（拒答/eval 语义不动）；三计数器观测；不加新 EVAL 门控（ADR-0009）。

- [x] tool deploy_events：GitHub commits+deployments 只读拉取，since/until 窗口（缺省 24h）+ 各 10 条截断 + 失败降级不挡链
- [x] config deploy 段（github_repo 门控注册，token 可选）+ ReAct 提示词门控注入
- [x] /reports deploy_events 观察字段 + console 渲染 + notify 载荷透传 + 三计数器（calls/errors/total）
- [x] MCP 暴露跟随白名单（/mcp 与 serve --mcp 配置了才带第四只）
- [x] 验收关：离线单测（窗口/截断/坏参/降级/未配置不注册）+ compose 真栈活验（指 Acfufu/oncall-agent 匿名：字段非空 + Tool.exec span + 计数器非零 + 降级一例）+ 既有回归全数同级

> 验收关 (2026-09-28 全量活跑：LM Studio qwen3-vl-8b + nomic，compose 真栈 + GitHub 匿名 API 指 Acfufu/oncall-agent；zw 目标 v070-deploy-events，红绿双证+F 活验，终验 attestation 见 .lazyzcode/attestations/)。
> - 报告富化：POST /alert（startsAt=now）→ GET /reports 条目带 deploy_events 10 条真实 commits（env/sha/message/time 倒序，取窗 [startsAt−24h, startsAt] 生效），deploy_events_calls_total=1。
> - chat 链：tools 数组含 deploy_events；ReAct 实调 time_now→deploy_events，答复原样列出 10 条事件；Jaeger 查得 Tool.exec:deploy_events span（status ok）。
> - 门控两态：repo 配置态 /mcp tools/list 四只；repo 置空重启 tools/list 缩回三只（基线二进制配了 repo 仍三只，作红半）。
> - 降级一例：repo 指不存在仓库 → 报告仍 done + deploy_events 空数组 + deploy_events_errors_total=1 + slog 降级行。
> - 离线单测：tool 12 例（窗口/截断/坏参/conn-refused/非2xx/门控两态/dispatch）+ handler 3 例（附字段/降级空数组形状稳定/未配置省略）全绿。
> - 回归同级：recall@3=30/30、rerank 30/30 fallback_err=0、拒答生成层 2/2 gen_err=0、alert_recall@3=7/7，与 v0.6 验收全同数；gofmt/vet 净。

## v0.7.1 修复批次 (2026-09-29，五轮双审驱动)

全项目五轮双审（架构/约定/降级并发/安全/可观测运维/文档测试，10 评审轴 + 交叉验证，46 项发现）产出修复清单。本批修 F01-F06+F08 八项（F07 指标口径与 F09/F15/F35 拍板项留后续批次）：

- [x] F01 测试时间炸弹：deploy_events 工具形状测试 stub 日期写死×缺省窗耦合，每天 10:00Z 后必红（-count=1 才现形）——改传显式窗口（850eb87）
- [x] F02 诊断去重死代码：taskID 哈希掺随机 report_id，ADR-0006「重投不重复入队」永不可达且旧测试锁死错误断言——taskID 只哈希 alerts，断言反转（a2b10cf）
- [x] F03 启动序竞争：worker goroutine 先于 InitTracer/InitMetrics——Init 块前移至 store 装配前，启动期降级/预载 span 随之可观测（572c802）
- [x] F08 /reindex 抹 upload 注册表——sources 旁表+合并语义，demo 按目录重建、upload 保留（031a78e/cf54ff2）
- [x] F04 静默降级可见性：memOnly 闩锁与 embed hash 回退零观测——store_fallback_total/embed_fallback_total+首次转移 warn+/ping memonly 位（a64c620）
- [x] F05 绑定面收敛回环：config 默认 host 127.0.0.1+compose 六服务端口发布前缀+双语 README Linux 差异说明（4c3810a）
- [x] F06 Linux 观测半瘫：prometheus 补 extra_hosts host-gateway（2d8ee03）；.gitignore 清障同行
- [x] 行为变更记录：默认绑定从 0.0.0.0 收敛 127.0.0.1 是有意收紧（无鉴权 API 与全接口暴露不相容）；容器回访宿主在 Linux 需显式开 0.0.0.0（README 已知局限）

> 验收关 (2026-09-29；zw 目标 v071-hotfix-review-findings，红绿双证+F 活验，终验 attestation 见 .lazyzcode/attestations/)。
> - 离线全绿：go test ./... -count=1 六包全 ok（红半：修前 internal/tool FAIL deploy_events_test.go:179）。
> - 去重复活：新断言「同 alerts 异 report_id → 同 taskID」修前 FAIL 修后 PASS（红半 n51）。
> - 启动序：InitTracer:56/InitMetrics:67 均先于 worker:126 与 store:84（红半：修前 107/118 > 97 断言 FAIL）。
> - 降级可见性活验：沙盒死端口起服 → /ping 带 memonly=true、/metrics 出 store_fallback_total=1 与 embed_fallback_total=1、boot warn 降级行可见（红半：修前 /ping 无字段、零序列、无声）。
> - 绑定面活验：lsof 127.0.0.1:8819（红半 *:8819）；docker compose config 六服务全带 127.0.0.1 前缀 + prometheus 含 extra_hosts（红半：无前缀/无该键）。
> - KU#1 证真：容器内 curl host.docker.internal:8819/ping 实通（Docker Desktop 回环可达），Linux 差异已文档化。
> - 回归：gofmt/vet 净、go test -race handler/queue/tool 全绿。

## v0.7.2 清债批次 (2026-10-02 立项)

主题：收掉 v0.7.1 遗留的 F07/F09/F15/F35 四项（五轮双审 46 项发现在档部分随 v0.7.1 修复，余项清单失传——本批顺带快扫重建）。拷问拍板：F07 rag_hits_total 按 caller 维度拆分（tool=工具面 / alert=诊断主链，主链补打点，指标序列形状变化在案；evalbaseline 离线 CLI 无 /metrics 面不打点）；F09 panic 即终态 SkipRetry（panic 是代码缺陷非瞬态故障，重试大概率复发——failed 通知与终态从此一致，代价是丢瞬态恢复机会）；F35 删除零接线 MCP client 缝并修订 ADR-0004（WithMCP/execRemote/main 从未接线，YAGNI，MCP 能力收敛对外 server 双传输 ADR-0007）；F15 rag_search 工具输出透传 source 信任级。

- [x] F35：删 mcp.go/mcp_transport.go client 缝 + exec.go WithMCP/execRemote/Close 死面，ADR-0004 修订注记
- [x] F15：rag_search 工具 JSON 输出增 source 字段（demo/upload vs incident），远端解析缝同步
- [x] F07：rag_hits_total 增 caller 维度（tool/alert），planner.diagnose 主链补打点
- [x] F09：ProcessAlertDiagnosis panic 落 failed+通知后 SkipRetry 上抛，不再退避重试
- [x] 快扫重建遗留清单落档 docs/research/2026-10-02-v072-review-findings.md（原 F10-F22 详情失传，重建清单改 R 前缀编号：R01-R12，抽查 3 项 file:line 实证）
- [x] 验收关：TDD 红绿逐项 + 回归门（gofmt/vet/test -race）+ 收口写回

> 验收关 (2026-10-02 四项 TDD 红绿 + 全量回归；commit 7914c67/3255088/d2919cc/ce1d9cd)。
> - F35：mcp.go/mcp_transport.go 整删，`go build ./...` 过，全套件 -count=1 绿；ADR-0004 Amended 注记 + AGENTS.md 同步；仓内零 `MCPConfig|WithMCP|NewMCPClient` 残留引用。
> - F15：红半「item missing source field」（rag_tool_test.go:44）→ 绿：工具 JSON 四字段含 source，demo+upload 双源断言。
> - F07：红半「主链检索后 metrics 零 rag_hits 序列」→ 绿：promhttp 实抓 `rag_hits_total{caller="alert"} 3`；exec 面同参改传 caller="tool"。指标序列形状变化（增 caller 标签）在档。
> - F09：红半「err 不含 SkipRetry」→ 绿：`errors.Is(err, asynq.SkipRetry)`；既有 TestNotifyOnPanicMarkFailed（failed 落态+通知）不破；queue/maybeNotify 过时注释同步。
> - 回归门：`gofmt -l .` 空、`go vet ./...` 过、`go test ./... -count=1` 七包 ok、`go test -race -count=1` handler/queue/tool/agent 四包 ok。
> - 快扫重建：R01-R12 落档（P1 一项：embed 维度错配级联；P2 四项；P3 七项），三项重灾发现 file:line 抽查核实，确认干净面同档。

## v0.7.3 降级治理批次 (2026-10-02 立项)

主题：清 v0.7.2 快扫清单（docs/research/2026-10-02-v072-review-findings.md R01-R12）的降级路径可信主题——降级要么如实报错、要么可见可测，不许静默。R01/R02/R03/R04/R05 同属「降级要么报错要么可见」，R10/R12 小修捎带，R09/R11 文档漂移顺手清。R06/R07/R08 留后续批次。

- [x] R01：embed 维度错配级联拆弹——boot 探测诚实化（降级时拒建 64 维 collection）+ 混维度内存搜索守卫
- [x] R02：Prom 不可达分型——Firing 吞错改显式错误，/plan 输出「告警源不可达」不再假阴性
- [x] R03：chat LLM fallback 观测——触发日志 + chat_fallback_total 计数器（InitMetrics 幂等化改为测试包共享 once-helper，语义不变更轻）
- [x] R04：judge 出站 HTTP client 超时注入（默认 30s）
- [x] R12：LLM 响应先查状态码再 decode + parseAlertPayload 死分支清理
- [x] R10：ProcessNotification 无任务上下文（ok=false）独立分支，不污染 notification_failed_total
- [x] R05：store 降级状态机测试补齐（store 包首测）
- [x] R09+R11：.env 死变量清障 + README 配置表 127.0.0.1 + 版本串统一 v0.7.3
- [x] 验收关：TDD 红绿逐项 + 回归门（gofmt/vet/test -race 全量）+ 收口写回

> 验收关 (2026-10-02 八项 TDD 红绿 + 全量 -race 回归；commit 7a7aa27/e94d07e/f6a3c47/7bb21af/c4aa11c/c43ff3e/7ea4c90/83202c9)。
> - R01：红半「8 维点以假分 1.0 混入 4 维查询」（store 包首测）→ 绿：维度不符点跳过；boot 机械断言 probeReal×3（修前 0）——探测降级拒建/重建 collection，warn 明示 memonly 待重启恢复。语义变更在档：离线 boot 不再产出 64 维毒丸 collection，embedder 恢复+重启后自动重建。
> - R02：红半「死端口 Prom 诊断文案=无告警假阴性」→ 绿：Firing 三分支显式错误（span 记 error），planner 输出「告警源不可达请人工检查」。
> - R03：红半「LLM 500 后 metrics 无 chat_fallback_total」→ 绿：fallback warn 带原始 loop 错误 + 计数器=1；agent 包测试共享 initTestMetrics 防重复注册。
> - R04：红半机械断言（judge.go DefaultClient×1/零超时 client）→ 绿：30s 共享 client + 换装 50ms 打 300ms 慢端点实得超时错误 + 正常路径回归。
> - R12：红半「502 HTML 报 decode llm resp 掩盖状态」→ 绿：先查状态码（JSON error.message 优先原文兜底）；parseAlertPayload 死错误分支清理（机械红 perr×2→0，签名收窄）。
> - R10：红半「无任务上下文投递失败污染 notification_failed_total=1」→ 绿：ok=false 独立分支仅告警不计数，注释与代码对齐。
> - R05：store 降级状态机锁定——死库建库/首写闩锁 memOnly、读己之写、删除如实清内存、单实例不回切。
> - R09+R11：.env.example 仅 OPENAI_API_KEY（死变量清障）；README 双语配置表/启动示例 127.0.0.1；版本串四处（v0.1/v0.3.0/v0.7.0/v0.4）统一 observability.ServiceVersion=v0.7.3。
> - 回归门：`gofmt -l .` 空、`go vet ./...` 过、`go test ./... -count=1` 八包 ok、`go test -race -count=1 ./...` 八包 ok。
> - R 清单余项：R06（PlannerAgent 懒构造）/R07（请求体上限+Server 超时）/R08（会话表无界）留后续批次，见 docs/research/2026-10-02-v072-review-findings.md。

## v0.7.4 尾批 (2026-10-02 立项)

主题：R 清单归零——快扫余下三项 P3（R06/R07/R08）。拷问拍板：R08 会话治理=加 `DELETE /session` 清空端点（CONTEXT 词条「可清空」兑现，AGENTS API 约定面同步）+ LRU 上限 256；v0.8 /reports 持久化方向拍板=JSON 快照（零新依赖，立项时另补 ADR 详设）。

- [ ] R06：PlannerAgent 懒构造 check-then-act 消除——改局部构造不回写共享字段，-race 并发测试红绿
- [ ] R07：/alert /upload /chat 请求体 2MB 上限 + http.Server ReadHeaderTimeout/ReadTimeout（WriteTimeout 有意不设：同步 /chat 经 LLM 可达分钟级，写死会杀在途对话，回环绑定下文档化）
- [ ] R08：ReAct 会话 LRU 上限 256 + DELETE /session 清空端点 + AGENTS/CONTEXT 同步
- [ ] 验收关：TDD 红绿逐项 + 回归门 + 收口写回

## v0.8+ 愿景

- [ ] /reports 持久化（重启丢历史+环驱逐；v0.6 通知载荷自包含后刺已钝，重开需 ADR）
- [ ] MCP 鉴权（维持网络层防护口径，有外部客户端真依赖时再立 ADR）
- [ ] rerank live（正交质量项，自带一套验收负担，不搭车）
- [ ] 企业级知识库 (版本/权限/去重)
- [ ] 沙箱执行 (默认关，另立 ADR，顺延 0010)
