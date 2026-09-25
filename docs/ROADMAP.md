# ROADMAP

## v0.1 最小闭环 (只读，可开箱)

- [x] Gin: /ping /upload /chat /plan
- [x] Eino ReAct (time/rag/prometheus只读) + Plan-Execute (拉告警→检索→报告)
- [x] Qdrant单路召回 + 标题加权
- [x] 极简单页 (聊天+诊断按钮+引用)
- [x] upload/list/delete/reindex
- [x] eval sample 20问占位
- [x] compose: qdrant + prometheus + demo知识，Key必填

## v0.2 质量

- [x] Hybrid (BM25+RRF) + rerank
- [x] 完整控制台 (知识库管理)
- [x] 评测报告对比

> 验收关 (2026-09-22，按拍板放行)。
> - Hybrid：recall@3=1.0(30/30)；拒答0/2接受，Floor=0，v0.3再调。
> - 控制台：`/`切console.html，/v01留档；eval填实数；delete半残记v0.3。
> - rerank码合未live验，待RERANK_BASE_URL实测。

## v0.3 收口 (2026-09-26 立项)

主题：还清 v0.2 全部遗留债 + 补 MCP/OTel 验收。沙箱/企业库推 v0.4+。

- [x] 余弦 Floor 标定证伪封存：7 库无可分界（e3109bf），Floor 旋钮保留，拒答验收不挂检索层
- [x] 拒答落生成层：react prompt「引用不相关也明示未找到相关匹配」；evalbaseline EVAL_GEN 负例回归（8a9bec9）
- [x] delete 删全：store 按 payload doc 过滤删点 + source 标记(demo/upload)；reindex 同步清 demo 消失/陈旧向量（33dc187，活体验收过）
- [ ] rerank live 验：本地模型 (:1234 qwen3-vl-8b 或 :11434) 出实数
- [x] MCP/OTel 验收 (ADR-0004，首刀 119fe31 + 指标补埋 5c4cf11)：2026-09-26 实测过，ADR 转 Accepted

> 验收关 (2026-09-26 实跑，LM Studio 未起导致部分数字为降级环境所得)。
> - 拒答：react prompt 已升级；EVAL_GEN 实跑 gen_err=2——LLM 端点(config.json→localhost:1234)未启动，2/2 待 LM Studio 起后补跑。
> - delete：upload→chat 引用命中→/delete→/list 摘除→chat 不再引用，活验通过；内部语义有 internal/rag/delete_test.go 回归。
> - recall@3：29/30（HashEmbedder 降级环境，Ollama 未起；与 v0.2 的 30/30@nomic-768 不可比，起 Ollama 后应复核）。
> - MCP/OTel：16686 见 POST /chat→Run.run→Tool.exec:rag_search→RAG.search+ChatModel 六 span 树；9090 实测 `http_server_request_duration_seconds_count{http_route=/chat}` 与 `rag_hits_total=3`。旁注：Qdrant 旧 collection 为 768 维(nomic)，Ollama 未起时 app 按 hash 64 维降级 memonly（预置行为，非回归）。
> - rerank：待本地模型。

## v0.4+ 愿景

- [ ] 企业级知识库 (版本/权限/去重)
- [ ] 沙箱执行 (默认关，另立 ADR-0005)
- [ ] MCP/OTel 深化 (工具面扩展/采样策略)
