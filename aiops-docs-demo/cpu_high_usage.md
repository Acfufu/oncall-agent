# CPU 高负载处置

## 现象

CPU 持续 >90%，告警 `CPUHighUsage` firing。

## 排查

1. `top` 看进程，定位热点 pid。
2. 查该进程日志，看 GC / 死循环 / 大key。
3. 查 Prometheus `rate(process_cpu_seconds_total[5m])` 确认是否单副本倾斜。

## 处置

- 短期：限流 / 扩容 / 杀热点。
- 长期：profile 优化热点函数。

> 引用测试：诊断须引用本片段。
