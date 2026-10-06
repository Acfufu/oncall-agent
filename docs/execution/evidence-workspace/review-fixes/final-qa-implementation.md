# 最后受限 QA

仅新增 `workspace_budget_test.go`、`readiness_test.go`、`retry_event_test.go`，未修改生产代码。所有服务数据位于临时SQLite，Hash/Memory显式构造；远端为localhost fake HTTP，等级L1。

| 场景 | 二元观察 | Invocation / artifact |
|---|---|---|
| writeDocument、upload、新旧reindex，正常多chunk | 首次与后续EmbedContext持同一约30s deadline，HTTP2xx；总deadline不按chunk重置 | TestWorkspaceWriteTotalBudgetAndInheritedDeadline，final-qa-first-race.log |
| 相同4 HTTP路径，父context80ms，慢EmbedContext | 返回实际Gin HTTP504 code=timeout，仅启动第一chunk embedding，SQL chunks=0，没有后续写入 | 同上；日志保留实际JSON响应 |
| Probe初始来源失效后恢复 | offline不能初始化，源恢复固定SpaceID、恢复SQL active版本/BM25，模型identity变化拒绝 | TestProbeRetrievalSourceRecoveryAndSpaceFence |
| Probe忙时remote marker | 修前ErrProbeBusy但fakeHTTP计数PUT=1（FAIL）；所需结果为PUT=0，状态保持unavailable | TestProbeRetrievalBusyDoesNotWriteSpaceMarker，final-qa-first-race.log |
| 实际Recover/claim | 两次queue started对应StageAttempt1→2，fencing Attempt1→3，两者独立 | TestQueueRecoverEventStageAttemptCountsExecution |
| 通知实际HTTP失败再成功 | source返回503→200；修前failed/succeeded两个事件StageAttempt均1（FAIL）；所需第二次为2 | TestNotificationFailedThenSuccessStageAttemptTwo，final-qa-first-race.log |
| 实际/chat资格 | 原查询ShardingChaos不能因模型改问CPUHigh得到cite，环境prod不能引用test文档；原查询CPUHigh+test才有cite | 主线程已有TestChatOriginalQueryAndEnvironmentQualification，final-qa-first-race.log |

精确命令：`go test -race ./internal/workspace ./internal/handler -run 'TestWorkspaceWriteTotalBudget|TestProbeRetrieval|TestQueueRecoverEvent|TestNotificationFailedThen|TestChatOriginalQuery' -count=1 -v`。

未运行30秒挂起等待：HTTP handler的实际deadline由ContextEmbedder观察，短父时限真实耗尽并返回504。这不同于声称真实生产网络或L2依赖的验证。两个红已报给生产所有者主线程修复，最终状态以final-qa-green-race.log为准，不将首轮整批标为通过。

主线程修复后按上述精确命令重跑，final-qa-green-race.log 两包全部目标场景 PASS。Busy probe fakeHTTP PUT=0，通知真实503→200的terminal StageAttempt为1→2。生产修复由主线程完成，QA子线程没有修改生产。final-qa-green-vet.log 记录同两包 go vet 退出0。最终源状态绑定 final-qa-green-source-sha256.txt；首轮红源码绑定 final-qa-red-source-sha256.txt。
