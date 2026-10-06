# M0 → M1 → M2 可运行切片

本次不提交/push/部署。共同启动命令见 operations.md；依赖故障直接展示 unavailable，只有 `?mode=demo` 使用明确标记的演示。HEAD 未变化，所有改动为可审查工作树。

|里程碑|交付切片|已执行可观察验证|后续|
|--|--|--|--|
|M0 / T00–T12|原Go服务内统一SQLite事实与不可变知识；严格检索错误/空间；认证会话；幂等告警/outbox/attempt栅栏；安全报告；独立通知；共享MCP transport|全仓test/race/vet/build；隔离Redis/asynq/Qdrant/Prometheus/Alertmanager：50重投1run、版本恢复/撤下/旧引用可读、来源停止/恢复、no_evidence、真实500→204通知、AM firing→显式resolved无新run、Redis停止拒绝admission|向同一事实层接M1，不替换后端/业务库|
|M1 / T13–T18|构建后的Go托管七页React；公共shell和分页面布局；事件画布/可访问列表/来源检查器/真实时间线；知识版本管理；旧UI回退|七页/五视口浏览器、分页/CAS/CSRF/键盘/快速切换回归；实际L1新文档→告警→引用版本检查器、无证据、注入503不转demo。L1显式测试提供方与L2真实服务分开记录|以真实source接M2；全局图只显示deferred|
|M2 / T19–T23|声明式拓扑每边有存档source，潜在影响为inferred；受限指标三个模板/三图共享窗/null；SQL摘要真实日期/分母；迁移备份/回滚文档和单Go镜像|拓扑/指标targeted race；实际Prometheus有限采样；实际单Go容器认证/故障/SQL权限/重启历史；前端来源/图例/终态/窄屏/性能测量|不扩自动处置/写工具/企业权限/全局网络；M3只规划|

最终源码、命令、截图与独立审查统一由 verification.md、task-status.json、source-state-final.json及m2/frontend/verification.md绑定。历史红日志保留且标明产品缺陷、测试夹具错误与环境不可用；后续通过不抹除原始失败。

外部真实LLM/embedding质量（L4）、多副本、多租户及生产吞吐未验证；不能用Hash/fake样本或模型自评替代。性能阈值按最终实测与具体计时范围记录，不把页面加载/网络计时当纯Inspector交互。
