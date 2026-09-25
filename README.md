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
oncall-agent v0.1 listening on 0.0.0.0:8819 (memonly=false with Qdrant up)
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
   → cited diagnosis, visible via `GET /reports` and the console. The report
   is auto-ingested as an incident note (`source=incident`, retrieval
   down-weighted, same-name overwrite, `knowledge.auto_ingest` to switch off).
   Eval before regressing: `EVAL_ALERT=1 go run ./cmd/evalbaseline`.

## Routes

| Method | Path | What it does |
| --- | --- | --- |
| `GET` | `/ping` | Health check, `{"status":"ok"}` |
| `POST` | `/chat` | ReAct dialogue over read-only tools, cited reply |
| `GET` | `/plan` | Plan-Execute: firing alerts → retrieval → cited report |
| `POST` | `/alert` | Alertmanager webhook: pushed alerts → cited diagnosis (sync) |
| `GET` | `/reports` | Recent alert-driven diagnoses (in-memory ring, last 20) |
| `POST` | `/upload` | Ingest one Markdown runbook |
| `GET` | `/list` | List ingested titles |
| `DELETE` | `/delete` | Remove a title from the registry |
| `POST` | `/reindex` | Reload the demo library |
| `GET` | `/metrics` | Prometheus scrape endpoint (not traced) |

The console is served at `/`; the previous single-page UI stays at `/v01`.

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
compose: qdrant (:6333) · prometheus (:9090) · alertmanager (:9093) · otel-collector (:4317/:4318) · jaeger (:16686)
app: :8819 · embedder default: local Ollama nomic-embed-text (:11434) · LLM: OpenAI-compatible api_base + model + key
```

Layout follows `internal/` layers: `config`, `handler`, `agent`, `rag`,
`store`, `tool`, `observability`, `trace`. Decisions are pinned in
`docs/adr/0001-0005`; scope per release in `docs/ROADMAP.md`.

## Configuration and operations

Only `openai.api_key` is mandatory. Everything else runs on template defaults:

| Key | Default | Notes |
| --- | --- | --- |
| `server` | `0.0.0.0:8819` | App address |
| `openai.api_base` + `model` + `api_key` | OpenAI-compatible | Any compatible endpoint works |
| `qdrant` | `127.0.0.1:6334`, collection `oncallagent` | HTTP probed on `:6333`; falls back to memory |
| `embedder` | `127.0.0.1:11434`, `nomic-embed-text` | Dimension auto-probed at boot |
| `prometheus.url` | `http://localhost:9090` | Alert + query source |
| `knowledge` | `auto_ingest: true`, `incident_weight: 0.5` | Incident-note ingestion + retrieval down-weight (ADR-0005) |

Never commit `config/config.json` — it is git-ignored. MCP tool routing
(`modelcontextprotocol/go-sdk`) and the similarity floor are staged behind
`docs/adr/0004-mcp-otel.md`; the sandbox stays off and alert actions stay
read-only (no acknowledge, no silence).

## Known limitations

- Retrieval-layer refusal stays 0/2 by design: refusal is asserted at the
  generation layer (eval shows 2/2 "no relevant match" with `EVAL_GEN=1`);
  the similarity floor knob remains off by default.
- Rerank runs on the evaluation path only, not on live `/chat`.
- `/alert` diagnoses synchronously: a slow LLM can outlive Alertmanager's
  delivery timeout and trigger webhook retries (duplicate diagnoses);
  tune `group_interval`/`repeat_interval`, async job mode is a v0.5 item.
- No license file is declared yet.

## Development

```bash
gofmt -l .            # must be clean
go vet ./...          # must pass
go build ./...        # must pass
EVAL_NORERANK=1 go run ./cmd/evalbaseline   # retrieval baseline
```

Commits use Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`), one
atomic change each.
