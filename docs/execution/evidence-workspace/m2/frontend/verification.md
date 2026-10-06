# Frontend final verification — 2026-10-07

Source ownership: web/app and web/dist. Final source/build hashes: `source-sha256.txt`. No commits, production deployment, destructive evaluation or business-library cleanup performed.

## Exact checks and observables

| Scenario | Invocation (repo root unless noted) | Binary observable | Artifact |
|---|---|---|---|
| TypeScript contract/build | `npm --prefix web/app run typecheck`; `npm --prefix web/app run build` | both exit 0; final static assets emitted | logs/typecheck.log; logs/build.log |
| Domain states / API cancellation | `npm --prefix web/app test` | 6 passed, including succeeded diagnosis + missing queue terminal is unknown, failed diagnosis + pending notification remains running | logs/unit.log |
| Browser surfaces | `cd web/app && npx playwright test` | 23 passed (20.6s) | logs/browser.log |
| All seven demo pages | same full Playwright invocation, tests/workspace.spec.ts | fonts and route-specific asynchronous content ready; each distinct screenshot | demo/*-1586.png |
| Responsive incident workspace | same invocation at 390×844, 768×1024, 1280×800, 1440×900, 1600×1000 | document scrollWidth <= viewport width | demo/incidents-{390,768,1280,1440,1600}.png |
| Pagination beyond defaults | tests/pagination.spec.ts in same invocation | incident25, document25, version25, event120 visible; second-page refresh reflects rename/deletion | controlled/pagination-knowledge.png |
| Lifecycle server scope | same test file, 20 active records + 21st resolved | selecting resolved sends status=resolved; 21st appears without loading earlier pages | controlled/lifecycle-server-filter.png |
| Receive-time and lowercase query | same test file | server CPUHigh with year2000 starts_at remains visible for cpu query | logs/browser.log |
| Metrics units and gaps | tests/metrics.spec.ts | three independent stacked panels share start/end/step; null makes separate path segments | controlled/metrics-topology-controlled.png |
| Topology provenance / isolation | same file | declared edges solid; distinct topology legend; potential impact resolves evidence; A delayed source cannot appear after B service selection | controlled/topology-scope-switch.png; logs/browser.log |
| Auth + document writes + keyboard | tests/workspace.spec.ts | CSRF + If-Match observed; modal Tab/ShiftTab trapped; Escape modal/Inspector clears | logs/browser.log |
| L1 actual application flow | `cd web/app && node scripts/live-evidence.mjs` against http://127.0.0.1:18820 | exit0, result passed=true; actual document indexed, alert accepted, persisted run succeeded/has_citations, Inspector contains actual version | live/result.json; live/http.json; live/live-run.json; live/live-graph.json; live/live-cited-inspector.png |
| L1 no evidence | same script, unindexed environment alert | real run no_evidence; no fabricated citations | live/live-no-evidence.json; live/live-no-evidence.png |
| Failure stays live | same script injects list503 in browser route | visible API failure; no demo substitution | live/live-failure-injected-no-demo.png; live/result.json |
| All seven live pages | same script using final dist | fonts + specific async route readiness before screenshot | live/live-{dashboard,incidents,evidence,metrics,knowledge,evaluations,settings}-1586.png |

Live profile uses explicitly isolated MemoryVector/Hash and controlled Prometheus samples labelled test_profile=L1. This proves the real authenticated application/API/SQLite path in the authorized L1 test profile, not production LLM/vector/Prometheus availability. Browser 503 is explicitly injected, not a claim of a real external outage. Storage topology is undeclared in this live profile and honestly shows its qualification error; configured topology positive path is controlled browser evidence. HTTP response bodies redact tokens/secrets/passwords; credentials are not persisted.

## Performance

`tests/performance.spec.ts` runs 60 nodes/120 unique edges and 300/600. Apple M5 arm64, 10 logical CPUs, 32 GiB; Chromium 145.0.7632.6, 1586×992. Shared host and concurrent build activity are not controlled benchmark conditions.

Latest full-suite samples: 60/120 initial load+fit 1409ms, pure ELK 197.9ms, accessible-list toggle+selection+source HTTP 152ms, browser DOM click→Inspector heading/close control 8.4ms. 300/600: 2716ms, 577.5ms, 284ms, 27.4ms respectively. Chromium estimated used heap is recorded in controlled/performance-{60,300}.json; it is not process RSS or a leak test. DOM interaction timing excludes source HTTP and Playwright auto-wait. Default pure layout <500ms and Inspector interactive <100ms observed in this sample; no production or universal performance guarantee. 300-node layout has no specified upper-limit threshold; initial-load/network timings are retained separately.

Dashboard/knowledge/settings entry does not import the graph/ELK chunk until a graph route. Initial bundle ~288KB, gzip~76KB. Lazy graph chunk remains ~1.64MB / gzip~504KB and Vite reports its chunk-size warning; no threshold suppression was added.

## Review repairs / retained red evidence

Lifecycle is now an App-owned server query coordinated with environment/service/time/search and summary, not a loaded-first-page local filter. Polling refreshes all loaded cursor pages. Topology selection/evidence is bound to incident+version; old source fetches are cancelled and request-key render protection hides old scope before effects. Notification timeline derives completion independently from notification_status.

logs/browser-review-fixture-red.log preserves an initial topology fixture navigation/button location timeout. Revision gating was investigated; the final normalized fixture passed. The red run lacks a source fingerprint and does not establish the exact initial cause. logs/browser-review-detail-fixture-red.log preserves lifecycle regression initial detail fixture shape error. These fixture setup failures are not claimed as reproductions of the original production defects. logs/typecheck-review-red.log preserves duplicate meta-property compiler failure, corrected before final type/build and final full browser pass. Earlier settings readiness and /metrics Prometheus route collision reds are retained. Canonical application metrics route is /workspace/metrics; /metrics retains monitoring.

M3 global graph and unconfigured evaluations remain explicit deferred/unconfigured states. Evidence-page event scope and visual exceptions remain the parent acceptance record; this report does not claim implemented global graph.

Final visual repair: long unbroken alert names and version IDs wrap within140px and clamp to2 lines; complete title attributes/list/Inspector retain full text. Both 60/300 browser cases assert the long-label DOM bounds stay inside its node, two-line clamp, anywhere wrapping and complete title. Latest actual live-cited-inspector.png was opened and inspected after this repair.
