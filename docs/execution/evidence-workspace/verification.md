# Evidence Workspace 验收记录

范围：交接包 T00–T23 / M0→M1→M2；M3 deferred。HEAD 仍为 `d7a1fa65a91d46835a412b713abfa4145f36fa3b`，未提交、推送或部署。用户起始未提交内容为 handoff，未改写；用户 config.json/.env 未修改，未运行旧 evalbaseline。

L0=静态/契约；L1=实际 Go handler/worker + fake HTTP/临时 SQLite/显式 Hash、Memory；L2=一次性真实外部服务；L3=浏览器；L4=真实模型质量。不同等级不相互替代。

## 已执行证据与最终刷新

|检查|命令/环境|结果与证据|
|--|--|--|
|全仓测试|`go test ./... -count=1 -timeout=3m`，宿主 Go 1.27.1，go.mod 1.25|PASS，logs/final-go-test.log|
|全仓 race|`go test -race ./... -count=1 -timeout=3m`|PASS（修复测试无界等待后刷新），logs/final-go-race.log|
|静态检查|`go vet ./...`|PASS，logs/final-go-vet.log|
|构建|`go build ./...`|PASS，logs/final-go-build.log|
|针对审查修复|`go test -race ./internal/handler ./internal/workspace …`|PASS，review-fixes/final-qa-green-race.log；red 保留于 final-qa-first-race.log|
|迁移/备份/回滚|真实 workspacectl + 临时目录/fake Qdrant HTTP|PASS，migration/cli-scenarios.json；原配置与快照前后 hash 相同，拒绝覆盖备份|
|核心外部集成|一次性真实 Redis/Qdrant/Prom，真实 asynq，显式 HashEmbedder|PASS，l2/core-green.log、core-owner-manifest.json；包括真实停止/恢复和通知重试|
|完整外部集成与 AM|`ONCALL_L2=1 scripts/workspace-l2.sh`|PASS 17.24s，l2/verification.md、run.log、owner-manifest.json；四个 owner 容器已清理，不操作业务 compose/volume|
|容器镜像|`docker build -t test_oncall_workspace:20261006 .`，Go 1.25 CGO + Node 24|PASS，logs/final-docker-build.log；首次 TS1117 夹具编译 red 保留；最后长标题修正后已构建最终镜像，仅本地测试镜像|
|前端型检/单测/构建/浏览器|web/app package scripts|PASS：typecheck/build exit0、6单测、23浏览器20.6s，见 m2/frontend/verification.md（最后长标题修正后）|
|单 Go 容器实际表面|`python3 docs/execution/evidence-workspace/container_smoke.py`，非 root、临时独立层|PASS，container-smoke.json；登录、401、真实503、SQL 0600、同容器重启保留历史；最终长标题/路由调整后已重跑通过|
|格式与原交接完整性|`git diff --check`、`gofmt -l cmd internal`、原 SHA256SUMS 校验|PASS，logs/final-diff-check.log、final-gofmt-owned.log、handoff-integrity.log；全仓 gofmt 唯一输出为原交接 repro 两文件，未改写|
|最终独立审查|architect只读源码/日志/147源码SHA/27截图SHA复核|PASS，无尚存实质阻断；review-fixes/architect-final-review.md，未声称审查者重跑测试|
|模型质量|外部真实 LLM、真实 judge|NOT RUN / L4，未提供受控模型配置或预算；界面不显示虚构正确率|

## 验收 ID 对照

|ID|实现及可观察验证|证据/等级|
|--|--|--|
|SEC-01|统一 console/webhook 角色，业务接口及 MCP fail closed；Cookie HttpOnly，短期会话|auth/verification.md、auth/final-race.log / L1；最终浏览器补充 L3|
|SEC-02|Origin/CSRF、限流；原文文本显示；拒绝任意 PromQL/URL/超预算|auth、workspace_metrics_test.go、M2 race / L1|
|SEC-03|chat 无状态，忽略伪造 session_id，不读全局历史|chat_boundary_test.go、chat qualification / L1|
|REL-01|事务 business key 幂等，50 相同实际 HTTP 仅一条 run/outbox，顺序归一化、fallback、复诊|workspace 测试 / L1；l2/core-green.log / L2|
|REL-02|SQL 文档管理、活动版本重建、demo reindex 保留上传、撤下与持久 cleanup|workspace/cleanup_test.go、migration；真实 Qdrant restart/withdraw / L2|
|REL-03|暂存→远端写→SQL 激活；失败留旧版本；提交后取消与重复请求修复投影；历史不可变|review-fixes/cleanup-final-green-race.log、historical-report 红绿 / L1；V1→V2 restart / L2|
|REL-04|严格 embedding/context/Qdrant 错误，固定 provider/model/dim 空间；源恢复无内存成功伪装|retrieval-implementation.md、readiness_test.go / L1；停止/恢复 / L2|
|REL-05|不自动 DELETE/Recreate 旧 collection；不同模型/维度拒绝|store/reliability_test.go、readiness_test.go / L1；L2 incompatible probe|
|REL-06|eval 无默认业务配置，ownership 验证、隔离命名与自建清理；业务 sentinel 保护|cmd/evalbaseline/isolation_test.go / L1；未执行旧评测|
|REL-07|Prometheus 真空与 HTTP/JSON/timeout 错误区分；plan 通过同 SQL 事实链|tool/prometheus_reliability_test.go、workspace_plan_test.go / L1；M2真实样本 / L2|
|REL-08|持久通知 outbox，独立 asynq 消费者；完整原告警 JSON；每次实际发送 stage_attempt|integration/retry_event_test.go / L1；真实 HTTP500→204 / L2；at-least-once 非 exactly-once|
|REL-09|MCP 单 transport 会话 GET/POST/SSE/关闭与认证|auth/mcp.log、internal/mcpserver/transport_test.go / L1|
|REL-10|body/batch 界限；共享 30s HTTP 预算与父取消；90s worker、bounded terminal commit|final-qa 初期四路径80ms父预算实际504；最终压力回归2s父预算、相同deadline、504、embed仅一次与SQL chunks0，见 review-fixes/budget-stress-green.log / L1|
|EVD-01|真实 chat/alert 路径安全摘录；无本轮相关知识则 SafeReport，不释放模型任意建议|shared-safety/implementation.md、chat_qualification_test.go / L1|
|EVD-02|逐 incident/run/attempt 的引用与 evidence source_refs，图边有来源；无跨运行替换|workspace integration/attempt 栅栏 / L1；L2真实引用；最终L3来源检查器|
|EVD-03|报告与 incident 不进入合格知识；source_kind 与 verification 分开；auto_ingest 关|evidence_guard、integration 测试 / L1|
|DATA-01|诊断成功不代表故障恢复；resolved 不新诊断；迟到 firing 不覆盖较晚状态|workspace_test.go / L1|
|DATA-02|文档更新/撤下后旧 report/chunk/图仍读原快照；旧告警状态不被后来的 resolved 改写|historical-report 红绿、workspace历史测试 / L1；L2 restart/withdraw|
|DATA-03|admission+outbox 原子、recover lease/fencing、旧 attempt 不能写引用终态、事件顺序持久|workspace integration/retry_event / L1；真实 asynq / L2|
|API-01|新旧路由、DTO/null、认证、过滤分页、CAS、独立 error/meta|api-contract/api01.log、workspace_contract_test.go / L1；OpenAPI 不声称全规范自动校验|
|UI-01–UI-05|七页按原图公共 shell/分页面布局；三列画布/检查器/时间线；键盘列表、窄屏、快速切换、live 错误不转 demo|m2/frontend/demo 与 live 七页、visual-exceptions.md、m2/frontend/logs/browser.log / L3；长标题DOM界限/完整title断言与实际截图目检通过|
|VIS-01|queue 与通知按实际 started 计数，重试阶段事件持久，skipped 不计成功；旧栅栏编号不冒充执行次数|review-fixes/final-qa-green-race.log / L1，L2通知真实重试|
|M2-01|有来源的声明拓扑、循环/跨环境/namespace 校验，潜在影响仅 inferred，原配置 source 可读|m2/verification.md、m2/race.log / L1；最终L3联动|
|M2-02|三模板，24h/step/series/points/body/timeout 限额；缺点 NaN/Inf=null；标明辅助观测非因果|m2/race.log / L1；l2实际 Prom series/finite samples / L2|
|QA-01|fake 模型进入真实 handler/agent/worker；测试断言业务事实，不独立提示词评测|baseline、agent/auth/shared-safety、integration / L1；核心L2|
|PERF-01|默认60/120、上限300/600受控图测量；分页长列表；不每 tick ELK 重布局|m2/frontend/controlled/performance-{60,300}.json；浏览器DOM点击→Inspector、纯ELK与HTTP端到端分别测量 / L3|
|OPS-01|SQLite migration/备份新路径/恢复、UI legacy 回退；新模板与独立 collection/namespace；容器单 Go 托管|migration/cli-scenarios.json / L1；container-smoke.json、l2/verification.md / L2；最后前端重建后同容器持久恢复与路由smoke PASS|

## 限制与解释

- L1/L2 的 HashEmbedder、MemoryVector、controlled sample 均是显式测试 profile，不在生产故障路径启用。
- 相关性门控采用保守的词项/环境匹配，非语义正确率或根因置信度。合格引用仍显示未核验适用性。
- 不使用概念图运营数值；summary 只来自 SQL，样本不足显示积累中、comparison=null。Prometheus 是辅助观测，不充当历史引用。
- 单实例 SQLite；未验证多副本/多租户/真实模型质量；M3 全局探索 deferred。
- 历史红日志包含编译错误时明确标注，不把它们称为行为复现。源码指纹见 source-state-final.json；最终147文件与27张截图已绑定，独立 architect 审查结论保存于 review-fixes/architect-final-review.md。

## 最终证据使用规则

镜像实际启动：/workspace/metrics 返回SPA，原/metrics匿名401、console返回Prometheus文本；/ready真实503、upload source_unavailable503，SQL0600且非root uid10001，旧报告显式导入后同容器重启仍可读。owner匹配后清理，见 container-smoke.json。

浏览器截图明确分为demo参考比较、L1实际Go/SQLite、controlled API故障/性能三类，具体路径见 m2/frontend/verification.md；真L2服务独立见 l2/verification.md。首次回归夹具定位timeout与TS1117编译失败的红日志不声称是产品行为复现。

源码及前端产物绑定见 source-state-final.json、m2/frontend/source-sha256.txt；L2源码指纹与现Go源码相同，不用重跑旧评测或以未执行检查代替事实。全仓 gofmt 两个原handoff repro文件是保留用户原文件的明确例外。

## 最终性能与源码绑定

Chromium145.0.7632.6、Apple M5/10CPU/32GiB、1586×992；共享宿主有并发工作，非生产基准。60/120纯ELK197.9ms、DOM节点点击→Inspector标题与关闭控件可交互8.4ms；300/600为577.5ms/27.4ms。默认图<500ms及Inspector<100ms目标在本次受控样本达到；300图不宣称<500ms。初次load+fit1409/2716ms，列表切换+选择+sourceHTTP152/284ms独立保留。heap是Chromium估计，非RSS/泄漏验证；未宣称生产吞吐提升。大型ELK lazy chunk仍约1.64MiB，Vite警告保留。

最终源码147文件 SHA汇总见 source-state-final.json；原L2已测96文件全部仍与当前字节一致。七页与其他状态27张截图路径/尺寸/哈希见 m2/screenshots-final.json；前端单独完整源码和build指纹见 m2/frontend/source-sha256.txt。最终Go检查后仅frontend和文档修改，未将旧后端证据冒充改变后的代码。

测试资源收尾：四个L2 owner容器与最终smoke owner容器已逐个校验归属清理。最后18820 profile由隔离console POST /test/quit正常退出，长驻Go测试最终PASS601.31s（仅启动/服务生命周期，不代替上述UI场景）；随后/ping连接失败000。临时owned SQL事实保留，不改业务库，不停止用户Dockerdaemon。

提交检查（用户后续授权提交/推送）：暂存284文件时，完整 `git diff --cached --check` 对原样保存的诊断日志（空格/ASCII标识）及Vite/ELK生成bundle报行尾空格。为保留测试证据与源码绑定，未修改这些生成字节。排除仅原始*.log及web/dist的 authored-source/doc staged diff检查exit0，见logs/publication-source-diff-check.log；此前未暂存 diff检查不代表新生成文件无行尾空格。配置、.env、原handoff、node_modules与SQLite文件不在提交范围。源码147SHA仍匹配最终验证。
