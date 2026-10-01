# oncall-agent

**The on-call agent that answers from runbooks, with citations — or says it found nothing.**

Alert in, cited diagnosis out: a Go service pairing Prometheus alerts with a
Hybrid-retrieved runbook library, over OpenAI-compatible LLM, Qdrant, and Prometheus.

Docs · [Quickstart](#quickstart-10-minutes) · [Routes](#routes) · [Why cited answers](#why-cited-answers)

English · [简体中文](./README.zh-CN.md)

> **Note**
> Every diagnosis carries its sources. No matching runbook means an explicit
> no-match statement — never an invented procedure.

```bash
cp config/config_template.json config/config.json  # add your LLM key
docker compose up -d
go run ./cmd/server
```

```text
oncall-agent v0.7.3 listening on 127.0.0.1:8819 (memonly=false with Qdrant up)
```

## Quickstart (10 minutes)

Prerequisites: Go 1.25+, Docker, git, and one LLM key. Everything else is
pre-provisioned by compose: Qdrant, Prometheus, the OTel collector, and Jaeger.

```bash
cp config/config_template.json config/config.json  # fill in api_key, the only required secret
cp .env.example .env
docker compose up -d        # Qdrant :6333 + Prometheus :9090 + OTLP :4317 + Jaeger :16686
go mod tidy && go run ./cmd/server
```

### Verify it worked

```bash
curl -s http://localhost:8819/ping
```

```json
{"status":"ok"}
```

Then open `http://localhost:8819` for the console, or ask the API directly:

```bash
curl -s -X POST http://localhost:8819/chat \
  -H 'content-type: application/json' \
  -d '{"message":"CPU usage is over 90%, how do I triage?"}'
```

The reply contains the answer plus `citations` (`{doc, snippet}`).
An empty citation list means the library had no match.

### Your next moves

1. **Load the demo library.** `POST /reindex` ingests the seven
   `aiops-docs-demo/` runbooks (CPU, service-down, OOM, disk, P99, MQ, TLS).
2. **Run the baseline.** `EVAL_NORERANK=1 go run ./cmd/evalbaseline`
   replays 32 seeded questions: recall@3 1.0 on 30 answerable items;
   `EVAL_GEN=1` also verifies generation-layer refusal (2/2 out-of-library
   probes answer "no relevant match", never invented).
3. **Watch a diagnosis.** `GET /plan` pulls firing Prometheus alerts and
   returns a cited report; traces land in Jaeger at `:16686`.
4. **Push an alert.** The compose stack ships Alertmanager plus a demo
   always-firing rule (`ContainerOOMKilled`): Prometheus → AM → `POST /alert`
   → `202 {id, status:"queued"}` on the spot; a Redis-backed worker finishes
   the diagnosis and the cited report lands in `GET /reports` and the console
   (entries carry `id`/`status`/`score`). The report is auto-ingested as an
   incident note (`source=incident`, retrieval down-weighted, same-name
   overwrite, `knowledge.auto_ingest` to switch off).
   Eval before regressing: `EVAL_ALERT=1 go run ./cmd/evalbaseline`.

## Routes

| Method | Path | What it does |
| --- | --- | --- |
| `GET` | `/ping` | Health check, `{"status":"ok"}` |
| `POST` | `/chat` | ReAct dialogue over read-only tools, cited reply |
| `GET` | `/plan` | Plan-Execute: firing alerts → retrieval → cited report |
| `POST` | `/alert` | Alertmanager webhook: pushed alerts → `202 {id, status:"queued"}`, worker completes the diagnosis (async, ADR-0006) |
| `GET` | `/reports` | Recent alert-driven diagnoses (in-memory ring, last 20) |
| `POST` | `/upload` | Ingest one Markdown runbook |
| `GET` | `/list` | List ingested titles |
| `DELETE` | `/delete` | Remove a title from the registry |
| `POST` | `/reindex` | Reload the demo library |
| `GET` | `/metrics` | Prometheus scrape endpoint (not traced) |
| `GET`+`POST` | `/mcp` | MCP server over StreamableHTTP: the three read-only tools (ADR-0007) |

The console is served at `/`; the previous single-page UI stays at `/v01`.

## MCP server (v0.5)

The three read-only tools (`time_now`, `rag_search`, `prometheus_query`) are
also exposed over MCP (ADR-0007) — allowlist semantics unchanged, diagnostics
agent not exposed:

- **StreamableHTTP**: point any MCP client at `http://localhost:8819/mcp`.
- **STDIO** for local clients (Claude Desktop / MCP inspector):

```bash
go run ./cmd/server serve --mcp        # no LLM key needed — tools only touch Qdrant/Prometheus
npx @modelcontextprotocol/inspector go run ./cmd/server serve --mcp
```

```json
{
  "mcpServers": {
    "oncall-agent": {
      "command": "go",
      "args": ["run", "./cmd/server", "serve", "--mcp"]
    }
  }
}
```

## Why cited answers

On-call advice is only useful when you can check it. The contract is fixed
at the tool layer:

- **Read-only tools, allowlisted.** `time_now`, `rag_search`,
  `prometheus_query` — anything else is refused by `IsAllowed`.
- **Hybrid retrieval.** Dense vectors plus in-memory BM25 (CJK bigrams,
  RRF k=60), fused and title-boosted ×1.5.
- **Floor-gated honesty.** A similarity floor drops weak hits; below it the
  service reports no match instead of guessing.
- **Observed depth.** Gin server spans plus per-tool spans flow through OTLP
  to Jaeger; request metrics are scraped straight from `/metrics`.

## Architecture

```text
Prometheus alerts ──▶ /plan ──▶ Hybrid RAG ──▶ cited report
Alertmanager ───────▶ /alert ──▶ same chain ──▶ cited report + incident note
Operator question ──▶ /chat (ReAct + 3 read-only tools) ──▶ cited answer
Runbook .md ──▶ /upload · /reindex ──▶ Qdrant + BM25 mirror
```

```text
compose: redis (:6379) · qdrant (:6333) · prometheus (:9090) · alertmanager (:9093) · otel-collector (:4317/:4318) · jaeger (:16686)
app: :8819 · embedder default: local Ollama nomic-embed-text (:11434) · LLM: OpenAI-compatible api_base + model + key
```

Layout follows `internal/` layers: `config`, `handler`, `agent`, `queue`,
`rag`, `store`, `tool`, `observability`, `trace`. Decisions are pinned in
`docs/adr/0001-0007`; scope per release in `docs/ROADMAP.md`.

## Configuration and operations

Only `openai.api_key` is mandatory. Everything else runs on template defaults:

| Key | Default | Notes |
| --- | --- | --- |
| `server` | `127.0.0.1:8819` | App address |
| `openai.api_base` + `model` + `api_key` | OpenAI-compatible | Any compatible endpoint works |
| `qdrant` | `127.0.0.1:6334`, collection `oncallagent` | HTTP probed on `:6333`; falls back to memory |
| `embedder` | `127.0.0.1:11434`, `nomic-embed-text` | Dimension auto-probed at boot |
| `prometheus.url` | `http://localhost:9090` | Alert + query source |
| `knowledge` | `auto_ingest: true`, `incident_weight: 0.5` | Incident-note ingestion + retrieval down-weight (ADR-0005) |
| `queue` | `redis_addr: 127.0.0.1:6379` | Diagnosis queue (Redis hard dependency, ADR-0006) |
| `notify` | `webhook_url: ""` | Notification write-back; empty = off (ADR-0008) |
| `reports` | `persist_path: data/reports.json` | `/reports` ring persistence across restarts; empty string = memory-only (ADR-0010) |
| `deploy` | `github_repo: ""`, `github_token: ""` | Deploy enrichment source; empty repo = `deploy_events` not registered (ADR-0009) |

### Deploy enrichment (v0.7, ADR-0009)

Set `deploy.github_repo` to `owner/name` and a fourth read-only tool
`deploy_events` joins the whitelist — ReAct prompt, `/chat` tools array, and
the MCP surface (`/mcp`, `serve --mcp`) all follow the same single source.
The tool synthesizes a timeline from GitHub commits (native `since`/`until`
window filter) and deployments (client-side filtered; that endpoint has no
window params), 10 events each, newest first; args are optional `since` /
`until` (RFC3339, default last 24h). The repo is pinned by config — the tool
takes no repo parameter, so it can never become an arbitrary repo probe.
Alert-driven reports gain an observational `deploy_events` field (flat
`env/sha/message/time` array) fetched over `[earliest startsAt − 24h,
startsAt]` — changes precede alerts — and pass through to the notify payload
additively. It is never part of `citations`: refusal semantics and eval
assertions are untouched. Failure degrades to an empty array and a log line;
counters: `deploy_events_calls_total` / `deploy_events_errors_total` /
`deploy_events_total`. Still read-only — not remediation. Anonymous GitHub
API allows 60 req/h; set `github_token` for private repos or heavier use.

### Notification write-back (v0.6, ADR-0008)

When a diagnosis settles on `low_score=true` (judge scored below
`judge.low_threshold`) or `status=failed`, the full report — same JSON shape
as a `GET /reports` entry — is POSTed to `notify.webhook_url`. Delivery runs
as a dedicated asynq queue: at-least-once, up to 3 retries with exponential
backoff (5s/10s/20s). The payload is self-contained, so ring eviction or a
restart never affects an in-flight notification. Receivers should deduplicate
by `id` (one report may arrive more than once). A drop after retry
exhaustion is logged and counted in `notification_failed_total` (successes
increment `notification_sent_total`); `/reports` entries carry no
notification state. Notification is outbound only — it is not remediation:
the service never acknowledges, silences, or resolves alerts.

Never commit `config/config.json` — it is git-ignored. MCP tool routing
(`modelcontextprotocol/go-sdk`) and the similarity floor are staged behind
`docs/adr/0004-mcp-otel.md`; the sandbox stays off and alert actions stay
read-only (no acknowledge, no silence).

## Known limitations

- `/reports` persistence (ADR-0010) writes an atomic tmp+rename snapshot on every ring change but does **not fsync** — a power loss may lose the last entry. Set `reports.persist_path` to `""` for memory-only.

- Retrieval-layer refusal stays 0/2 by design: refusal is asserted at the
  generation layer (eval shows 2/2 "no relevant match" with `EVAL_GEN=1`);
  the similarity floor knob remains off by default.
- Rerank runs on the evaluation path only, not on live `/chat`.
- **`POST /alert` is async since v0.5 (BREAKING)**: it returns
  `202 {id, status:"queued"}` and results arrive via `GET /reports`; v0.4
  clients reading the sync body must migrate. The queue runs on Redis
  (compose presets it); with the queue unwired `/alert` answers `503`.
- The `/reports` ring is in-memory: history is lost on restart. Queued tasks
  survive in Redis and are re-delivered after a restart; same-name incident
  overwrite keeps re-runs idempotent. Ring cap is 20 — a heavy backlog can
  evict a `queued` entry before its worker finishes. Notification payloads
  are self-contained, so eviction or a restart never drops an in-flight
  notification (ADR-0008).
- Default binding is loopback-only: `server.host` defaults to `127.0.0.1` and
  compose publishes every port on `127.0.0.1`. Containers reach the app via
  `host.docker.internal`, which resolves to host loopback on Docker Desktop.
  On Linux `host-gateway` maps to the bridge gateway, where a loopback-bound
  app is unreachable from containers — set `server.host` to `0.0.0.0`
  explicitly and protect it at the network layer.
- The MCP surface (`/mcp`, `serve --mcp`) is unauthenticated by design — same
  posture as the rest of the read-only HTTP API; protect it at the network
  layer.

## Development

```bash
gofmt -l .            # must be clean
go vet ./...          # must pass
go build ./...        # must pass
EVAL_NORERANK=1 go run ./cmd/evalbaseline   # retrieval baseline
```

Commits use Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`), one
atomic change each.

## License

[Apache-2.0](LICENSE)
