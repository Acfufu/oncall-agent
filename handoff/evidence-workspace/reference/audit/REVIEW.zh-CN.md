# oncall-agent 源码审查与修复验收清单

审查日期：2026-10-05  
仓库：`Acfufu/oncall-agent`  
分支：`main`  
固定提交：`2e97742aae610b52535999a9851683320c349c6e`

## 结论与证据边界

建议先发布可靠性修复版本，再扩大功能范围。当前最突出的风险不是缺少更多 Agent 能力，而是数据生命周期、故障语义、业务幂等与拒答保护没有形成一致的可验证契约。

本报告依据通过 GitHub 连接器读取的实际代码，覆盖 HTTP 装配、chat/planner、知识库、向量存储与 embedding、告警队列、通知、MCP 装配、控制台核心请求与评测程序。对 MCP 会话行为还核对了项目锁定的 `modelcontextprotocol/go-sdk v1.8.0` 源码。

验证方式：静态控制流审查，以及四个摘录逻辑的离线 Go 复现。没有运行完整仓库的 build/vet/test/race，没有连接用户的 Redis、Qdrant、Prometheus、LLM 或 webhook，也没有修改远端仓库。运行环境是 Go 1.23.2；原仓库要求 Go 1.25.0。附带的独立模块只用于测试摘录的标准库逻辑，不代替原项目的集成测试。

P1：建议在承载真实业务或向其他用户开放前修复。网络暴露、共享访问等触发条件会单独写明。P2：可靠性与契约完善项，按实际启用功能安排。没有把本次审查包装成已经完成的生产渗透测试，也没有宣称依赖无漏洞。

涉及删库、重建、故障注入的复现只能在一次性测试 collection 与假服务上执行。

## 问题总览

| ID | 级别 | 问题 | 主要位置 |
|---|---|---|---|
| R01 | P1，网络可达时 | 未鉴权的知识库写接口与高成本入口 | cmd/server/main.go；config/config_template.json |
| R02 | P1，共享访问时 | 所有默认聊天共享同一会话 | handler/chat.go；agent/react.go；web/console.html |
| R03 | P1 | 随机报告 ID 进入任务哈希，告警去重失效 | queue/queue.go；handler/alert.go |
| R04 | P1 | reindex 清注册表但保留上传向量，产生不可管理文档 | handler/docs.go；handler/handler.go |
| R05 | P1 | 同名上传保留旧版本 chunk | handler/docs.go；rag/rag.go |
| R06 | P1 | 后端/embedding 故障被掩盖为成功，并混用向量空间 | rag/embed.go；store/qdrant.go |
| R07 | P1 | 启动时自动删除维度不匹配的整个 collection | cmd/server/main.go；store/qdrant.go |
| R08 | P1 | 评测复用业务 collection 并删除 incident 数据 | cmd/evalbaseline/main.go |
| R09 | P1 | 无引用只加拒答前缀；独立评测误判“拒答成功” | agent/react.go；agent/planner.go；cmd/evalbaseline/main.go |
| R10 | P1 | Prometheus 故障被描述成没有 firing 告警 | agent/planner.go |
| R11 | P2 | 未经校验的报告自动回流知识库 | handler/alert.go；rag/rag.go |
| R12 | P2 | 通知入队失败可丢失；失败通知缺上下文；并发不隔离 | handler/alert.go；queue/queue.go |
| R13 | P2 | MCP GET/POST 使用不同的有状态 transport handler | cmd/server/main.go；mcpserver/server.go |
| R14 | P2 | 请求规模与端到端取消边界不完整 | cmd/server/main.go；handler/alert.go；rag/embed.go；store/qdrant.go |
| R15 | P2 | 测试断言与实际业务路径偏离 | queue/queue_test.go；cmd/evalbaseline/main.go |

## R01：网络可达时，调用者可以直接修改知识库

**依据。** 模板将服务绑定到 `0.0.0.0:8819`。Gin 装配只有 trace 与 recovery 等中间件；`POST /upload`、`DELETE /delete`、`POST /reindex`、`POST /alert` 和 `POST /chat` 没有鉴权。Docker Compose 的多个基础服务也以未指定 loopback 地址的端口映射发布。

README 已声明 MCP/HTTP 网络防护由部署层负责；这不是被遗漏的文档。但“Agent 工具只读”不能保护知识库写接口，也不能限制其他人触发 LLM 消耗。

**最小修复。** 本机模式默认监听 loopback；需要容器回调时使用明确的受限网络路径。共享访问前为管理、聊天、告警入口配置 token/网关认证，区分管理凭据与告警推送凭据，并增加请求限额。MVP 不必立即引入复杂权限系统。

**验收。** 无凭据的 upload/delete/reindex/alert/chat 被拒绝；正常凭据允许既定用途；文档明确反向代理与网络边界。

## R02：默认会话跨用户串线

**依据。** `Chat` 与 `ReAct.Run` 都把空 `session_id` 替换为 `default`。全局 ReAct 实例以 `sessions[sessionID]` 保存历史。控制台 `/chat` 请求只发送 message，不发送 session_id。因此两个浏览器使用控制台，也会进入同一段历史。

**影响。** 之前用户的故障信息会被装进之后用户的模型请求，产生数据暴露与上下文污染风险；客户端可直接指定 session_id，也没有与身份绑定。map 的锁只保证读写安全，不提供会话授权；同会话并发请求还可能覆盖彼此历史。

**最小修复。** 服务端生成不可预测会话标识；共享部署时绑定调用者身份。真正单用户无状态 MVP 可直接关闭历史，不必维护全局 default 会话。限制会话总数、存活时间与历史 token 数量。

**验收。** 两个浏览器的后续请求不包含彼此历史；不允许跨身份选择 session_id；同会话并发不会静默丢掉一轮对话。

## R03：告警去重键包含每次变化的报告 ID

**依据。** `Alert` 每次请求生成 `newReportID()`；`EnqueueAlertDiagnosis` 将 `ReportID + Alerts` 一起序列化，然后对整个 payload 调用 `taskID(payload)`。相同 alerts 的重投，因为 ReportID 不同，得到不同 TaskID。

`queue_test.go/TestTaskIDStableAndSensitive` 甚至要求只改变 report_id 时 TaskID 必须变化。它验证了哈希敏感性，却没有验证业务去重。

**最小修复。** 分离诊断业务键、任务 ID、报告 ID。用规范化告警身份及明确的事件/时间窗口生成稳定业务键，数组顺序不应决定身份。重复请求返回原 reportID/已接受状态，不应制造失败记录或重试风暴。

**验收。** 同告警不同 HTTP 请求 ID、不同 reportID、不同告警数组排列不会产生重复业务任务；新的告警生命周期或业务允许的复诊时间窗口可以产生新任务。

**本次离线验证。** 已用附带 Go 复现计算：相同 alerts、不同 report_id 得到不同 TaskID。未对真实 Redis 进行入队实验。

## R04：reindex 后，上传文档仍可检索但无法管理

**依据。** `loadDemo` 只删除 `source=demo`，按设计保留 upload。随后 `resetTitles(fresh)` 却把完整 `h.titles` 替换为仅含 demo 的 map。`Delete` 又要求 `hasTitle(title)` 成功，否则直接 404。

**触发。** 上传自定义文档 → reindex → /list 不再包含该标题 → /delete 返回 404，但上传向量与 BM25 条目仍然存在。

启动同样只回灌 demo；旧上传数据在 Qdrant 中存在时，新的注册表与 BM25 镜像没有相应恢复。incident 入库也没有进入当前标题注册流程。

**最小修复。** 按 source 更新注册信息，而不是重置整张表；为文档建立一个持久化真源，并在启动恢复注册表/BM25。删除资格不应取决于一张会丢失的临时索引。

**验收。** 上传→reindex→重启后仍可列出、检索、删除；删除后稠密、稀疏、注册表三者一致。

## R05：同名上传不是替换，旧处置步骤会残留

**依据。** `Upload` 直接调用 `RAG.AddDoc`。point ID 包含 `doc + chunk序号 + snippet内容`；内容变化就产生新 ID。AddDoc 没有清理同名旧版本，注册表却只保存最新正文。

**影响。** 用户修正 runbook 后，旧步骤仍可能被检索，出现“列表是一篇，检索是多个版本”的状态。不是密码学哈希碰撞问题，而是更新语义缺失。

**最小修复。** 明确接口为创建还是替换。最低限度提供文档级 replace 语义；更稳妥的轻量实现是先写新版本、成功后切换活动版本，再清旧版本。不要把简单“先删后写”当作已完成原子性保护。

**验收。** 同标题上传 V1→V2 后旧内容不再可召回；缩短文档后多余旧 chunk 被清理；嵌入/写入中途失败不会破坏最后一个成功版本。

**本次离线验证。** 验证了同文档同 chunk 位置改变正文会得到不同 ID；完整上传残留链路来自静态控制流审查，未冒充已跑 HTTP 集成测试。

## R06：故障被隐式转为“成功”，还会混用向量空间

**依据。** 两种远端 Embedder 在请求失败后均返回 `HashEmbed(text), nil`，得到 64 维向量。VectorStore 的 Upsert/Search 遇到 Qdrant 错误会设置 `memOnly=true`，返回 nil 或内存检索结果，没有恢复路径或补写机制。`cosine` 按两个向量较短长度计算，不拒绝维度不一致。

**故障链。** 已有真实 embedding → embedder 短暂失败 → 查询或写入变为 64 维 hash → Qdrant 不接受不匹配维度 → 服务永久切到内存 → 后续写入仍报成功，但没有落到 Qdrant。进程重启后这些内存写入丢失。删除也可能只影响退化后的内存而不影响旧持久化数据。

**最小修复。** 显式区分 demo/offline 与 persistent 模式。持久化模式远端 embedding 失败应报错，不允许单次调用切换语义空间；所有向量必须校验模型标识、维度、版本。持久化写入失败不能伪装为 durable success。依赖健康与恢复重建策略必须可见。

**验收。** 真实 embedding 请求失败不会悄悄得到另一模型的向量；写失败被调用者感知；恢复后无需靠重启丢弃状态；不同维度向量被拒绝。

**本次离线验证。** 原 cosine 逻辑对 `[1,0]` 和 `[1,0,1000]` 返回 1。这不是有效的不同维度语义向量比较。

## R07：启动参数变化可触发整库删除

**依据。** main 探测到已有 collection 维度与新的非 hash embedding 维度不同时，直接调用 `RecreateCollection`。该函数向整个 collection 发送 DELETE，再创建新 collection；删除错误也被丢弃。后续启动回灌只覆盖 demo，不恢复上传文档和事件沉淀。

**最小修复。** 普通启动遇到不匹配应 fail closed 并给出迁移提示；重建必须是显式管理操作。需要变更模型时创建新 collection，重建并校验后切换，保留旧库以便回滚。

**验收。** 配置成不同维度后启动不会删除原库；迁移失败时旧数据仍然可用；只有明确授权的维护命令可以执行破坏性删除。

## R08：评测会删除业务事件沉淀

**依据。** evalbaseline 读取相同的 `config/config.json` 并使用其中 Qdrant collection。`runAlertEval`、`runJudgeEval` 为避免自引用调用 `DeleteSource("incident")`。因此 README 推荐的告警评测命令，在复用业务配置时会删除真实 incident 数据。

**最小修复。** 评测默认创建独立、带随机后缀的 collection；结束时仅清自己创建的命名空间。禁止评测使用业务 collection，不能只增加一段警告文字。

**验收。** 在业务库预先放入 sentinel incident；运行 EVAL_ALERT/EVAL_JUDGE 后 sentinel 与业务数据计数不变；任何清理动作都局限于评测自建库。

## R09：拒答保护与评测存在共同盲区

**依据。** `ReAct.Run` 在 citations 为空时，通常只把“未找到相关匹配”前缀加到原 final 前，原回答内容不被删除。即使模型没有调用 rag_search，仍能返回包含未证实建议的回答。只要检索返回过任何 hits，引用数组非空也不能证明最终论断被这些引用支持。

`genRefusal` 使用独立构造的一次 LLM 请求，不走真实 ReAct/HTTP 路径；验收仅检查回答是否包含上述字符串。于是“未找到相关匹配，但请做 X”也能被算为成功。

`/alert` 与 `/plan` 实际调用模板式 Planner 拼接检索结果，并不执行这条独立的 LLM 拒答链。默认 floor=0 时，对库外告警缺少等价的相关性决策，不能从 EVAL_GEN 的结果推导线上告警已获得同样的拒答保障。

**最小修复。** 本轮无合格证据时直接返回确定性的安全结果，不保留未经证实的操作文本。让聊天、推送诊断和评测使用同一证据判定契约；不必立即建设复杂 verifier，但必须防止“加免责声明就放行”。

**验收。** 假模型不调用检索却给出处置建议时，输出被阻断；“拒答词+操作步骤”不算通过；库外问题、错误引用、历史相关但本轮无证据、模型失败时都走实际服务路径验证。

**本次离线验证。** 输入无引用的“把 worker 数量设置为 99。”，原保护逻辑保留该句；原评测谓词同时返回 true。

## R10：数据源不可用被误报成没有告警

**依据。** `Planner.PlanWithContext` 只在 FiringWithContext 无错误时接收告警。错误时 alerts 保持空值，随后返回“当前无 firing 告警，无需诊断”。检索错误也会被弱化成无匹配结果；RunAlertDiagnosis 默认生成 done。

**最小修复。** 分离 `no_alerts`、`source_unavailable`、`no_evidence`、`diagnosis_failed`。让真实依赖失败通过 error/status 传播；/ping 继续作 liveness，另加 readiness 或可见依赖状态，不要让故障看起来像正常空数据。

**验收。** Prometheus 超时/500/异常 JSON 时不会返回“无需诊断”；真正的空告警列表才返回无告警；状态、日志、指标一致。

## R11：报告未经质量校验就回流检索

**依据。** RunAlertDiagnosis 先 ingestIncident，再调用 judge。低分、未评分、无引用结果也会进入 source=incident。权重 0.5 是排序策略，不是质量隔离；唯一候选或标题高度匹配时仍可被召回。报告按 alertname 覆盖，不区分环境/服务；每条告警还保存整批 diagnosis，造成跨告警混入。

**最小修复。** MVP 可直接默认关闭 auto_ingest，保留报告展示。启用时区分未审定 incident 与 runbook；至少无合格证据/低质量不进入默认知识检索。避免同一诊断再引用自己的派生内容，并明确文档身份包含哪些维度。

**验收。** 无引用和低分报告不会自动成为可信依据；重复诊断不发生自证循环；不同服务的同名告警不会静默互相覆盖。

## R12：通知链存在三个不同的可靠性缺口

**入队失败。** maybeNotify 仅记录 EnqueueNotification 错误；ProcessAlertDiagnosis 随后返回 nil。一次瞬时 Redis/入队故障可使通知完全未入队，之后没有重试载体。at-least-once 只能覆盖已经成功进入通知队列的任务。

**失败载荷。** markFailed 更新环内记录时保留了旧 alerts，但返回的局部 rep 只有 ID/status/time/diagnosis。发送这个 rep 会使通知缺少原告警上下文；即使 /reports 仍有 alerts，通知也可能没有。

**资源共享。** 两个 queue 名称使用同一个 Concurrency=2 的 asynq.Server。两个慢诊断占满执行槽时，通知仍需等待；不同队列名称不等于执行资源隔离。

**最小修复。** 使用轻量可恢复的待通知记录/outbox，或让入队失败保留可重试状态且不重复诊断；markFailed 返回完整的最终报告。确实需要低延迟通知时给它单独的 worker 配额/实例。

**验收。** 模拟一次通知入队失败后能恢复投递；panic 通知仍包含原 alerts；两个阻塞诊断不会阻塞独立通知消费者。

## R13：MCP 的 GET 与 POST 不共享 transport 会话

**依据。** main 为 POST /mcp 与 GET /mcp 分别调用一次 StreamableHTTPHandler。项目锁定 SDK 的 handler 各自维护 sessions map，而不是由传入的同一个 Server 自动统一会话。对于使用有状态 Streamable HTTP 的客户端，POST 建立的会话不能由另一个 GET handler 查到。项目也没有把 DELETE 挂到这个 transport。

**最小修复。** 创建一个 transport handler 实例并复用到适用方法；按实际选择的有状态/无状态模式配置、文档化与测试。只读工具不要求应用一定使用有状态模式。

**验收。** 使用支持有状态协议的客户端，initialize 后的 GET/SSE 与后续 POST 使用同一会话；会话关闭路径与选择的传输模式一致。不能只测试 tools/list 能返回。

**边界。** 本项来自项目装配代码与锁定 SDK 的静态交叉核对，没有实际运行 MCP 客户端联调。

## R14：容量限制与端到端取消尚不完整

**依据。** Alert 使用无总量上限的 io.ReadAll；普通 JSON 入口未见统一 body cap；http.Server 只设置 Addr/Handler；sessions 总量无上限。任务虽然有 asynq Timeout，但 RAG.Search、Embedder.Embed 和向量 HTTP 请求没有把任务 context 传入所有底层调用。单次 HTTP client timeout 不等于整个批次预算或取消传播。

**最小修复。** 给 body 大小、单批告警数、文档大小/chunk 数、会话总量和 token 长度设界限；设置合理的请求头/空闲超时；逐层传递 context，任务循环检查取消。对已由 SDK 提供大小限制的 MCP 入口无需重复声称它完全无保护。

**验收。** 超限输入明确被拒绝；取消后不再继续后续 embedding 与写入；依赖慢响应时不会无限占用 worker。

## R15：现有评测不足以证明线上契约

**依据。** queue_test 固化了错误去重语义；genRefusal 使用独立提示词/调用路径和字符串判据；评测以同进程预热 demo，有利于 BM25 内存镜像完整，但没有覆盖真实重启后的上传文档恢复。小型固定样本上的高召回不能代表更新、删除、重启、故障与共享访问的正确性。

**最小修复。** 不追求大而全测试。先增加下面的业务回归：

| 优先级 | 必须验证的业务场景 |
|---|---|
| 1 | 同告警重投返回原业务结果，不增加任务 |
| 1 | 同名文档 V1→V2，只能召回 V2 |
| 1 | 上传→reindex→重启→删除，三个索引一致 |
| 1 | Qdrant/embedding 故障不伪装持久化成功 |
| 1 | 更换 embedding 不自动删库；评测不碰业务库 |
| 1 | 无证据时实际 /chat 与 /alert 均不能放行未经支持的处置 |
| 1 | Prometheus 故障不同于零告警 |
| 2 | 两浏览器会话隔离；重试通知不丢上下文 |
| 2 | MCP initialize→GET/SSE→POST→close |

## 建议交付顺序

第一批：阻止破坏性行为——R07/R08；禁止静默持久化失败与向量空间切换——R06；共享使用前处理 R01/R02。

第二批：修复知识与任务生命周期——R03/R04/R05，以及 R10 的故障语义。每项修复同时加入对应业务回归。

第三批：修复“无证据不回答”的运行时契约与端到端验收——R09/R15；先关闭未经审核的自动沉淀降低风险——R11。

第四批：按实际启用面修复通知、MCP、容量与取消——R12/R13/R14。

无需为这些修复先引入新的 Agent 框架、重排模型、微服务化或复杂多租户权限平台。报告内存环、少量只读工具、rerank 暂不在线等已经明确的 MVP 取舍，不自动视为缺陷。

## 离线复现包

`repro/` 是标准库 Go 摘录逻辑模块。执行：

```bash
cd repro
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -v .
```

四个 TestRepro 的 PASS 表示复现了当前缺陷/前置机制，不表示项目符合业务验收，更不表示完整项目测试已通过。完整输出在 `repro-results.txt`。

源文件定位均以本报告开头的固定提交为准。SDK 交叉核对位置为 `modelcontextprotocol/go-sdk@v1.8.0/mcp/streamable.go`：StreamableHTTPHandler.sessions、NewStreamableHTTPHandler、Stateless 默认值与 ServeHTTP 分支。
