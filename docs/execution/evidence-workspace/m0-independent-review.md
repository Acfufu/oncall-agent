# M0 独立 L1 集成验证

环境：macOS arm64，Go 1.27.1；每例 `t.TempDir()/facts.sqlite`，SQLite真实事务；`MemoryVector+HashEmbedder` 是显式测试profile；fake Enqueuer；Gin真实 WorkspaceHTTP + auth Middleware；通过 httptest TCP HTTP 进入真实Service。没有运行旧eval、业务服务、生产配置，没有安装主机工具。源状态 `logs/m0-integration-source-state.json`。

独立编写范围仅 `internal/workspace/integration_test.go`，主代码只读。复现问题由主管修改 Service/diagnosis/readmodel，我独立重新执行。

最终 invocation：

`go test -race ./internal/workspace -run '^TestM0Integration' -count=1 -v`

最终 artifact：`docs/execution/evidence-workspace/logs/m0-integration.log`；全部场景PASS。

| Test ID / exact scenario | 二元observable |
|---|---|
| REL-01 TestM0IntegrationFiftyWebhookReplays | 50并发真实POST /alert，交替告警数组顺序、随机report_id；所有202、同run id；SQL runs=1、outbox=1、incidents=2、run_incidents=2、alert_observations=2，fake queue实际入队次数=1 |
| EVD-02/SEC-01 TestM0IntegrationTwoAlertsEvidenceAndReadBoundary | 真实POST documents创建两个激活版本；双告警诊断成功；每incident citations仅自身DocID、非空VersionID/ChunkID、本轮attempt；实际evidence GET匿名401、AM403、console200；graph200；run_events sequence从1连续 |
| EVD-01/REL-07 TestM0IntegrationNoEvidenceAndSourceUnavailable | 空知识：succeeded/no_evidence、无citations；来源不可用：failed/source_unavailable、无citations；真实GET /runs相同领域状态 |
| REL-08 TestM0IntegrationNotificationQueueAndHTTPRecovery | 失败诊断终态与通知outbox同存；第一次EnqueueNotification故障后pending；显式到期重投成功（共2调用）；payload含原name/labels/annotations/description；真实notify HTTP500使通知pending但诊断failed不变；重试204后sent；重复消费不重发；实际请求体与持久payload逐字相同 |
| REL-10 TestM0IntegrationCancellationPersistsTerminalFailure | 受控embedding HTTP开始后取消worker ctx；worker停止；SQLite状态failed、error.code=timeout；不能遗留running |
| DATA-03/EVD-02 TestM0IntegrationRecoveredAttemptCannotReuseOldCitation | 第一attempt写citations后停在受控HTTP judge；Recover→第二attempt来源失败；释放旧judge后旧worker返回ErrFenced；终态source_unavailable且CitationIDs为空，graph没有当前report→旧片段cites边 |

修复前的同一次完整scope invocation发现两个真实FAIL，保留 `logs/m0-integration-red.log`：取消后running/not_evaluated；恢复attempt沿用旧引用。主管修复后重新执行完整scenario，未弱化断言。最新还增加SQL evidence attempt与实际通知HTTP body断言。

适用回归扩大到整个workspace包：

`go test -race ./internal/workspace -count=1`

artifact `docs/execution/evidence-workspace/logs/m0-workspace-scope.log`：PASS。包含已有版本切换/撤下/重启、outbox故障恢复、恢复迟到firing、fencing、备份恢复场景；不将其记为外部服务集成。

## 边界及未测

- L2 BLOCKED：独立执行 `docker info` exit_code=1，缺 `/Users/acfufu/.docker/run/docker.sock`；`redis-server`/`qdrant`/`prometheus` native executables均不存在。证据 `logs/m0-l2-environment.json`。不声称真实Redis/asynq、Qdrant持久向量或Prometheus集成通过。
- 未测真实server main boot与路由装配；本组直接注册生产handler/auth并通过TCP HTTP执行。主程序启动/静态UI/浏览器由主管另行验证。
- 通知队列是fake；HTTP失败重试显式调用真实ProcessNotification，不冒充真实asynq调度、MaxRetry耗尽、进程发送后崩溃或外部接收端exactly-once验证。
- 未执行真实模型、付费judge、长90秒deadline耗尽、生产Qdrant维度迁移、隔离eval真实服务、M2指标/拓扑或UI视觉验证。这里只声明已执行的M0 L1场景。
- 旧attempt检索与片段Evidence留存为审计记录，当前graph按metadata.attempt过滤；report证据当前终态覆盖且旧attempt被fence拒绝。未发现新的跨attempt发布阻塞。

后续状态（保留以上历史边界）：Docker 已可用，最终真实服务与通知/AM验证见 `l2/verification.md`；实际单 Go main/container启动与路由、持久恢复见 `container-smoke.json`；七页与实际L1闭环见 `m2/frontend/verification.md`。本文件仍仅是原阶段M0 L1独立记录，不能把后续证据倒填为当时已测。
