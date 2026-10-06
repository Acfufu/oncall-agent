# Workspace API / legacy adapter implementation

Scope: `internal/handler/workspace.go`, its tests, and `internal/tool/rag_tool.go` contextual retrieval fix. No other handler/server edits by this child.

`RegisterWorkspace(engine, service, prom, config) *WorkspaceHTTP` registers `/api/v1` read/write routes. Returned object's `Alert`, `Reports`, `Upload`, `List`, `Delete`, `Reindex` are legacy handlers for main registration. `DemoDir` defaults to `aiops-docs-demo`; main may set it. Shared authentication/CSRF/rate controls remain main middleware ownership; tests below deliberately exercise handler/domain behavior with explicit local fake dependencies.

List endpoints return `data` arrays. Details return `data` objects; incident detail wraps `{incident,runs}`, write-document wraps `{document,version}`. Meta is live/fresh with request ID, UTC as_of, revision, pagination and truncation; no automatic demo substitution. Cursor contains stable last `(time,id)` and the endpoint/query filter hash, with filter-change rejection. Time comparisons parse timestamps to avoid RFC3339 variable-fraction ordering errors.

Graph requests enforce node/edge/depth bounds and reject foreign focus or unrelated run. Default graph returns complete bounded event scope; explicit focus/depth selects an undirected evidence neighborhood. Version read validates parent document ID. Event lists use ascending sequence/after_sequence, next sequence and has_more.

Document create requires Idempotency-Key; update/delete require exact If-Match. Index failure returns503 rather than claiming activation. Legacy upload replaces only the current upload namespace with service compare-and-swap and immutable history. Legacy deletion only resolves one upload namespace match and leaves demo/history intact. Reindex requires explicit `{confirm:true}` on both new and legacy paths; older clients must add confirmation. The default document lists include tombstones so deletion/index state remains inspectable; legacy list hides withdrawn titles.

Alert adapter applies1MiB body limit, domain ParseObservations, date validation, durable admission+outbox dispatch and true duplicate state. Pure resolved returns200 and no inference. Accepted dispatch errors remain durable pending. Reports read only SQLite-derived compatibility shape; DB failure is503, never empty success. System status actually probes DB, queue, retrieval, Prometheus and records last-success probe timestamps; configuration fingerprint is hashed and contains no raw endpoint/token.

RAG tool now calls `SearchWithContext` and exports `tool.ErrSourceUnavailable`; missing RAG and real retrieval errors cannot become empty success. `errors.Join` preserves context cancellation and source-unavailable classification. Child recorded prior missing-source/cancel regression before changing implementation.

## Evidence

- `logs/retrieval-tool-red.log`: `go test ./internal/tool -run 'TestMissingRAGIsUnavailable|TestRagToolCancellation' -count=1`, both regression failures before fix.
- `logs/retrieval-tool-green.log`: `go test -race -v ./internal/tool -run 'TestMissingRAGIsUnavailable|TestRagToolCancellation' -count=1`, actual tool tests PASS.
- `logs/workspace-http-tests.log`: `go test -race -v ./internal/handler -run TestWorkspace -count=1`; seven real handler + temporary-SQL scenarios PASS after parent fixed the reproduced incident INSERT placeholder mismatch. Includes same-key/different-title409, manual JSON reordering idempotency and foreign-run graph404.
- `logs/workspace-http-partial.log`: independently passing document-version/history/legacy,413 body, invalid observation dates and closed DB503 cases. Explicit L1, no real external service coverage.

- `logs/workspace-http-full-race.log`: `go test -race -v ./internal/handler ./internal/tool -count=1`; both entire packages PASS. Legacy async completion now asserts deterministic `agent.SafeReport` and zero unversioned citations; legacy explicit auto-ingest tests assert notes stay out of trusted retrieval. These legacy fixtures are not production SQL facts.
- `logs/workspace-http-vet.log`: `go vet ./internal/handler ./internal/tool`, exit0.
- `logs/workspace-http-source-sha256.log`: tested source binding.

These are L1 explicit temporary SQLite, memory+hash test profile, fake queue and httptest HTTP validation. Main auth, real external services, built frontend and browser checks are parent-owned and not represented as verified here. Graph envelope now uses Revision/AsOf from the same SQL read snapshot returned by Service.Graph. Full handler/tool race validation and source hashes were refreshed after this change.
