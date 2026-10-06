# 审查修正：持久 cleanup 与 SQL 发布投影

范围为 `internal/workspace/knowledge.go`、`service.go` 的 Dispatch；新增 cleanup 回归。主线程另授权修改删除 HTTP 回归与 OpenAPI 的202状态，保留其历史版本元数据修正与其它并发改动。

删除将 tombstone 与 kind=cleanup outbox 同事务提交，返回202/cleanup_pending；SQL 当前投影立即排除，历史SQL chunks/evidence不删。Dispatch 独立于队列处理cleanup，失败保留pending并持久指数退避，重启从SQL重新读取；成功同事务标removed/sent。knowledge锁贯穿清理与状态提交，当前已复活同版本时跳过物理删除；每次删除任务有独立ID，后续再删除仍可清理。未知outbox kind不再落入通知分支。

PutDocument两个幂等提前返回分支均恢复SQL当前投影。发布/删除提交后沿用主线程已写的restorePublishedProjection（WithoutCancel，5s界限）；投影恢复失败直接返回错误，不能报告成功而保留旧active过滤。无expected已有文档除正文hash外，title/source/environment也必须相同，否则409。历史ID/原正文/创建时间规则未改。

| 场景 | 调用与二元观察 | 证据 |
|---|---|---|
| 删除故障→退避→关闭重开SQLite→恢复远端 | TestCleanupDurableRetryRestartAndHistoricalChunks：pending/attempts1/next_at未来，无提前重试；重启后sent/attempts2/removed；历史chunks仍在 | cleanup-red.log、cleanup-targeted-race.log、cleanup-final-green-race.log |
| 迟到清理与同版本复活/再次删除 | TestCleanupLateDispatchDoesNotDeleteReactivatedVersion：活跃版本仍可检索；下一次删除可完成removed | 同上 |
| 幂等重传修复丢失active投影，metadata POST绕过 | TestDocumentRetransmissionRepairsProjectionAndRejectsMetadataBypass：key和无key路径恢复命中；改大小写标题/环境拒绝；取消上下文恢复已发布SQL事实仍成功 | 同上 |
| 原子删除回滚 | TestCleanupTombstoneAndOutboxRollbackTogether：触发器拒绝outbox插入，删除报错且SQL当前文档/检索/任务数量不部分提交 | cleanup-final-green-race.log |
| 删除实际HTTP合同 | TestWorkspaceDocumentVersionAndLegacyAdapters：202 cleanup_pending，当前检索空；Dispatch后removed；旧Markdown和chunks可读 | cleanup-final-green-race.log |

先红命令：`go test ./internal/workspace -run 'TestCleanup|TestDocumentRetransmission' -count=1 -v`。完整绿命令：`go test -race ./internal/workspace ./internal/handler -count=1 -v`。中间cleanup-full-race.log保留旧删除测试200期望导致的失败；cleanup-final-race.log保留并发Chat测试构建未完成导致的失败，未将其计为通过。取消后的发布恢复helper此前已由主线程修复，测试直接验证该恢复窗口，没有人为回退它制造红。

这是隔离L1（临时SQLite、显式Memory/Hash、fake Qdrant、Gin HTTP）证据；不声明生产外部服务验收。源码绑定见cleanup-source-sha256.txt。
