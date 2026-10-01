# v0.7.2 快扫审查报告——遗留风险清单重建（2026-10-02）

> 背景：五轮双审（2026-09-29）原始 46 项发现清单未落盘，F01-F09/F15/F35 修复后 F10-F22 及 P3 卫生项详情失传。本报告是 v0.7.2 清债批次修完四项后的**全新快扫**（非原文恢复），编号改用 R 前缀避免与失传的 F 编号混淆。抽查 3 项重灾发现（R01/R02/R10）均 file:line 实证核实。
>
> 扫描面：cmd/、internal/ 全部包、web/console.html、docker-compose.yml、prometheus/、alertmanager/、otel/、config 模板、.env.example、README/AGENTS/CONTEXT 及全部测试文件。
>
> **确认干净的面**（本轮无发现，供下轮复审基线）：console.html 所有 innerHTML 插值均经 esc() 转义；无路径穿越（reindex 读固定 DemoDir，upload/delete 走 JSON 标题非文件名）；key/token 仅进 Authorization 头无日志路径；bm25/reportRing/registry 锁使用一致；工具白名单单一事实源且 MCP 面经同一 ExecWithContext 门检；notify 出站有超时且读尽 body；AM send_resolved:false 与「不确认不静默」一致；compose 六服务端口发布全收敛 127.0.0.1。

## P1

- **[R01] [降级路径] embedding 维度错配级联：Ollama 瞬断 → 64 维假向量入 Qdrant → memOnly 闩锁**
  `internal/rag/embed.go:48-55`（Embed 失败静默回退 HashEmbed 64 维且返回 nil error）、`cmd/server/main.go:87-98`（dim 探测因 Embed 恒成功无法区分回退，Ollama 宕时 collection 建成 64 维）、`internal/store/qdrant.go:216-219`（Upsert 失败 → fallback 闩锁 + return nil 谎报成功）。
  后果：运行中 Ollama 重启即触发——部分文档 hash-64、部分真实 768 混存，此后真实维度查询被 Qdrant 拒 → 永久降级内存模式（重启才恢复），检索质量静默崩坏。F04 的 memonly 位只能看到「降级了」，看不到根因。
  修法方向：Embed 回退带维度守卫（回退向量与 collection 维度不符即报错或拒写）；boot 探测失败拒建 64 维 collection；Upsert 错误分类（维度错配 vs 网络）可观测。

## P2

- **[R02] [降级路径] Prom 不可达被吞成「当前无 firing 告警」，/plan 假阴性**
  `internal/tool/prometheus.go:71-82`（NewRequest 错、Do 错、非 2xx 三分支全 `return nil, nil`，span 记 OK、无日志无计数）+ `internal/agent/planner.go:48-49`（空列表输出「当前无 firing 告警，无需诊断」）。
  后果：Prom 挂掉时值班员得到「无告警」而非「告警源不可达」。修法方向：不可达与空列表分型返回，文案区分，补 prom_unreachable 计数器或 span error。

- **[R03] [可观测] /chat 的 LLM 故障失声：loop 错误被 fallback 覆盖，无日志无计数**
  `internal/agent/react.go:113-117`（`return r.fallback(...)` 用 fallback nil 覆盖 named err，span 按成功收口）、`react.go:269-289`（fallback 不记触发原因）、`internal/handler/chat.go:44-46`（err 直接丢弃）。embed/store 降级都有计数器（F04），ReAct 的 LLM 降级什么都没有。修法方向：fallback 入口 log + chat_fallback_total 计数器，保留原始 loop err。

- **[R04] [资源] judge 出站调用 http.DefaultClient 零超时**
  `internal/judge/judge.go:57`。全仓其它外呼均有 Client Timeout（react 300s/prom 10s/notify 10s/github 10s），唯 judge 裸用 DefaultClient——LLM 挂起可吃满 asynq 任务预算（10 分钟）拖死并发 2 的 worker 槽位；`cmd/evalbaseline/main.go:400` 路径完全无界。修法方向：注入带 Timeout 的 Client（30-60s）。

- **[R05] [测试盲区] 两条最高危降级链零覆盖**
  `internal/agent/react.go`（ReAct loop/fallback/会话裁剪）无任何测试；`internal/store/` 整包无测试——memOnly 闩锁、Upsert 静默回退、Search 降内存（R01 载体）零覆盖；handler 的 chat/upload/delete 路径同样无测试。R01/R03 恰都落在这些盲区。修法方向：store 降级状态机表驱动测试 + react fallback 假 LLM 注入断言。

## P3

- **[R06] [并发] PlannerAgent 懒构造 check-then-act 无锁**
  `internal/handler/plan.go:21-23`、`internal/handler/alert.go:179-181` 在 h.mu 保护外。生产 boot 时 main.go:111 已装配窗口关闭，但 queue 并发 2 下任何未装配路径（测试/重构）是真实 data race。修法方向：装配期强制非 nil，删懒构造分支。

- **[R07] [资源] 请求体无上限 + http.Server 无读写超时**
  `internal/handler/alert.go:230`（io.ReadAll 无 LimitReader）、`cmd/server/main.go:162`（Server 无 Read/Write/HeaderTimeout）、/upload ShouldBindJSON 同样无界。回环绑定缓解暴露面，超大 payload 仍可占内存。修法方向：http.MaxBytesReader 1-2MB + Server 超时。

- **[R08] [资源] ReAct 会话表按 session_id 无界增长，且无清空入口**
  `internal/agent/react.go:38,107,133`（sessions map 只增不减，无 TTL/淘汰）；CONTEXT.md 声称会话「可清空」但 API 面无清空端点——文档能力与实现不符。修法方向：LRU/TTL 上限 + DELETE 会话语义（或改 CONTEXT 词条）。

- **[R09] [文档漂移] .env.example 三变量仅 1 个被读，cp .env 步骤形同虚设**
  `internal/config/config.go:134` 全仓仅读 OPENAI_API_KEY；OPENAI_BASE_URL/OPENAI_MODEL 是死变量且无 godotenv/compose 引用——改 .env 换模型会静默落到默认 api.openai.com。修法方向：补齐 env 覆盖或删变量改文档。

- **[R10] [降级路径] ProcessNotification 重试耗尽判定与注释相反**
  `internal/handler/alert.go:358-366`：注释说 GetRetryCount/GetMaxRetry ok=false「按未耗尽处理走重试」，但双下划线忽略 ok，(0,0) 时 `0>=0` 为真走耗尽分支——打 notification_failed_total + 耗尽日志。生产行为碰巧正确，单测场景计数器被污染，注释误导维护。修法方向：显式检查 ok，ok=false 独立分支不计数。

- **[R11] [文档漂移] README 配置表仍写 server 默认 0.0.0.0；版本串四处过时**
  `README.md:159`（表格 0.0.0.0:8819）、`README.md:23`（示例日志）vs `internal/config/config.go:90`（127.0.0.1）——同文档自相矛盾（v0.7.1/F05 漏改面）。版本串：`cmd/server/main.go:161` "v0.1"、`internal/observability/trace.go:21` "v0.3.0"、`internal/mcpserver/server.go:18` "v0.7.0"、`web/console.html:6,10` "v0.4"。修法方向：配置表改 127.0.0.1；版本串统一单一事实源。

- **[R12] [健壮性] LLM 响应先 decode 后查状态码；parseAlertPayload error 分支死代码**
  `internal/agent/react.go:250-259`（decode 在 StatusCode 检查前，502 HTML/429 空 body 报 "decode llm resp" 掩盖真实状态）；`internal/handler/alert.go:236-238`（四 return 点恒返 nil error，`perr != nil` 不可达）。修法方向：先查状态码再 decode；删死分支。
