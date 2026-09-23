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

## v0.3+ 愿景

- [ ] 企业级知识库 (版本/权限/去重)
- [ ] 沙箱执行 (默认关)
- [ ] MCP/OTel深度集成
