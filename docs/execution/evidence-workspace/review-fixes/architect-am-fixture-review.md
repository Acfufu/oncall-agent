# Architect：两次 AM 失败定向审查

只读独立审查，未执行测试/改代码/操作容器。结论 REVISE fixture，当前不支持改生产归并逻辑。

第二轮 AM 日志明确：15:35:20.005 接收 firing，20.890 投递成功，20.974 第二提交仍 active，之后多次成功重复；不是网络/鉴权，也不是首次回调迟于关闭。退出后的 connection refused 是结果。资源争用仅假设，未证实。

官方 AM v0.34.1 `alert.Merge` 在新 endsAt 尚未被本机判过去时会保留旧较晚 EndsAt：[官方源码](https://github.com/prometheus/alertmanager/blob/v0.34.1/alert/alert.go#L38-L60)。host 的 time.Now() 边界可能导致整分钟延长。生产 ParseObservations / Admit 同标签 startsAt 归并 resolved 未见对应缺陷。

建议：startsAt 固定且足够早，resolved endsAt 明确过去；记录 AM HTTP Date 与 host now，优先用 AM Date-5s。保存两次输入、真实 callback status/endsAt、同一 incident 最终 SQL resolved、恢复不新增 run。主线程已将修正交隔离 L2 所有者；成功标准不放宽。
