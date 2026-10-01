# ADR 0010 — /reports 持久化（JSON 快照）

- Date: 2026-10-02
- Status: Accepted（v0.8 立项拍板：JSON 快照零新依赖；SQLite 方案否决——环存 20 条报告杀鸡牛刀）

## Context

GET /reports 落内存环（20 条），重启丢历史（2026-10-02 活验实锤：boot 1 的 AM 自动诊断在重启后消失）。v0.6 通知载荷自包含后外发侧已不依赖环，但人侧回看历史（尤其 failed/低分复盘）仍需进程存活。重启丢历史与「诊断落 /reports 带状态」的分发承诺不符。

## Decision

- **存储 = 单 JSON 文件快照**，路径 `reports.persist_path`（Default `data/reports.json`，显式空串=关闭）。零新依赖，stdlib `encoding/json` + 原子写（tmp + rename，同目录）。
- **写时机 = 每次环变更同步落盘**（add/update 终态均触发；诊断链每条报告约 3 次写，频率低，同步无碍）。持环锁内写——以微小 IO 阻塞换写序正确，报告环为低频热路径，可接受。
- **读时机 = boot 装配后加载**，按文件序注入环（截到环容量 20）；文件不存在视为空启动，损坏文件告警后按空启动，不挡 boot（降级 doctrine）。
- **失败可观测**：写失败不挡主链，warn 日志 + `reports_persist_errors_total` 计数器（F04 同姿态）。
- **不做**：fsync（断电可能丢最后一条，接受并文档化）；环容量配置化（维持 20）；跨进程多写者竞争（单进程假设，与现状一致）。

## Consequences

- 重启后 /reports 历史保留（环容量内），failed/低分可复盘。
- 新增磁盘写面：报告环变更即 IO；路径不可写时持续 warn+计数（可观测降级，不挡诊断）。
- data/ 目录进 .gitignore；README 已知局限补 fsync 缺口。
