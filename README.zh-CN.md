# oncall-agent

Evidence Workspace：Go / Gin + SQLite 业务事实 + Redis / asynq + Qdrant 检索 + Prometheus / OTel，React / TypeScript 前端由同一 Go 服务托管。按 M0→M1→M2实施；M3 全局探索仅规划。告警与工具保持只读，不确认、不静默、不自动处置。

[English](README.md) · [运行、迁移、备份与回滚](docs/execution/evidence-workspace/operations.md) · [验收与限制](docs/execution/evidence-workspace/verification.md) · [API](docs/api/workspace.openapi.yaml)

## 启动

需要 Go 1.25+、C 编译器（SQLite CGO）、Node 24（仅构建），以及 Redis、Qdrant、Embedding 服务。默认使用 Ollama `nomic-embed-text`；Embedding/Qdrant 故障明确报错，不使用内存库或 Hash 兜底。工作台和确定性引用诊断不要求 LLM key；聊天与 judge 使用配置的 OpenAI 兼容模型。

```sh
# 已有配置不覆盖；升级前先按运行说明盘点与备份
cp -n config/config_template.json config/config.json
cp -n .env.example .env
(cd web/app && npm ci && npm run build)
docker compose up -d
# 在当前 shell 设置两个不同强随机凭据；不要写入前端或提交到 Git
# export ONCALL_CONSOLE_TOKEN=...; export ONCALL_WEBHOOK_TOKEN=...
export ONCALL_LOCALHOST_HTTP=true # 仅 loopback HTTP 开发；TLS 环境不设置
export ONCALL_CONFIG="$PWD/config/config.json"
go run ./cmd/server
```

打开 [本地工作台](http://127.0.0.1:8819)，输入 console token 建立 HttpOnly / SameSite=Strict 会话。缺少凭据时业务接口拒绝访问。`GET /ping` 只证明进程存活，`GET /ready` 检查事实库、队列和检索。生产不运行 Node 或 Vite。

完整容器切片可用 `docker compose -f docker-compose.yml -f docker-compose.workspace.yml up --build -d`；先设置上述 token。新增 app 使用独立 SQLite volume 和新 collection，宿主仅开放 loopback 8819；Alertmanager 用 webhook token 调用 `/alert`，包含 resolved。不要执行 `down -v`，不要清理旧 collection。

## 工作台与数据

七页保留参考图的海军蓝、共同导航和各页布局。事件工作台提供事件列、证据图/可访问列表、来源检查器、真实阶段时间线。知识库支持版本、更新与撤下；引用始终指向不可变 version/chunk。M2 包含有来源的声明拓扑及三个受限指标模板，缺值保留 null，潜在影响仅推断。

指标页路径为 `/workspace/metrics`，原 `/metrics` 保留监控抓取用途。`?mode=demo` 是显式演示，持续显示“演示数据”，管理操作禁用；真实接口故障不会切演示。评测未配置时为空态，不显示虚构正确率。M3 全局图显示 deferred。

SQLite 是单实例事实来源：事件、运行、逐阶段事件、版本、引用、outbox 和来源记录跨重启保存。告警业务键事务幂等；resolved 更新生命周期，不产生新诊断，诊断成功不代表故障恢复。报告与事件笔记不成为合格 runbook；模板默认关闭 `knowledge.auto_ingest`。没有本轮相关证据时输出安全无证据结果，不释放模型任意操作建议。

## 接口与认证

所有业务、`/mcp` 和 `/metrics` 都需要认证。控制台使用 console Bearer 或同源短期会话；Cookie 写操作需要 Origin 与 CSRF。`POST /alert` 使用独立 webhook Bearer。HTTP 明文凭据只在显式 loopback 开发模式允许。

|接口|用途|
|--|--|
|`GET /api/v1/incidents`、`/runs/{id}`、`/incidents/{id}/graph`|分页事件、运行、证据图与时间线|
|`/api/v1/documents`、`/documents/{id}/versions`|版本化知识管理，更新具备 CAS|
|`GET /api/v1/evidence/{id}`|原始来源与不可变引用|
|`GET /api/v1/incidents/{id}/topology`、`/metrics`|限定环境拓扑、受限服务端指标模板|
|`GET /api/v1/workspace/summary`、`/system/status`|SQL 摘要、依赖状态与最近成功时间|
|`POST /alert`|异步 admission，202 后由持久 outbox 入队|
|`GET /reports`、`/plan`，`POST /upload`、`/chat`，`GET /list`|兼容旧客户端，通过同一领域层；需认证|
|`DELETE /delete`、`/session`，`POST /reindex`|撤下知识、兼容会话清理、活动版本投影恢复|
|`GET/POST /mcp`|共享 StreamableHTTP transport 会话与只读白名单|
|`GET /metrics`|Prometheus 抓取端点（需 console Bearer，不进 trace）|

MCP STDIO：`go run ./cmd/server serve --mcp`，信任本地进程所有者，仍只暴露白名单工具。`deploy_events` 仅在配置 GitHub repo 时注册，来源为只读 commits/deployments。通知使用独立消费者、持久载荷、at-least-once；接收方按报告 ID 幂等。通知失败不阻断诊断，不代表自动处置。

## 配置与恢复

模板为 `config/config_template.json`，不要提交 `config.json`、`.env` 或 token。重要项：`storage.sqlite_path`、独立 Qdrant collection、Redis namespace、Embedding provider/model/dimension、`ui.legacy_enabled` / `legacy_default`、受控 topology 文件、metric templates、可选 notify webhook。不同 embedding 空间用新 collection，不自动删除旧库。

`workspacectl inventory` / `dry-run` 为只读；`backup --output` 使用一致性 SQLite `VACUUM INTO` 并拒绝覆盖。旧报告通过显式 `import-legacy --apply` 导入，checksum 幂等且保留不完整来源标记，不重新投递旧任务。不能仅复制活动 WAL 库主文件。恢复时停止实例，保留原库，选择备份新路径；不要覆盖运行库。UI 可通过 `/legacy`、`/v01` 或 `ui.legacy_default=true` 回退，后端事实不变。详见运行说明。

## 开发与验证

```sh
gofmt -l cmd internal
go vet ./...
go test ./... -count=1 -timeout=3m
go test -race ./... -count=1 -timeout=3m
go build ./...
(cd web/app && npm run typecheck && npm run test && npm run test:browser)
# 可选：仅创建本次 owner 标签的隔离服务并清理；不接业务 compose/volume
ONCALL_L2=1 scripts/workspace-l2.sh
```

不要使用旧无隔离评测命令。新版 `evalbaseline` 必须提供明确隔离配置和本次 ownership；本次未执行真实模型质量评测。已执行检查、源码指纹、七页/窄屏截图、性能测量与限制见验收记录。受控 fake/Hash/Memory 仅在显式测试 profile 使用，不能证明模型质量或生产吞吐。

[Apache-2.0](LICENSE)
