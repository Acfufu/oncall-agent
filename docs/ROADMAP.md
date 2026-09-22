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

- [ ] Hybrid (BM25+RRF) + rerank
- [ ] 完整控制台 (知识库管理)
- [ ] 评测报告对比

## v0.3+ 愿景

- [ ] 企业级知识库 (版本/权限/去重)
- [ ] 沙箱执行 (默认关)
- [ ] MCP/OTel深度集成
