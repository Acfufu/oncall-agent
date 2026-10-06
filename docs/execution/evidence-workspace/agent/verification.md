# Agent EVD-01 / REL-07 / REL-10

L1：fake HTTP 模型与 fake Prometheus、明确MemoryVector+HashEmbedder测试profile；不是生产向量或真实模型验证。源状态在 `source-state.json`。

先红调用（修复前）：
`go test ./internal/agent -run '^Test(ReActNoEvidenceReplacesEntireAdvice|ReActRejectsUnversionedEvidence|ReActToolFailureDoesNotBecomeNoEvidence|ReActFallbackFailureDoesNotBecomeNoEvidence|PlannerRetrievalFailureIsVisible|ReActHistoricalCitationsNotReused)$' -count=1`

结果：六场景FAIL，`red.log`逐项记录模型无检索危险建议、未版本化引用接受、tool/fallback检索错误吞并、Planner故障伪装无匹配、历史无本轮引用仍输出建议。修复后同调用 `green.log` PASS。

最终调用 `go test -race ./internal/agent -count=1`：全包PASS，artifact `race.log`，含旧会话上限/清理、fallback指标及以下新场景二元判定：

| 场景 | 二元observable |
|---|---|
| TestReActNoEvidenceReplacesEntireAdvice | 真正ReAct.Run返回完整SafeReport常量且citations空；模型危险建议完全丢弃 |
| TestReActRejectsUnversionedEvidence | 模型确实调用rag_search且内存有知识；无DocID/VersionID知识不成为处置证据 |
| TestReActToolFailureDoesNotBecomeNoEvidence | 真实rag_search→失败Embedder返回error，不继续模型生成/不转no_evidence |
| TestReActFallbackFailureDoesNotBecomeNoEvidence | fake模型500→真实fallback检索失败返回error |
| TestPlannerRetrievalFailureIsVisible | legacy PlanPushed输出来源不可用，绝不显示无匹配冒充依赖正常 |
| TestReActHistoricalCitationsNotReused | 同会话第一轮合格引用成功，第二轮不检索则SafeReport且引用空 |
| TestSafePlanSeparatesUnavailableFromEmpty | Prom500/非法JSON返回error；真正alerts=[]仅无firing；有firing但embedding失败error |
| TestSafePlannerVersionQualification | IndexVersion+ActivateVersions引用含完整DocID/VersionID/ChunkID；legacy未版本化知识返回SafeReport |
| TestSafePlannerCancellationStopsBeforeRetrieval | 已取消ctx经SafePlanPushed为context.Canceled且无引用 |

静态检查 `go vet ./internal/agent`，`vet.log` exit_code=0。

`SafePlanWithContext(ctx)` / `SafePlanPushed(ctx,alerts)` 返回显式error供handler/worker拒绝来源故障；旧方法保留签名、错误时稳定明示来源不可用。`SafeReport="当前未找到可用于处置的合格知识，请人工研判。"`。引用只能来自本轮rag_search实际命中，必须完整版本身份+非incident/space_metadata。两个旧指标/fallback fixture升级为真实版本激活，没有削弱断言。

本分工未编辑handler/main/tool/RAG。工具层SearchWithContext接线由检索分工处理；HTTP状态码由主管整合新SafePlan接口后验证。无模型质量/生产故障隔离验收声明。
