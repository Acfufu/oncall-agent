# 七页视觉与语义例外 / UI-05

主线程已实际打开全部7张原图，基准1586×992；参考 manifest 的 SHA 保留，未修改 PNG。公共令牌在 web/app/src/tokens.css，布局在 style.css。实现是 React DOM/React Flow/ELK，不使用截图背景。

本次截图有两类，不能混同：七页参考比较使用显式 `?mode=demo`（顶栏持续“演示数据”，管理操作禁用）；实际 HTTP/SQLite 闭环使用隔离 L1 服务，来源/故障状态来自其业务记录，不能证明生产依赖或模型质量。真实外部服务另由 L2 验证。

|页面|preserved|semantic-adjusted / unavailable / deferred|实现与最终截图|
|--|--|--|--|
|UI-S01 仪表盘|208px侧栏、64px顶栏、紧凑KPI、左重点事件、中趋势/分布、右健康、底部活动表|移除自动关闭/重启/根因概率；SQL分母与统计口径明确。只有记录日期组成趋势，无足够样本显示积累中；未知依赖不涂绿。没有预测风险/MTTR/虚构审计人名|App.tsx/History.tsx；m2/frontend/demo/dashboard-1586.png|
|UI-S02 工作台|事件列、中央画布、右来源检查器、跨后两列底部时间线；海军蓝/细边框/类别色/焦点描边|生命周期、诊断、证据、通知独立显示；置信度改待核验；模型建议不充当证据；评分/通知未启用非成功。无日志搜索/自动处置/无来源假设。山形装饰省略|Workspace.tsx/Graph.tsx；m2/frontend/demo/incidents-1586.png|
|UI-S03 证据图谱|左范围选择、中央图/过滤/邻域、右检查器、底部真实路径事件，复用同一source事实|M1左列限定事件/run选择而非全库分类；全库网络、社区/力导向、自动根因路径全部deferred。标题直接说明M3延后；图数量仅当前快照，保留截断说明|Workspace.tsx/Graph.tsx；m2/frontend/demo/evidence-1586.png|
|UI-S04 指标联动|事件/服务范围、单位分开的叠放图、共享时间窗、来源与查询详情、事件联动；M2服务拓扑|仅三个受控模板，不填六组虚构曲线；缺点/null保留空隙；配置拓扑declared与potential_impact inferred分开。无相关系数/建议回滚/任意SQL或PromQL/URL。无指标能力时明确未配置而非假曲线|Metrics.tsx；m2/frontend/demo/metrics-1586.png；live-metrics来源样本另列|
|UI-S05 知识库|文档列表、中原文/版本标签、右元信息与版本历史的三列|来源不等于审核；正文作为不可信文本，保留Markdown原文；活动版本/历史/撤下可区分。阅读数/贡献者/平均正确率等未采集隐藏。demo管理禁用；重载demo为独立受控库动作|Knowledge.tsx；m2/frontend/demo/knowledge-1586.png|
|UI-S06 评测|左数据集/配置、中结果与历史、右案例/比较，蓝紫/紧凑表格位置|未配置可追溯评测时空态、运行按钮禁用并注明隔离要求。百分比与秒数独立说明；模型自评不当正确率。不扩建远程平台、不执行旧评测、不显示静态30/30|App.tsx；m2/frontend/demo/evaluations-1586.png|
|UI-S07 设置|左类别、中接入摘要、右状态/维护指南，同尺寸只读组织|无密钥回显/保存/眼睛；不声称密钥加密存储；Redis为任务队列。时区/减弱动画/退出为实际操作，其他配置只读；清库替换受控维护说明；没有无凭据连接测试或虚构可用率|App.tsx；m2/frontend/demo/settings-1586.png|

通用修正：真实API失败保留error/unavailable与最近成功时间，不跳demo。Live过滤服从server first_received_at/大小写无关口径；演示才本地筛选。轮询刷新所有已加载页，不将旧后页标fresh。图节点与关系有source_refs；证据列表提供不依赖颜色/hover的访问路径，选中仅描边，不改业务状态。手机是主从与全宽来源抽屉，不是缩小桌面。

## 主线程目检记录

2026-10-06已用 view_image 检查当前7页：六页的海军蓝、三列/图/表格和上述语义布局可见。第一次指标截图停在React.lazy加载空态，已拒绝作为最终证据并要求等待实际页面ready重拍；随后已实际查看 ready 后的最终叠放三图截图及 L1 受控样本图，单位、共享时间窗与来源标签可见。截图差异不使用任意像素百分比作为通过标准。

窄屏/其他视口、实际来源/无证据/失效/历史/重试/指标样本以及性能路径见最终m2/frontend/verification.md及verification.md；未拍状态不能视为截图覆盖。

补充语义修正：终态 run 缺少某阶段终态事件时，显示“终态未记录”，不继续标 running；服务拓扑采用 declared/observed/inferred 图例，与证据关系图分开。指标页 canonical 路径为 `/workspace/metrics`，保留原 `/metrics` Prometheus 接口；此为 API 兼容调整，页面布局不变。

最终目检：2026-10-07再次实际打开最终L1引用检查器、三组受控指标及390窄屏。图节点长无空格名称与版本ID已限定140px、两行换行/省略，DOM不越界回归通过；完整文本保留title、可访问列表和Inspector。最终27张截图路径/尺寸/SHA见m2/screenshots-final.json，含7张参考视口demo、7张L1 live、5视口与来源/故障状态。
