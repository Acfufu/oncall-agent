# Retrieval reliability / version projection implementation

Scope: T02/T05; reliability R06/R07/R08/R10/R14 components within store/rag/Prometheus/eval. Parent owns server/handler/domain wiring. No business eval invocation or production resource mutation performed.

Baseline directly verified: production store silently switched to memory after failed write; remote embed returned hash after HTTP500; RecreateCollection issued DELETE; eval loaded business config and warmed production vectors. Red artifact captured the first two reproductions before editing. Existing fallback and incident-downweight tests were updated to ADR-0011 semantics, while preserving assertions for failed writes, independent storage mode, and obsolete incident chunks.

## Interfaces

- `store.NewVector` remains pointer construction but all network operations propagate errors. `NewMemoryVector` explicitly selects test memory. `EnsureCompatible(ctx,dim)` GETs existing dimension, creates only on 404, never deletes. Legacy `RecreateCollection` always refuses.
- `EnsureSpace(ctx,spaceID)` reads a reserved metadata point. Different space rejects even with same dimension. Existing unmarked nonempty collection rejects with migration guidance. New empty collection receives a non-retrievable metadata point. Parent computes provider/model/dimension/config fingerprint and calls both guards.
- `rag.EmbedContext(ctx,embed,text)` uses contextual remote embedding. Remote HTTP errors, empty vectors and malformed responses do not synthesize hash vectors. Legacy nil embed only accepts explicitly selected memory storage.
- `SetEmbeddingSpace(spaceID,dimension)` fixes expected space and vector size; IndexVersion/Search validate. `IndexVersion(ctx,docID,versionID,title,md,source,spaceID)` writes immutable version points, returns `[]VersionChunk`, restores BM25 metadata, never activates.
- `RestoreChunks([]VersionChunk)` restores sparse projection from SQL; Embedding may be nil. `ActivateVersions([]ActiveVersion)` atomically replaces the SQL active-version view. Each ActiveVersion holds DocID, VersionID and SpaceID. Dense filter and sparse eligibility run before ranking truncation; incident and metadata sources excluded.
- `DeleteVersionWithContext(ctx,versionID)` deletes disposable projection of exactly one version after SQL tombstone/active exclusion. Historical SQL chunks remain parent responsibility.
- Result keeps old doc/snippet/score/source and adds chunk_id/version_id/doc_id and separate nullable dense_score/bm25_score plus rrf_score.
- Eval default has no business configuration/network access: explicit memory+HashEmbedder only. Persistent/remote legacy eval refuses until an owned-resource provisioner exists. Old incident deletion removed. Remote GEN/JUDGE mode disabled with explicit migration message.

## Verification

All below are L1 fake HTTP / pure in-memory checks, not L2 service integration or model quality evidence.

| Scenario / criterion | Exact invocation | Binary observable | Artifact |
|---|---|---|---|
| Initial HTTP500 embedding and persistent failed write | `go test ./internal/rag ./internal/store -run 'TestRemoteEmbeddingRejectsFailure\|TestPersistentFailureDoesNotFallback' -count=1` | Both FAIL before fix (synthetic success) | `logs/retrieval-red.log` |
| REL-04/05: no silent fallback, dimensions/space mismatch, zero DELETE | `go test -race ./internal/rag ./internal/store ./internal/tool ./cmd/evalbaseline -count=1` | All four packages `ok`; assertions reject HTTP500/mismatch, DELETE count remains zero | `logs/retrieval-race.log` |
| REL-03/DATA-02: staging invisible; old active retained until switch; specified projection cleanup; sparse restart restore | same four-package race invocation | Version tests return exact v1/v2 chunk IDs; no staging/topK leak; unrelated active remains | `logs/retrieval-race.log` |
| REL-07: Prometheus error status/missing alerts/invalid JSON/500 versus true empty | same four-package race invocation | Error cases nonnil error; true empty nonnil zero-length result | `logs/retrieval-race.log` |
| REL-06: eval default isolation / persistent refusal | same four-package race invocation | explicit memory; unowned persistent and remote modes error before any resources | `logs/retrieval-race.log` |
| REL-10: canceled embed/write | same four-package race invocation | context.Canceled; fake Qdrant request count zero | `logs/retrieval-race.log` |
| Vet scope | `go vet ./internal/rag ./internal/store ./internal/tool ./cmd/evalbaseline` | exit 0 | `logs/retrieval-vet.log` |

Source binding: `logs/retrieval-source-sha256.log` records tested files. Earlier `retrieval-first-green.log` captures an intermediate failing check and is explicitly not pass evidence. `retrieval-green.log` passed targeted packages before added interfaces; refreshed final validation is `retrieval-race.log`.

Limitations: no isolated actual Qdrant/Redis/Prometheus L2 run by this child. No real model quality evaluation. Server main must wire guards and SQL projection recovery/activation; parent validates full user surface. Persistent eval is safely refused rather than falsely claiming complete owned-resource integration.
