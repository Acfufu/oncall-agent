# 单实例运行、升级与恢复

Go 1.25+、C 编译器（SQLite CGO）、Node 24 构建前端。生产只运行 Go，不运行 Vite。
先复制 config/config_template.json 到一个新配置文件（已有 config.json 不覆盖）。配置 storage.sqlite_path、独立新 Qdrant collection 和 Redis namespace。当前 Embedding 不可用会明确降级为 source_unavailable，不用 Hash/内存库替代。LLM key 仅 chat/judge 需要；无 key 仍可显示事实与做确定性引用诊断。

```sh
cd web/app
npm ci
npm run typecheck
npm run test
npm run build
cd ../..
# 在当前 shell 设置两个不同的强随机 console/webhook token，不把值写进Git或前端
export ONCALL_CONFIG=/absolute/path/to/new-config.json
export ONCALL_LOCALHOST_HTTP=true # 仅 loopback HTTP 开发；TLS环境不要设置
# export ONCALL_CONSOLE_TOKEN=...; export ONCALL_WEBHOOK_TOKEN=...
go run ./cmd/server
```

浏览器打开 http://127.0.0.1:8819 ，输入 console token 建立 HttpOnly 会话。缺 token 时业务接口 fail closed。`/ping` 只表示进程活着，`/ready` 检查事实/队列/检索；接口失败不会进入 demo。演示必须显式 `?mode=demo`。旧页 `/legacy`、`/v01` 的凭据仅在页面内存中输入；也可将新配置 `ui.legacy_default=true` 后重启回退 `/`，后端仍用同一事实层。

可选完整容器切片：先在 shell 设 token，再运行 `docker compose -f docker-compose.yml -f docker-compose.workspace.yml up --build -d`。app 对宿主只发布 loopback 8819，AM 通过 app:8819/alert 加 Bearer 凭据，resolved会回调。SQLite为单实例volume；不同时启动多个实例操作同库。旧compose保留；新override不重用/删除旧Qdrant collection。Embedding默认访问宿主Ollama。模板默认关闭自动沉淀。

## 升级前盘点与备份

只读 inventory/dry-run 不创建不存在的库，不 reset Qdrant：

```sh
go run ./cmd/workspacectl inventory --config /path/new-config.json
go run ./cmd/workspacectl dry-run --config /path/new-config.json --legacy /path/old-reports.json
# 存在的SQLite使用一致性VACUUM INTO新路径，拒绝覆盖备份；不能只cp WAL主文件
go run ./cmd/workspacectl backup --config /path/new-config.json --output /path/new-backup.sqlite
```

旧服务仍运行时将其 `/reports` 输出安全保存（只读，带console Bearer）以保留可见报告。先备份原始Markdown、旧配置与旧Qdrant官方snapshot/export，保留旧collection。工具只盘点旧向量，不自动拼接片段冒充完整人工runbook；仅有向量片段的知识需要人工重建并标注不完整来源。导入旧报告需显式 `import-legacy --config ... --legacy ... --apply`，事务且checksum幂等，保留旧ID、原始JSON；来源不完整不能进入合格知识，也不重投旧queued/running。

## 恢复演练与回滚

停止实例，保留现库和WAL文件，选择备份的新路径作为 storage.sqlite_path 后启动；校验run IDs、文档版本、旧引用与事件后再切流。不要覆盖运行中的库。回滚UI用 legacy_default；回滚程序保持新库/旧库分别留存，新数据库不能由旧程序读取或清空。不执行 docker compose down -v。恢复检索使用事实库active版本，旧版本历史保留。新embedding模型/维度/space用新collection，不能自动DELETE旧collection。

## 验证边界

`go test ./...` 和race用临时SQLite/fakeHTTP；`ONCALL_BROWSER_TEST=1 go test ./internal/handler -run '^TestWorkspaceBrowserServer$' -count=1 -timeout 35m` 是显式L1浏览器测试profile，不是生产fallback。测试temp库、MemoryVector、HashEmbedder仅该测试使用。

禁止用旧破坏性评测验收。新版 eval 必须显式隔离配置与本次ownership标识；尚未执行真实模型质量测试。Docker Desktop 启动后已可用，真实 Redis/Qdrant/Prometheus 隔离验证见 `l2/core-green.log`。完整 L2 与 Alertmanager 回调结果见最终 `verification.md`；静态 compose 解析不等于 AM 端到端已通过。

```sh
# 仅创建本次 owner 标签的一次性容器、独立端口与临时事实库；退出时按 owner 校验后清理
ONCALL_L2=1 scripts/workspace-l2.sh
# 默认不运行上面的 opt-in 集成；前端浏览器回归命令为：
cd web/app && npm run test:browser
```

指标工作台直接链接为 `/workspace/metrics`；`/metrics` 是需 console Bearer 的 Prometheus 抓取端点。
