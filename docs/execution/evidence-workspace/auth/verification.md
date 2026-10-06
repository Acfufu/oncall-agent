# T06/T11 认证、聊天隔离、MCP 证据

等级：L1（Gin handler + httptest TCP fake 模型/MCP 服务），不是外部 Redis/Qdrant/真实模型集成证据。执行环境 macOS arm64，Go 1.27.1；源状态见 `source-state.json`，未提交、推送或修改用户配置。

| 验收项 | 精确场景/二元判定 | 调用 | Artifact |
|---|---|---|---|
| SEC-01 | 无凭据 chat/upload/evidence/graph/mcp 为401；AM跨scope为403；console为200；AM POST /alert为200；无配置503 | `go test -race ./internal/auth ./internal/mcpserver ./internal/handler -run 'Test(PermissionBoundary\|CookieCSRF\|RateLimits\|SecureTransportAndSessionExpiry\|AuthenticatedTransportSession\|ChatStatelessSafety\|ChatLimits\|ChatCancelledRequest)$' -count=1` | `final-race.log` PASS |
| SEC-02 | HttpOnly+Strict cookie；缺CSRF/跨源写403；正确同源写200；注销后401；非localhost HTTP登录403；HTTPS Secure；TTL过期及容量淘汰401 | 同上 TestCookieCSRF/TestSecureTransportAndSessionExpiry | `final-race.log` PASS |
| SEC-03/EVD-01 | fake模型故意返回“未找到相关匹配”+危险操作，生产Chat响应 no_evidence 且丢弃模型建议；相同伪造session_id连续两次每请求仅一个user且无历史残留 | `go test ./internal/handler -run '^TestChat(StatelessSafety\|Limits)$' -count=1`（修复前/后）；最终race加取消用例 | `chat-red.log` FAIL复现真实问题；`chat-green.log` PASS；`final-race.log` PASS |
| REL-09 | 单transport initialize得到session id，initialized 202，tools/list 200含time_now，同session GET SSE 200，DELETE 204；GET/POST/DELETE匿名401、AM403 | `go test ./internal/mcpserver -run '^TestAuthenticatedTransportSession$' -count=1` | `mcp.log` PASS；`final-race.log` PASS |
| REL-10 | 第6登录429，第121读429，第11复诊429；12KiB message/32KiB body超限413；未配置Chat503；已取消请求408 | 最终race上述精确选择 | `final-race.log` PASS |
| 静态检查 | auth/mcpserver vet exit_code=0 | `go vet ./internal/auth ./internal/mcpserver` | `vet.log` PASS |

实现接口：`auth.New(consoleToken,webhookToken,opts...)`、`WithLocalhostHTTP(bool)`、`WithSessionLimits(ttl,max)`、`Register(*gin.Engine)`、`Middleware()`。主程序必须把Middleware装配到所有受保护接口；本分工未编辑main。生产token从环境读取由主程序负责。login来源IP使用TCP RemoteAddr，不信任匿名转发头；反向代理部署须保持HTTPS transport或另行配置明确受信代理，不能隐式信任客户端Forwarded头。

全包额外检查 `go test ./internal/auth ./internal/mcpserver ./internal/handler -count=1` 曾FAIL：旧 `TestAlertAutoIngestIncident` 要求默认检索能召回incident，和并行实现的默认可信检索排除incident冲突，日志 `all.log`。该失败已交由主管协调，不将全包记PASS。auth新包初始红日志 `red.log` 为缺实现编译失败，不冒充原行为bug复现；原行为bug由 `chat-red.log` 提供。

会话默认8h/100。浏览器长期token只通过Authorization进入login；会话返回随机CSRF，不记录长期密钥。纯Bearer按scope访问不依赖CSRF；Cookie写请求必须同源Origin和CSRF。旧DELETE /session无状态返回cleared=0，无法删除其他请求历史。
