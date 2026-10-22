# 03 · API 契约

**本节为待实现的新契约，不表示当前仓库已暴露这些路径。** 前端只使用同源 `/api/v1`；旧接口由兼容 adapter 保留。实现时将本节落成项目内 OpenAPI 与 DTO 测试；本包 `domain.ts` 仅覆盖工作区读模型，不冒充完整 OpenAPI。

## 1. 基础形状

成功响应：

```json
{
  "data": {},
  "meta": {
    "request_id": "req_...",
    "as_of": "2026-10-05T14:32:18Z",
    "data_state": "fresh",
    "mode": "live",
    "revision": 7,
    "next_cursor": null,
    "truncated": false
  }
}
```

`mode=demo` 的数据永远有“演示数据”标签；不能从 API 故障自动推导 mode=demo。`data_state` = fresh/stale/partial/unavailable。empty 是一份成功的数据集长度为零，不是 unavailable。

错误响应保留旧客户端能显示的顶层字符串：

```json
{
  "error": "Prometheus 暂时不可用",
  "code": "source_unavailable",
  "retryable": true,
  "request_id": "req_...",
  "details": {"source": "prometheus"}
}
```

不得泄露 token、原始内部栈或带密码的 URL。HTTP 400 参数不合法；401 未认证；403 权限/CSRF 拒绝；404 不存在或不可见；409 版本/幂等冲突；413 超大小；429 超速率；503 依赖或配置未就绪；504 端到端超时。no_evidence 是完成的领域结果，不强制变 5xx。

所有时间用 RFC3339 UTC；duration_ms 为非负数或 null。缺失的数值返回 null，不能以 0 代替未采样。游标分页稳定排序 `(received_at DESC,id DESC)`，固定筛选条件；cursor 非法或换筛选返回 400。默认 20，最大 100。内部 id 不作为秘密，但仍逐次鉴权。

## 2. 认证与浏览器会话

单机两种凭据：console/admin token 和 webhook token，配置引用环境变量，不写在前端 bundle 或 URL。server 不存在已配置 key 时，受保护 live 接口 fail closed 503；允许查看静态登录页、/ping，不能匿名读图和上传。

| 方法与路径 | 行为 |
|---|---|
| POST `/api/v1/auth/session` | 通过 Authorization Bearer 提交 console token，校验后签发短期 HttpOnly session cookie，并返回 CSRF token；不把长期 token 存 localStorage |
| GET `/api/v1/auth/session` | 返回当前会话状态/CSRF token/能力，不返回长期密钥 |
| DELETE `/api/v1/auth/session` | 验证 CSRF 后注销 |

浏览器写请求校验 Origin/CSRF；cookie 在 HTTPS 使用 Secure + SameSite=Strict，开发 localhost HTTP 例外需显式模式。登录失败限速；默认会话 TTL 8h、总量 100，可配置。纯 Bearer 的服务调用不依赖 cookie CSRF，但必须校验 scope。AM token 仅能 POST /alert；MCP token 或 console token 的工具权限限既有只读工具。

图、文档片段、run_events 与报告均经过同样认证，不能只保护写入口。frontend CORS 默认不开；反代同源部署。状态接口区分认证前的 liveness 与认证后依赖详情。

## 3. 工作台读接口（M1）

| Endpoint | 请求要点 | 返回 / 规则 |
|---|---|---|
| GET `/api/v1/system/status` | 无 | 启用能力、每个依赖 fresh/stale/unavailable、上次成功探测时间、配置指纹，不含密钥 |
| GET `/api/v1/workspace/summary` | environment/service/from/to | KPI 值、分子分母、样本数、覆盖期、比较期；无覆盖时 null |
| GET `/api/v1/incidents` | 状态/环境/服务/时间/cursor/limit | 事件摘要；latest_run 摘要；使用 lifecycle_status，不混任务状态 |
| GET `/api/v1/incidents/{id}` | 可选 run_id | incident、关联 run 列表摘要；无权限/找不到 404 |
| GET `/api/v1/runs/{run_id}` | 无 | run 状态、独立通知状态、报告、citation refs、revision |
| GET `/api/v1/runs/{run_id}/events` | after_sequence=0；limit≤200 | 按 sequence ASC 追加事件；同 run 单调 sequence；meta.has_more + next_after_sequence |
| GET `/api/v1/incidents/{id}/graph` | run_id；focus_id；depth=1；node_limit=60；edge_limit=120 | GraphSnapshot；深度最大2、节点最大300、边最大600；返回截断及省略数量 |
| GET `/api/v1/evidence/{evidence_id}` | evidence_id 为有类型的不透明 ID | 已脱敏原始查询/文档片段/观测、来源、版本、hash、时间和核验信息；不执行新查询 |

graph 的 nodes/edges、referenced evidence 与 run_id 必须属于同一可见空间。不存在的 focus_id 返回400或稳定回到事件根节点，并告知原因；不偷偷跨事件查找同名实体。

`revision` 用于忽略旧请求晚返回；GET 读取可使用 ETag。先切换事件 A→B 后，即使 A 请求晚返回，也不能覆盖 B。graph 使用同一 DB 读快照组装，返回 `run_id` 与 `as_of`，不混多个时间版本。

### KPI 定义（同一筛选范围）

- 活跃事件：当前持有 active 状态且落入筛选定义的事件数。覆盖不完整时加“已接入数据”，不称全平台总数。
- 无证据事件：所选事件最新 completed run 的 evidence_status=no_evidence 数量；source_unavailable 单独显示，不混入无证据。
- 平均诊断时延：已终止 runs 的 `finished_at-started_at` 平均值，排队时延另列；注明样本数及是否含失败。默认含 succeeded/failed、不含 cancelled，与 summary 元数据一致。
- 含引用报告占比：succeeded runs 中至少一条合格本轮引用的数量/总 succeeded；**不命名为正确率**，分母零时 null。
- 环比：只有当前和比较窗口覆盖可比时计算；未积累数据显示“暂无对比”，不硬编码 ±33%。

M1 无时间序列数据时顶部 mini chart 改为“历史数据积累中”状态；M2 summary/history 接口有真实观测后再绘制。

## 4. 运行触发与旧接口

| Endpoint | 契约 |
|---|---|
| POST `/alert` | 保留 AM webhook。firing 批量规范化/幂等后 202 `{id,status:"queued",received,...}`；duplicate 返回同 id 和实际兼容状态。纯 resolved 通知接受并更新生命周期，200 `{status:"accepted",received:n,diagnosis_enqueued:false}`，不触发推理 |
| POST `/api/v1/incidents/{id}/runs` | console 身份；Idempotency-Key 必填；新人工复诊 202 `{run_id,status,dispatch_state}`；重复同键同体返回原 run；同键异体409 |
| POST `/chat` | 保留基本消息形状，默认无历史。旧任意 session_id 不得作为访问历史的凭据；对需要历史的旧客户端明示迁移 |
| GET `/plan` | 保留原调用方式；经相同业务服务捕获可追溯运行事实；上游不可用返回明确503，不输出无告警。同步耗时受端到端预算限制 |
| GET `/reports` | 从 SQLite 生成原先形状，默认最近20条；succeeded→done，未评分 null→旧 score=0；旧字段语义保留，新增字段应向后兼容 |
| GET `/ping` | 存活检查，不表示依赖健康 |
| GET `/ready` | readiness；日志/容器探针使用；依赖失败503；敏感详情只在授权 system/status 返回 |
| `/mcp` | 仍仅暴露既有只读工具；一个 transport handler；适用方法统一认证；STDIO 不把图管理写能力暴露成工具 |

新 alert 无已就绪队列 503；事务接受后出现短暂投递故障按 outbox 契约返回 durable accepted，并将 pending 可视化。保持 accepted 不等于 worker 已开始。旧 /plan 与 /chat 的安全修正改变失败表现是允许的，必须更新文档，不维持旧的不安全结果。

## 5. 知识文档（M1）

| Endpoint | 请求 / 返回 |
|---|---|
| GET `/api/v1/documents` | source_kind、query、cursor、limit；返回文档、active_version、index_status |
| POST `/api/v1/documents` | `{title,content,source_kind:"upload",environment}`，Idempotency-Key；同步有界激活成功201；索引失败503且不声称激活 |
| PUT `/api/v1/documents/{id}` | If-Match=当前 active version；相同 hash 幂等200；新版本安全激活200；并发版本冲突409 |
| GET `/api/v1/documents/{id}/versions` | 版本列表、活动状态、引用数量；稳定分页 |
| GET `/api/v1/documents/{id}/versions/{version_id}` | 原 Markdown、chunk 列表、内容hash、来源、索引状态 |
| DELETE `/api/v1/documents/{id}` | CSRF + If-Match；当前检索立即禁用；物理清理未完成202且 cleanup_pending，完成200；不宣称删除所有历史快照 |
| POST `/api/v1/knowledge/reindex-demo` | 管理权限，显式确认；只影响 demo source；完成返回文档/版本统计，不重置全部注册表 |

旧 `/upload`、`/list`、`/delete`、`/reindex` 适配同一领域服务；标题只能在旧默认上传 namespace 唯一解析，多个匹配返回409，不能按标题删除全部来源。staging 超时不能被新查询召回；重试根据 Idempotency-Key 返回该操作确定状态/最后有效版本。

## 6. M2：拓扑和指标

GET `/api/v1/incidents/{id}/topology`：environment/service；返回服务子图，每条边包含 declared/observed、source_ref、valid_from/to。没有导入配置则 capability=not_configured 和空结果，不生成假依赖。

拓扑首版从受控本地文件加载；无需网页任意写图。服务 ID 必须包含环境/命名空间。依赖配置更新应产生版本，不修改历史快照。影响传播使用反向依赖但只标“潜在影响”，不自动升级为实际故障。

GET `/api/v1/incidents/{id}/metrics`：metric_id（后端模板名）、start/end/step。模板在后端映射 PromQL，前端不能提交任意 upstream URL，不给任意 PromQL 入口。后端验证时间窗≤24h、step≥15s、series≤20、每series≤2000点、query timeout≤10s、响应体限额；模板未知400，采样不支持明确报错。

后端访问 Prometheus `/api/v1/query_range`。解析结果中的浮点字符串，NaN/±Inf 归一为 null，不能当0；没有数据留断线，不连接缺失区间。若上游只返回 histogram 类型而模板预期 float，返回 unsupported_result_type，不伪造均值。[SRC-09]

指标不会因“与告警同时间”自动入证据图；只有持久的 metric_observation（查询、时间窗、样本摘要/完整性、来源）才能成为图证据。M2 无 metric_observation 存档时仅提供实时辅助图表，明确它不属于旧报告当时证据。

## 7. 默认资源限制

这些是初始配置值，不是压测证明：HTTP body alert≤1MiB、batch≤100、chat≤32KiB、message≤12KiB、文档≤512KiB、chunks≤512、人工复诊≤10/min/身份、login≤5/min/IP。普通只读查询≤120/min/身份；图限额见上。超限明确413/429并给 Retry-After。

HTTP ReadHeaderTimeout=5s、IdleTimeout=60s；普通请求总预算30s、诊断任务预算90s（实际模型耗时需配置）、底层 HTTP 独立10–15s且服从上层 context。不要对未来 SSE 套死统一 WriteTimeout；本阶段没有 SSE。

所有客户端取消必须传到检索、embedding、Qdrant、Prometheus 与批次循环。取消后不能继续新建后续工具调用。已提交的事务/队列任务不会因为 HTTP 断开自动撤销；人工取消是独立未来能力，UI 不显示未实现按钮。
