# 摘录逻辑离线复现

来源：Acfufu/oncall-agent，提交 2e97742aae610b52535999a9851683320c349c6e。

这里不是完整仓库。excerpts.go 的函数/表达式摘自对应文件；拒答 guard 为便于独立测试增加了薄包装。没有连接真实模型、Redis、Qdrant、Prometheus 或 webhook。

四个 PASS 表示当前有问题的行为/机制可被复现，不表示原项目测试已通过。上传文档残留的整体结论仍依赖原 Upload/AddDoc 缺少替换清理的静态证据；本地测试只验证其内容变化会生成不同 point ID。

运行：`GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -v .`

此模块最低 Go 版本为本地复现环境的 1.23，与原项目要求 Go 1.25 是两回事。不要用该模块替代原项目构建或集成测试。
