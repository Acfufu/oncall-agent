# 磁盘空间满处置

## 现象

磁盘使用率超 85%，告警 `DiskFull` firing，写操作开始失败。

## 排查

1. 看分布：`df -h` 定位满的分区，再 `du -sh /* | sort -rh | head` 找大户。
2. 看日志：`/var/log` 下 `ls -lSh | head`，确认是否日志轮转失效。
3. 看 Docker：`docker system df`，悬空镜像/停止容器占空间常是元凶。

## 处置

- 短期：清过期日志（保留 7 天），`docker image prune -a` 清悬空镜像，扩容分区。
- 长期：修 logrotate 配置，日志加配额，超 80% 即告警提前介入。
