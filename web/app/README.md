# Evidence Workspace frontend

Run `npm ci`, `npm run dev` in this directory. Vite proxies same-origin `/api` to Go `127.0.0.1:8819`; production uses `npm run build` output in `web/dist`, hosted by Go.

Live is the default. Open `/incidents?mode=demo` or use the explicit “进入演示数据” control for deterministic handoff fixtures. Demo never writes to live and is never selected by a failed request. Login exchanges an in-memory console token for an HttpOnly session; CSRF stays in memory. No browser persistent credential storage is used.

Checks: `npm run typecheck`, `npm test`, `npm run build`, `npm run test:browser`. Playwright can target a running Go deployment via `WORKSPACE_URL=http://127.0.0.1:8819 npm run test:browser`. Browser mocks are explicitly route-intercepted contract tests, not evidence of external service health. Runtime evidence is recorded under `.omo/evidence/frontend-workspace` from repository root.

Seven routes: `/dashboard`, `/incidents`, `/evidence`, `/workspace/metrics`, `/knowledge`, `/evaluations`, `/settings`. Global graph is M3 deferred; evaluations are unavailable until a real isolated result source exists; settings are read-only. Metrics accept only server-owned template IDs. Historical trends are not plotted without a time-series resource.
