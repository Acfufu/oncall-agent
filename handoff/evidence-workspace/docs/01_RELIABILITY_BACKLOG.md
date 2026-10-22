# 01 · R01–R15 可靠性修复清单

原始证据和复现见 [完整审查](../reference/audit/REVIEW.zh-CN.md)。下面保留原 ID，新增的是实施任务与验收要求，不是已完成修复。基线源文件位置均相对 `internal/`，另有明确根路径。[SRC-02]

| 原 ID | 主要位置 | 本次执行要求 | 对应验收 |
|---|---|---|---|
| R01 | `cmd/server/main.go`、配置、compose | 本机默认 loopback；统一认证；管理/AM token 分离；浏览器会话与 CSRF；部署网络路径可用 | SEC-01, SEC-02 |
| R02 | `handler/chat.go`、`agent/react.go`、console | 默认无状态聊天，不允许全局 default 历史；保留历史必须服务端会话隔离并限制容量 | SEC-03 |
| R03 | `queue/queue.go`、`handler/alert.go` | 告警实例/事件、诊断 run、task 分开；哈希不含随机 report ID；并发 admission 幂等，重复返回同一 run | REL-01 |
| R04 | `handler/docs.go`、`handler/handler.go` | 持久文档清单；reindex 仅更新 demo 来源；启动恢复 BM25；删除不依赖内存标题表 | REL-02 |
| R05 | `handler/docs.go`、`rag/rag.go` | doc/version/chunk 稳定身份；新版本 staging 后激活；当前检索排除 superseded；历史引用可读 | REL-03 |
| R06 | `rag/embed.go`、`store/qdrant.go` | 持久模式禁止单次调用切换 hash；验证 embedding_space 与维度；Qdrant 错误不伪装 durable success | REL-04 |
| R07 | `cmd/server/main.go`、`store/qdrant.go` | 启动维度不符 fail closed，不自动 DELETE；模型变化显式新 collection 迁移 | REL-05 |
| R08 | `cmd/evalbaseline/main.go` | 评测独立 DB/collection/队列命名空间；清理需 ownership guard；业务 sentinel 不变 | REL-06 |
| R09 | `agent/react.go`、Planner、eval | 真实路径共用证据资格契约；无合格证据屏蔽操作建议；拒答字符串不是验收标准 | EVD-01, EVD-02 |
| R10 | `tool/prometheus.go`、`agent/planner.go` | HTTP/网络/解析错误完整传播；unknown/source_unavailable 与无告警分开；no_evidence 也不同于检索失败 | REL-07 |
| R11 | `handler/alert.go`、RAG | 关闭默认 auto_ingest；未审核 incident 不参与默认 runbook 检索；禁止自引用循环 | EVD-03 |
| R12 | `handler/alert.go`、队列 | 完整失败 payload、可恢复 notification outbox；已发送记录不重复诊断；独立通知消费者配额 | REL-08 |
| R13 | `cmd/server/main.go`、MCP | GET/POST 复用同一 transport handler；支持的 DELETE/关闭按选定模式统一；认证一致 | REL-09 |
| R14 | handler/HTTP/embed/store | body/batch/query/token/session 限额；context 逐层传递；超时后停止后续读写 | REL-10 |
| R15 | queue tests、evalbaseline | 回归改测业务契约；真实 handler/Planner 路径；fake LLM + 受控联调；禁止只测提示词 | QA-01 |

## 修复先后

先合并 R06/R07/R08 和明显的 R10 错误传播，防止验证过程损伤数据。再落地持久化文档/运行模型，统一修复 R03/R04/R05/R12，避免先修一套内存逻辑再整体替换。R01/R02/R14 与上述工作可在明确接口后并行，接真实访问之前必须全部过关。R09/R11/R15 是真实报告发布前的硬门槛。R13 可以独立小提交。

## 不允许的“修复”

1. 向量调用失败仍返回 nil error，只在日志加 warn。
2. 删除旧版本后再尝试 embedding，导致失败时没有最后可用版本。
3. 启动重新写 demo 就宣称用户文档已恢复。
4. 改注释或 README，让错误的去重测试继续通过。
5. 把“无匹配”加到模型建议前，就标记拒答成功。
6. 在通知队列名称上隔离，但继续让慢诊断占满全部执行槽。
7. 添加鉴权后让浏览器、AM 或 MCP 正常路径完全不可用。
8. 将关闭自动沉淀误解为关闭业务报告保存。

## R09 的最小安全契约

将“证据存在”“证据相关”“结论已核验”分开。`has_citations` 只能表示存在本轮合格来源引用，不是结论被证明。

本轮没有合格 runbook 证据：生产主路径返回固定的“当前未找到可用于处置的合格知识，请人工研判”及原始可观测事实，不附未经支持的变更步骤。依赖失败返回 source_unavailable，不叫 no_evidence。不要拦截不涉及处置的普通状态/错误文本。

向量余弦、BM25、RRF 分值分别保存，不能拿某个融合分值当概率；是否合格由版本有效性、环境/服务适用性、来源资格和经已标注正负例校验的门槛共同决定。现有小样本无法证明通用相关性能力，阈值不稳定时保守返回证据不足。首版不新增宣称万能的语义 verifier。

历史会话的 citations 不计入本轮资格。任何自动生成的处置建议必须能绑定到本轮来源片段；引用 ID 无效或无来源时不放行。对内容正确性的人工验证保持独立，不能把结构校验通过写成根因确认。
