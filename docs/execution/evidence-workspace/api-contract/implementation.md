# API-01：实际实现合同

`docs/api/workspace.openapi.yaml` 是 OpenAPI 3.1 YAML，描述当前 Workspace、M2 topology/metrics、console auth、legacy、health、Prometheus/MCP HTTP 表面。未添加 M3 或未实现路径。Snake_case、nullable DTO、标准 envelope、auth 小 envelope 与 legacy 无 envelope 区别明确。记录 Idempotency-Key、If-Match、过滤绑定游标、事件序号、资源预算和 cookie CSRF。

`go test ./internal/handler -run TestAPI01 -count=1 -v`：api01.log 记录 PASS。测试实际注册 Workspace/M2 路由，与 paths/methods 双向对照；通过临时 SQLite、显式 Memory/Hash 与真实 Gin httptest 请求，递归检查200/201/409响应 DTO 的必需字段及基本类型，涵盖 summary.history、文档历史、run、graph、未配置 topology/metrics。完整 handler race 日志位于 ../shared-safety/final-full-race.log。

不声称完整 JSON Schema 规范验证器校验、所有错误分支枚举测试或真实外部依赖验证。认证、M2正常查询和真实浏览器验收由其负责线程提供；此合同无部署动作。generate.py 是合同生成源，模型 schema 从当前 Go DTO 提取，其余约束人工核对当前 handler。
