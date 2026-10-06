# 本轮证据边界

合格引用必须含 document/version/chunk 标识、正文，且来自 upload/demo。ReAct 和 Planner 输出只能是本轮合格引用的确定性摘录，标明适用性、根因尚未核验、需人工研判。LLM 即使返回一个真实引用，也不能释放其它任意处置建议；外来 alert description 同样不升级为建议。

聊天响应 evidence_status 为 has_citations/no_evidence；无证据返回 SafeReport。检索/工具故障实际返回503 source_unavailable，内部保留原错误，外部不泄露来源密钥；deadline504、取消408。默认无会话历史。

| 场景 | Invocation / 二元观察 | 证据 |
|---|---|---|
| 真 ReAct mock 模型→rag_search→真实临时版本→恶意删除生产库建议 | TestReActQualifiedCitationDoesNotReleaseUnrelatedModelAdvice：修前 FAIL、修后只能原文摘录且不出现模型越界建议 | red.log、final-full-race.log |
| Planner 告警描述带删除动作、非法来源片段 | TestPlannerDoesNotPromoteAlertDescriptionIntoAdvice / TestEvidenceReportDoesNotPromoteUntrustedSource PASS | final-full-race.log |
| 实际 /chat HTTP + mock模型和临时版本，来源损坏、deadline | chat_boundary_test.go 场景：越界动作缺席、has_citations、真实503/504、内部错误保留且外部无秘密 | final-full-race.log |
| Agent/Handler/Tool 所有测试 | `go test -race ./internal/agent ./internal/handler ./internal/tool -count=1 -v` | final-full-race.log |
| 静态检查 | `go vet ./internal/agent ./internal/handler ./internal/tool ./cmd/workspacectl ./internal/workspace` 退出0 | final-vet.log |

测试源绑定 source-sha256.txt；这些是 L1 临时数据库/内存 Hash/Memory 显式测试和 mock 模型，不替代浏览器或真实外部依赖验收。
