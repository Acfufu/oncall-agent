# T00 基线（2026-10-06）

- HEAD: d7a1fa65a91d46835a412b713abfa4145f36fa3b；审查旧基线 2e97742aae610b52535999a9851683320c349c6e。
- 起始工作树：仅 `?? handoff/`，交接包是用户输入，保持原样。
- Go: go1.27.1 darwin/arm64（go.mod 要求 1.25.0，不升级）；Node v24.19.0；npm 12.2.0。
- 原有命令：go test ./...、go test -race ./...、go vet ./...、go build ./...、gofmt -l .；Makefile check 仅 ping。
- Docker daemon 当前不可达（~/.docker/run/docker.sock 不存在）；L2 需独立服务，不能使用旧业务栈作替代。
- 未读取/改写用户 config.json 或 .env；未运行 evalbaseline；未创建提交。
- .codegraph 存在，定位代码先使用 codegraph explore；静态审查不等于集成验证。
- 已用 view_image 打开全部七张原图（1586×992）。

R01–R15 当前代码复核表由独立只读审查补齐。

## 当前审查问题（静态复核；非集成测试）

|ID|当前状态|定位 / 已有局部测试|
|--|--|--|
|R01|仍存在，loopback部分缓解|main.go无认证；compose绑定回环|
|R02|仍存在|chat.go空ID→default；session_test仅LRU与清除|
|R03|仍存在，task排除report_id部分修复|alert.go先造报告；queue_test仅report ID哈希去重|
|R04|仍存在，reindex子问题已修复且有测试|reindex_test保上传；上传注册表/BM25未持久|
|R05|仍存在|rag.AddDoc内容hash增新点，旧短文残留无版本|
|R06|仍存在|embed回hash，store写错误返回nil；混维内存过滤已有测试|
|R07|仍存在|main.go自动RecreateCollection|
|R08|仍存在|evalbaseline业务collection写入/删incident；本批未跑|
|R09|仍存在|react无引用前缀仍保建议；Planner吞检索错误|
|R10|部分修复仍存在|Prom错误传播有测试；Planner无error契约|
|R11|仍存在|config与Handler默认auto_ingest=true|
|R12|仍存在|通知无outbox，共享2消费者，panic payload缺告警|
|R13|仍存在|GET/POST不同transport实例，无握手测试|
|R14|部分修复仍存在|body2MB、读超时已有测试；缺批次限额/context|
|R15|仍存在|队列去重等局部测试；独立拒答字符串评测非真实业务链|

基线 `go test ./... -count=1` 实际PASS（日志logs/baseline-test.log）；这证明原断言通过，不证明上述新契约成立。

## 后续环境变化

Docker Desktop 启动后 daemon 可用。一次性 Redis/Qdrant/Prometheus 核心集成已实际通过，见 `l2/core-green.log` 与对应 owner manifest；起始不可用记录保留为历史事实。最终验收与代码指纹另见 `verification.md`，不以起始状态判断当前结果。

## 实施后 R01–R15 复核

原HEAD未改写；以下描述工作树修正，不假称它们已在原HEAD或已提交。L1/L2只证明所列场景，不代表外部模型质量。

|ID|当前结论|实际证据|
|--|--|--|
|R01|已修复且有测试：统一角色/会话/业务/MCP鉴权|auth/final-race.log；匿名401、角色403；L2实际AM Bearer回调|
|R02|已修复且有测试：chat默认无状态，不共享default历史|auth/chat-red.log→chat-green.log、shared-safety/final-full-race.log|
|R03|已修复且有测试：SQL business key+事务outbox，不以随机report_id去重|workspace/integration；l2/run.log 50重投→1run|
|R04|已修复且有测试：文档/活动版本持久；demo不覆盖上传，SQL重建BM25|workspace测试；L2 reopen V2/withdraw|
|R05|已修复且有测试：不可变版本+活动过滤、失败保旧、durable cleanup|review-fixes/cleanup-final-green-race.log，L2实际Qdrant历史|
|R06|已修复且有测试：embed/Qdrant错误传播、固定空间，不回Hash/Memory成功|retrieval红绿、readiness测试；L2停止/恢复/不兼容拒绝|
|R07|已修复且有测试：启动不Recreate/DELETE旧collection|store/reliability_test.go；L2 provider空间拒绝；业务库未运行破坏测试|
|R08|已修复且有测试：eval无业务默认配置，ownership/独立collection与自建清理|cmd/evalbaseline/isolation_test.go；旧eval未运行|
|R09|已修复且有测试：仅本轮相关/环境适用知识摘录；无证据SafeReport；不保模型任意建议|shared-safety红绿、chat_qualification_test.go；L2真实no_evidence|
|R10|已修复且有测试：上游错误与真空区分，实际Plan也走SQL链|Prom可靠性、workspace_plan_test.go；L2来源错误|
|R11|已修复且有测试：默认auto_ingest关闭，incident/report不进入合格知识|config模板、agent/evidence_guard与可信来源测试|
|R12|已修复且有测试：通知outbox+独立消费者，完整原告警，actual retry计数|workspace integration/retry_event；L2 HTTP500→204|
|R13|已修复且有测试：MCP GET/POST共享transport/session|auth/mcp.log，transport_test.go initialize/SSE/list/delete|
|R14|已修复且有测试：body/batch/rate限制、共享deadline/cancel与bounded终态提交|auth、workspace_budget_test.go；最终2s父预算504/单embed/零chunk|
|R15|已修复且有测试：真实业务路径断言，不独立拒答关键词评测|agent/handler/workspace integration；L2实际worker；最终L3闭环另见verification|
