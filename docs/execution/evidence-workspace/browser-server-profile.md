# 可重复 Go 浏览器 L1 profile

专属测试文件 `internal/handler/browser_server_test.go`。默认 `go test` 明确SKIP，不将长驻server测试记成PASS。

启动 invocation：

`ONCALL_BROWSER_TEST=1 go test ./internal/handler -run '^TestWorkspaceBrowserServer$' -count=1 -timeout 35m -v`

本次启动stdout/stderr artifact：`logs/browser-server-l1.log`；exec tty session_id=28247。服务实际监听 `http://127.0.0.1:18820`，限时30分钟；使用 `t.TempDir()/isolated-browser-facts.sqlite`，不读取业务配置/业务SQLite。可通过console Bearer POST `/test/quit` 或Ctrl+C退出，server graceful shutdown后等待测试worker结束。

固定隔离凭据为 `isolated-browser-console` / `isolated-browser-webhook`，仅写在这个test profile源码；不要用于任何部署。登录经真实auth.Register与Cookie/Origin/CSRF Middleware；所有旧业务路径、新API、metrics、MCP都受到共享认证保护。长期测试凭据没有放到URL或前端bundle。

Go实际托管绝对路径web/dist SPA/assets；保留legacy/v01。SQLite和WorkspaceHTTP/Service为真实业务实现；Queue仅在test过程调用真实ProcessAlertDiagnosis/ProcessNotification goroutine，是显式L1 fake；检索为明确MemoryVector+HashEmbedder测试profile。未配置Prometheus/judge/通知上游，Plan明确503；不伪造演示状态掩盖依赖故障。

预置仅一份 `CPUHigh L1 测试知识`（environment=test、明确测试内容），没有预创建事件或run。浏览器应上传自己的知识文档，再通过已认证 `/alert` 触发真实SQL接收→诊断→图/片段/时间线→版本更新与历史读取。

二元预检（实际HTTP）：SPA `/`200；`/ping`200且test_profile；`/api/v1/documents`匿名401、console200且data长度1；`/api/v1/incidents`console200且data长度0；`/mcp`匿名401。artifact `logs/browser-server-preflight.json`。默认显式选测试 `go test ./internal/handler -run '^TestWorkspaceBrowserServer$' -count=1 -v` artifact `logs/browser-server-default-skip.log` 为SKIP，不是server质量测试PASS。

源状态：`logs/browser-server-source-state.json`。本记录只证明启动和预检；浏览器实走与截图由主管另行记录。L1不替代真实Redis/asynq、Qdrant、Prometheus/模型L2/L4。server未退出期间，长驻测试未到终态，不声称其最终PASS。

M2 refresh: current interactive server session 12264, same http://127.0.0.1:18820, explicit ONCALL_BROWSER_DB=/tmp/oncall-evidence-browser-test-k2n7ij8y/isolated-browser-facts.sqlite validated marker; old session28247 ended. Online SQL backup preserved two user-tested documents/two incidents. Profile registers M2 with controlled test/default/browser-api→database declaration and fake Prom range samples explicitly labeled L1. Actual HTTP topology200, six source Evidence GET200, metrics200 captured m2/browser-profile-preflight.json. This remains L1; separate L2 uses real Docker sources.

Final UI recovery: former session12264 reached its30min profile deadline. Same owned SQLite was reopened with latest Go source in new session22201 at18820; actual /ping200. Command uses ONCALL_BROWSER_TEST=1 plus the same ONCALL_BROWSER_DB. New log logs/browser-server-final-l1.log; source snapshot m2/browser-final-source-state.json. It remains Hash/Memory/fake queue + controlled Prom samples L1 only; credentials remain isolated literals, cookie sessions require a fresh login. Hold30min from this restart for final frontend QA.

Final review refresh: session22201 / PID61667自然30min到期exit0，确认旧端口退出后用同一owned SQLite重启session79195。00:13:56 CST `/ping`实际200，窗口30min，重新登录；log `logs/browser-server-review-refresh-l1.log`，`m2/browser-review-refresh-state.json`保存旧退出事实、当前SQL计数和Go源码SHA。未清理/重建原测试业务事实。

Review follow-up window (2026-10-07 CST): session22201 naturally completed its30min hold (1800.01s); PID61667 no longer existed and /ping000 was observed before starting any new process. New session79195 reopens the exact same owned SQLite with the same explicit L1 profile. Actual /ping200 at00:13:56 CST,30min hold to approximately00:43:5x. Log logs/browser-server-review-refresh-l1.log; m2/browser-review-refresh-state.json records previous exit/closed port, ownership marker, existing SQL counts, HEAD/Go/SHA256, new ready HTTP body/status. No source implementation changed; fresh authentication is required after session restart. The previous test PASS merely confirms graceful profile lifetime, not a browser QA pass.

结束：最终UI与证据完成后root经隔离console POST /test/quit正常退出，随后/ping000；logs/browser-server-review-refresh-l1.log记录PASS601.31s。原owned临时SQLite保留作证据，未删除业务库。
