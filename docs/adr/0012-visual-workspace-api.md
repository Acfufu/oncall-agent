# ADR-0012 Visual Workspace & API

Status: Accepted（2026-10-06，本次用户授权）

实施 handoff/evidence-workspace 的 T00–T23，按 M0→M1→M2验证退出条件。M3全局图/GraphRAG/Semantica仅规划。新增 /api/v1 同源接口，旧HTTP接口通过同一领域层适配。React+TypeScript+Vite编译为Go托管静态资源，不增加生产Node；React Flow+ELK作有向证据图。旧UI保留 /legacy 与 /v01，可显式回退。

冻结身份、状态、边来源、字段与null语义为交接 docs/02、03、contracts/domain.ts；API采用 data/meta envelope和顶层error/code/retryable/request_id。无匹配、不可用、空数据、过期分开。图限定incident/run；引用绑定不可变version/chunk；不得将检索分数、judge评价当概率/正确率。生命周期恢复仅由有时间的来源观测确认，诊断完成不代表恢复。

七页shell、深色海军蓝、三列工作台和底部时间线依原图保留；每页语义修正按docs/04、10，独立例外记录。显式demo只用fixtures，失败不切演示。M2拓扑只从受控声明文件且每条边有来源，指标只允许受限服务端模板，缺值为null，关联不称因果。

任何公开部署、真实数据覆盖/删除、提交/推送均需用户另行授权。此次不自动执行。

指标页使用 `/workspace/metrics`，保留受保护的 `/metrics` Prometheus 抓取端点；SPA深链接由现有 Go NoRoute 托管。
