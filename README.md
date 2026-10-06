# oncall-agent

Evidence Workspace combines Go / Gin, SQLite business facts, Redis / asynq, Qdrant retrieval, Prometheus / OTel and a React / TypeScript UI hosted by the same Go process. M0–M2 are implemented; M3 global exploration remains planned. Tools and alerts stay read-only: no acknowledgement, silence or automatic remediation.

[简体中文](README.zh-CN.md) · [Startup, migration, backup and rollback](docs/execution/evidence-workspace/operations.md) · [Verification and limits](docs/execution/evidence-workspace/verification.md) · [API contract](docs/api/workspace.openapi.yaml)

## Start

Requires Go 1.25+, a C compiler for SQLite CGO, Node 24 for building, Redis, Qdrant and an embedding service. The template uses Ollama `nomic-embed-text`. Embedding/Qdrant failures are explicit; production never substitutes Hash or memory storage. The workspace and deterministic cited diagnosis do not require an LLM key; chat and judge use the configured OpenAI-compatible model.

```sh
# Preserve existing configuration. Inventory and back up before upgrading.
cp -n config/config_template.json config/config.json
cp -n .env.example .env
(cd web/app && npm ci && npm run build)
docker compose up -d
# Set two different strong random credentials in your shell; never in frontend/Git.
# export ONCALL_CONSOLE_TOKEN=...; export ONCALL_WEBHOOK_TOKEN=...
export ONCALL_LOCALHOST_HTTP=true # loopback HTTP development only; omit for TLS
export ONCALL_CONFIG="$PWD/config/config.json"
go run ./cmd/server
```

Open [the local workspace](http://127.0.0.1:8819) and enter the console token to create an HttpOnly / SameSite=Strict session. Missing credentials fail closed. `/ping` checks liveness; `/ready` checks facts, queue and retrieval. Production runs Go only, without Node or Vite.

The optional container slice is `docker compose -f docker-compose.yml -f docker-compose.workspace.yml up --build -d`, with both tokens set first. It uses an independent SQLite volume and new collection, publishes the app on host loopback only and authenticates Alertmanager firing/resolved callbacks with the webhook token. Do not run `down -v` or remove old collections.

## Workspace and facts

Seven pages preserve the reference navy theme, shared navigation and individual layouts. Incidents provide a selectable list, evidence graph / accessible list, source inspector and persisted stage timeline. Knowledge supports versions, updates and withdrawal; historical citations retain immutable version/chunk snapshots. M2 adds sourced declared topology and three restricted metric templates, with null gaps and explicitly inferred potential impact.

The metric page is `/workspace/metrics`; `/metrics` remains the monitoring endpoint. Explicit `?mode=demo` carries a persistent demo label and disables management. Live failures never switch to fixtures. Unconfigured evaluation stays empty; global graph exploration is marked deferred.

SQLite persists incidents, runs, events, document versions, citations, outbox and source records. Admission is transactionally idempotent. Resolved observations update lifecycle without creating another diagnosis; a successful diagnosis does not establish recovery. Reports and incident notes cannot become qualified runbooks. Auto-ingestion defaults off. Missing relevant evidence produces a safe result without arbitrary model action suggestions.

## Authentication and interfaces

Business APIs, `/mcp` and `/metrics` require authentication. Console APIs accept console Bearer or a short same-origin session; cookie writes require Origin and CSRF. `/alert` uses a separate webhook Bearer. Plain HTTP credentials are accepted only in explicit loopback development mode.

|Endpoint|Purpose|
|--|--|
|`/api/v1/incidents`, `/runs/{id}`, `/incidents/{id}/graph`|Paginated incidents, runs, evidence graph and events|
|`/api/v1/documents`, `/documents/{id}/versions`|Versioned knowledge with CAS updates|
|`/api/v1/evidence/{id}`|Readable original sources and immutable citations|
|`/api/v1/incidents/{id}/topology`, `/metrics`|Environment-scoped topology and restricted server metric templates|
|`/api/v1/workspace/summary`, `/system/status`|SQL summaries, dependency states and last success times|
|`POST /alert`|202 admission followed by durable outbox dispatch|
|`/reports`, `/plan`, `/upload`, `/chat`, `/list`, `/delete`, `/session`, `/reindex`|Authenticated legacy adapters over the same domain facts|
|`GET/POST /mcp`|Shared StreamableHTTP transport sessions and read-only tools|
|`GET /metrics`|Prometheus scrape endpoint; console Bearer required, not traced|

For local-owner trusted STDIO use `go run ./cmd/server serve --mcp`. The same read-only allowlist applies. `deploy_events` is enabled only with a configured GitHub repository and reads commits/deployments. Notifications have a separate consumer and durable payload, with at-least-once delivery; recipients deduplicate by report ID. Notification failure does not block diagnosis or imply remediation.

## Configuration and recovery

Use `config/config_template.json`. Never commit `config.json`, `.env` or tokens. Configure the SQLite path, independent Qdrant collection / Redis namespace, embedding identity, UI rollback flags, optional controlled topology / metric templates and notification webhook. A different embedding space requires a new collection; the application never automatically deletes the old one.

`workspacectl inventory` and `dry-run` are read-only. `backup --output` uses consistent SQLite `VACUUM INTO` and refuses overwrite. Explicit `import-legacy --apply` is checksum-idempotent, preserves incomplete source markers and does not requeue historical work. Do not copy only the main file of an active WAL database. Restore after stopping the instance, retain the original database and select a new backup path. `/legacy`, `/v01` and `ui.legacy_default=true` provide UI rollback. See the operations guide for commands.

## Development and verification

```sh
gofmt -l cmd internal
go vet ./...
go test ./... -count=1 -timeout=3m
go test -race ./... -count=1 -timeout=3m
go build ./...
(cd web/app && npm run typecheck && npm run test && npm run test:browser)
# Optional: creates and cleans only explicitly owned isolated service containers.
ONCALL_L2=1 scripts/workspace-l2.sh
```

Do not use the old unisolated evaluation command. `evalbaseline` now requires explicit isolated configuration and ownership. Real model quality was not evaluated in this delivery. Logs, source fingerprints, seven-page / narrow-screen captures and measured performance limits are in the verification record. Fake providers, Hash embeddings and memory vectors exist only in explicit test profiles and do not establish model quality or production throughput.

[Apache-2.0](LICENSE)
