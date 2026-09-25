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
- [ ] 拒答落生成层：react prompt「引用不相关也明示未找到相关匹配」；evalbaseline 加 EVAL_GEN 生成层负例回归
- [ ] delete 删全：store 按 payload doc 过滤删点 + source 标记(demo/upload)；reindex 同步清 demo 消失/陈旧向量
- [ ] rerank live 验：本地模型 (:1234 qwen3-vl-8b 或 :11434) 出实数
- [ ] MCP/OTel 验收 (ADR-0004，首刀已落 119fe31)：16686 chat→rag→tool 树；9090 `http.server.request.duration`+`rag_hits_total`

> 验收关 (跑完填实数)。
> - 拒答：生成层 2/2 负例明示未找到相关匹配；正例 recall@3 不回归 (30/30)。
> - delete：删一篇 → rag_search 检不出；reindex 后 demo 消失文档向量清零，上传文档不动。
> - rerank：eval 报告 rerank/RerankFused 列实数。
> - MCP/OTel：Jaeger trace 树 + Prom 两指标实测。

## v0.4+ 愿景

- [ ] 企业级知识库 (版本/权限/去重)
- [ ] 沙箱执行 (默认关，另立 ADR-0005)
- [ ] MCP/OTel 深化 (工具面扩展/采样策略)
