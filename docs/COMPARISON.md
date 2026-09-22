# 对标 COMPARISON — yinxiangpingfan/OnCallAgent vs wangsaiCool/oncall-agent

Migrated 2026-09-22. 结论指导本项目选型。

| 维度 | yin (Go) | wangsai (Java) |
|---|---|---|
| 语言 | Go 1.25, Gin + Eino | Java 17, Spring Boot 3.2 + Spring AI Alibaba |
| LLM | OpenAI 兼容任意 | 锁定 DashScope qwen3-max |
| Embedding | Ollama nomic-embed-text 本地 | DashScope text-embedding-v4 |
| 向量库 | Qdrant | Milvus biz/biz_hybrid 双集 |
| Agent | RAG + ReAct + Plan-Execute-Replan | Agentic RAG + 经典RAG + AIOps多Agent |
| 检索 | 单路稠密，无rerank | Hybrid (稠密+BM25+RRF) + qwen3-rerank + 改写 + 去重 |
| 工具 | time/RAG/Prometheus/CLS MCP | time/docs/Prometheus/CLS mock/executePython沙箱 |
| API | /ping /upload /chat /chatStream /plan `:8819` | /api/agent /api/rag /api/ai_ops /api/eval `:9900` |
| 前端 | 无 | static控制台 (助手/知识库/评测) |
| 知识库 | docs/ 空 | aiops-docs 5篇 + uploads + reindex全套 |
| 评测 | 无 | eval-data + P/R/MRR规划 |
| License | README称MIT | Apache-2.0 |
| 成熟度 | 骨架轻，闭环通 | 工程重，企业级 |

选型启示：本项目取 Go路线 (传播+部署) + 补 wangsai的检索/控制台/评测。详见 ADR-0001~0003。
