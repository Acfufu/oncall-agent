# P99 延迟突增处置

## 现象

P99 延迟突增但 CPU 正常，告警 `P99LatencyHigh` firing。

## 排查

1. 看分位：`histogram_quantile(0.99, rate(http_request_duration_seconds_bucket[5m]))` 确认是整体慢还是长尾。
2. 看依赖：trace 按 span 耗时排序，定位慢的是 DB / 下游 RPC 还是 GC 停顿。
3. 看 DB：慢查询日志 + 连接池等待，`SHOW PROCESSLIST` 抓当前慢 SQL。

## 处置

- 短期：慢依赖加缓存/降级，DB 加缺失索引，连接池调大。
- 长期：P99 接入 SLO 看板，发布自动对比延迟基线，回归即回滚。
