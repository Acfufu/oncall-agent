# 运维迁移 CLI：隔离 L1 验证

新增 `cmd/workspacectl` 与 `internal/workspace/migration.go`。CLI 仅接受显式 `--config` 或 `ONCALL_CONFIG`；默认不加载业务配置。inventory/dry-run 原样读取旧 JSON，SQLite 以原生 `mode=ro` 打开且不运行 schema migration，Qdrant 仅 GET collection info。缺原 Markdown 明确提示需恢复原文件，不由标题伪造知识版本。

`import-legacy --apply` 单事务导入：保留报告 ID、原始 JSON/告警，done→succeeded、score=0→null，缺来源标 legacy_incomplete/not_evaluated；不创造运行事件、文档版本、图或 outbox。重复导入幂等，异内容同 ID 回滚整次导入。原文件校验和保持不变。`DB.LegacyReportArchive` 为兼容层返回历史原文。

`backup --output NEW` 使用已有 DB.Backup/VACUUM INTO，在同目录私有临时文件完成后原子无覆盖链接；选择旧备份路径演练回滚，未覆盖当前库。所有命令只访问自行创建的临时 fixture 与 localhost fake HTTP。

| 场景 / 实际命令 | 二元观察 | 证据 |
|---|---|---|
| `go test -race ./internal/workspace ./cmd/workspacectl -run 'TestLegacyImport\|TestInventory\|TestLegacyBackup\|TestBackupSelection\|TestCLI' -count=1 -v` | 各测试 PASS；重启与重复导入、事务冲突、只读连接写入拒绝、备份恢复/无覆盖 | final-race.log |
| `go build -o .omo/evidence/migration/workspacectl ./cmd/workspacectl`; `python3 .omo/evidence/migration/verify_cli.py` | 实际二进制退出码、SQL事实/备份数量、源哈希一致；Qdrant方法全部GET | cli-scenarios.json、verify_cli.py |
| 初次 VACUUM 验证 | query_only 阻止 VACUUM INTO；修正为原生 mode=ro 后通过，未隐瞒初次失败 | first-run.log、second-run.log |

上述为 L1 临时 SQLite/fake Qdrant/实际 CLI 测试，不声称生产升级或真实 Qdrant 验证。原始完整工作区测试曾遇到并发兼容层变更失败；此处仅声明 final-race.log 的明确迁移场景通过。全项目验收由主线程整合。
