# 容器 OOMKilled 处置

## 现象

容器频繁重启，`kubectl get pod` 显示 `OOMKilled`，告警 `ContainerOOMKilled` firing。

## 排查

1. 查重启记录：`kubectl describe pod <pod>` 看 Last State 是否为 OOMKilled。
2. 查内存曲线：Prometheus `container_memory_working_set_bytes{pod="<pod>"}` 确认是缓慢泄漏还是突发尖峰。
3. 查应用日志：搜 OOM 前 GC 频繁 / 大对象分配 / 缓存无上限增长。

## 处置

- 短期：上调 `resources.limits.memory`，先止血恢复。
- 长期：修泄漏（缓存加 TTL/上限，JVM 调小 `-Xmx` 留余量给堆外内存）。
