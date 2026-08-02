# Lineage — Milestones

Living progress tracker. Update status markers as work lands and link the commit that
completed a task. Architecture specs are in [`docs/`](docs/) (`00`–`19`); this file
tracks *execution* against them.

**Legend:** ✅ done · 🚧 in progress · ⬜ not started · 🔮 future

## Roadmap

```mermaid
flowchart LR
    M0["M0 · Design docs"] --> M1["M1 · Scaffold"]
    M1 --> M2["M2 · Persistence"]
    M2 --> M3["M3 · Storage"]
    M2 --> M5["M5 · Model API complete"]
    M3 --> M4["M4 · Delivery hardening"]
    M5 --> M4
    M5 --> M7["M7 · Lineage graph"]
    M4 --> M6["M6 · Admin UI"]
    M5 --> M8["M8 · Observability"]
    M2 --> M9["M9 · Helm / deploy"]
    M5 --> M10["M10 · SDK & CLI"]
    M5 --> M11["M11 · Model insights"]
    M6 --> M11
    M11 --> M12["M12 · Version portrait"]

    M5 --> M13["M13 · EU risk classification"]
    M13 --> M14["M14 · Evidence export"]
    M13 --> M16["M16 · EU modification review"]
    M11 --> M16
    M16 --> M14
    M5 --> M15["M15 · Retention &amp; hold"]
    M15 --> M14

    M3 --> M17["M17 · OCI/ORAS driver"]
    M4 --> M17

    classDef done fill:#1f7a3d,stroke:#0d3d1e,color:#fff;
    classDef active fill:#b45309,stroke:#7c3a06,color:#fff;
    classDef todo fill:#334155,stroke:#1e293b,color:#fff;
    class M0,M1,M2,M3,M4,M5,M6,M7,M8,M9,M10,M11,M12,M17 done;
    class M13,M14,M15,M16 todo;
```

**Next up:** M13 → M14 (phases 1–3 of `15.7`) is the shippable near-term slice — it answers
the GPAI obligation that has been in force since Aug 2025. M15 follows, because a retention
story is what makes an evidence bundle credible rather than decorative.

## Status Summary

| # | Milestone | Docs | Status |
|---|---|---|---|
| M0 | Architecture & design docs | `00`–`19` | ✅ |
| M1 | Go scaffold (single binary, ports & adapters) | `01` | ✅ |
| M2 | Persistence: per-dialect MetadataStore + migrations | `02` | ✅ |
| M3 | Storage: S3 backend, signed URLs, upload flow | `05` | ✅ |
| M4 | Delivery hardening: cache↔events, fetch, `lineage://` | `04` | ✅ |
| M5 | Model API completeness + OpenAPI | `03` | ✅ |
| M6 | Admin UI: BFF + web console | `06` | ✅ |
| M7 | Lineage & provenance graph | `07` | ✅ |
| M8 | Observability: metrics, traces, SLOs | `09` | ✅ |
| M9 | Deployment: Helm chart + profiles | `08` | ✅ |
| M10 | SDK & CLI (OpenAPI-generated) | `10` | ✅ |
| M11 | Model insights: fingerprint, footprint, evaluations | `11` | ✅ |
| M12 | Version portrait: generated fingerprint + portrait marks | `12` | ✅ |
| M13 | EU risk classification & drift | `16` | ⬜ |
| M14 | Evidence export: Annex XII / Annex IV bundles | `18` | ⬜ |
| M15 | Retention, legal hold, Merkle audit sealing | `19` | ⬜ |
| M16 | EU modification review (Art. 25) | `17` | ⬜ |
| M17 | OCI/ORAS storage driver | `05.3.1` | ✅ |

**Open work:** M13–M16, the compliance set specced in `15`–`19` (`ddf4654`). No blocked
decisions — `00.11.11`–`14` are all resolved. M3's OCI/ORAS checkbox, the one pre-existing
`[ ]`, closed with **M17**.

**One behaviour change to plan for.** M15 makes `DELETE` **refuse** on held or
retention-floored subjects (`409 failed_precondition`). Every other task in M13–M16 is
purely additive — new tables, new endpoints, no existing behaviour altered. See §00.11.13.

---

## M0 — Architecture & Design Docs ✅

Full spec set `00`–`10`, mermaid-only diagrams, decisions recorded in `00 §11`.
**Done:** commits through `9eed5f0`. `11-model-insights.md` was specced later, with **M11**
(it replaced the managed-service doc, moved out of this OSS repo); `12-version-portrait.md`
later still, with **M12**.

## M1 — Go Scaffold ✅

Compiling, runnable, tested skeleton; ports & adapters; end-to-end happy path verified.
**Done:** `0f8bb34`.

- [x] Module, package tree, Makefile, README, `.gitignore`
- [x] Domain: entities, enums, stage machine, ULID, coded errors, port interfaces
- [x] Core: create/publish/transition/resolve, audit-in-flow
- [x] Adapters: in-memory store, fs storage, memory cache, event bus
- [x] Two surfaces (:8081 Model API, :8080 Admin UI) + ops (:9090)
- [x] Unit tests (stage machine, name validation); `go vet`/`gofmt` clean

## M2 — Persistence ✅

**Goal:** replace the in-memory store with real per-dialect adapters (§02.7).
**Acceptance:** the full happy path passes against **both** SQLite and Postgres; schema
via migrations; singleton invariant enforced (Postgres `FOR UPDATE`, SQLite serialized).
**Done:** shared `sqlstore` (`database/sql`) + `Dialect`; conformance suite green on
memory + SQLite; persistence verified across a restart.

- [x] Shared `sqlstore` + `Dialect` (placeholders, unique-violation, model lock)
- [x] SQLite `MetadataStore` (cgo-free modernc driver, WAL, single-writer) + migrations
- [x] Postgres `MetadataStore` (pgx) + migrations; `FOR UPDATE` singleton lock
- [x] Shared conformance suite (`storetest`) run vs memory + SQLite; Postgres via `LINEAGE_TEST_PG`
- [x] Versioned forward-only migrator (`schema_version`)
- [x] Config wiring (`LINEAGE_DB_ENGINE`) picks the adapter at startup; default SQLite
- [x] FK `ON DELETE CASCADE` in schema (§02.5)
- [x] Cursor pagination (real `nextPageToken`) — delivered in **M5** (`domain.Page`)
- [x] Postgres JSONB `label`/`custom_properties` push-down — delivered post-M9 (per-dialect
      `JSONContainsClause`); the Postgres adapter is now validated on real embedded Postgres

## M3 — Storage ✅

**Goal:** production artifact delivery (§05).
**Acceptance:** register-by-reference fills digest/size via `Stat`; signed upload
(initiate→PUT→finalize) verifies digest; signed download works on S3.
**Done:** hand-rolled SigV4 (validated vs AWS's published vector **and** against a live
MinIO server) so any S3-compatible endpoint works; full upload flow (all three modes) driven
end-to-end over HTTP — including the real binary uploading to MinIO via a presigned PUT and a
consumer downloading via the resolve `signedUrl`; integrity + immutability enforced.

- [x] S3 driver: `SignGet`/`SignPut`/`Stat`/`Get`/`Put`/`Delete`, path- & virtual-host style
      (AWS · MinIO · R2 · Ceph via endpoint override); SigV4 hand-rolled, stdlib-only
- [x] **Credential chain**: static → env → **IRSA / EKS Pod Identity** (STS
      `AssumeRoleWithWebIdentity`) → ECS task role → EC2 IMDSv2; temporary creds cached +
      auto-refreshed before expiry (§05.4.1)
- [x] Upload initiate/finalize flow (§05.6): signed direct PUT (s3), **multipart** for large
      files (presigned part URLs → complete), **and** stream-through (`fs`/non-signing);
      `Put`/`URIFor`/multipart/`ListObjects` added to the `StorageBackend` port
- [x] Integrity: sha256 verification on finalize (stream-through hashes inline; signed path
      Stats `x-amz-meta-sha256`, or verifies-by-stream under a size cap; large/multipart trust
      declared digest + `Stat` size); immutability via write-once `UNIQUE(version_id,name)`
- [x] **GC** (§05.8): default `retain` (`DELETE` drops the row, not bytes); optional `sweep`
      reference-counts objects by uri (`ArtifactRefsURI`) and deletes unreferenced ones past a
      grace period, path-scoped; background sweeper wired via `LINEAGE_STORAGE_GC=sweep`
- [x] Tests: SigV4 vector, `fs` round-trip, IRSA web-identity + cache/refresh, core upload
      (stream-through/multipart/mismatch/immutability), GC sweep; **live MinIO** integration
      (round-trip + multipart, gated by `LINEAGE_TEST_S3_*`)
- [x] OCI/ORAS driver — was a locked v1-out decision (§00.11.4); delivered in **M17**

## M4 — Delivery Hardening ✅

**Goal:** the resolve/fetch path is fast, correct, and integration-ready (§04).
**Acceptance:** cache invalidates on events; `ETag`/`304` on resolve; stream-through
fetch for fs; `lineage://` initializer resolves in a KServe pod.
**Done:** verified end-to-end against the running binary — conditional resolve returns 304,
`/content` streams fs bytes, and `lineage-init` pulls `lineage://fraud-detector/production`
into a model dir with matching bytes.

- [x] Resolve cache subscribed to `version.created`/`stage_changed`/`artifact.created` via the
      bus in `core.New`; direct invalidation calls removed (purely event-driven, §04.4)
- [x] `ETag` (= digest) + `If-None-Match` → `304` + `Cache-Control: private,no-cache` on resolve
- [x] `/content` fetch: `302` to a fresh signed URL, or stream-through with `Range` +
      conditional (`http.ServeContent`) where the backend can't sign (`FetchArtifact`)
- [x] `lineage://` grammar parser (`domain.ParseLineageURI`) + `cmd/lineage-init` KServe
      storage-initializer; `ClusterStorageContainer` manifest in §04.5
- [x] Tests: parser cases, resolve ETag/304, content stream/Range/304, event-driven
      invalidation (promotion reflected immediately); live initializer round-trip

## M5 — Model API Completeness ✅

**Goal:** the full `/v1` contract (§03).
**Acceptance:** OpenAPI served at `/v1/openapi.json` matches handlers; all resources CRUD.
**Done:** every resource in the §03 map is wired and verified end-to-end (HTTP tests + a live
SQLite binary smoke run): CRUD, guards, immutability, idempotency, pagination, audit, OpenAPI.

- [x] Model PATCH / `:archive` (reversible) / guarded `DELETE`; Version PATCH / guarded `DELETE`
- [x] Artifact GET / list / PATCH (immutable uri·digest·sizeBytes → `409`) / DELETE
- [x] Lineage endpoints (add/list/delete, relation + target validation)
- [x] Deployment endpoints (create/list/get/patch/delete)
- [x] Audit feed: `GET /v1/models/{m}/audit` + `GET /v1/audit?subjectType=&subjectId=`
- [x] **Idempotency-Key** replay for POST creates (in-process store, replays cached 2xx)
- [x] **Cursor pagination** (real `nextPageToken`, `(createdAt,id)` cursor) — carried from M2,
      shared `domain.Page`, applied to models/versions/audit
- [x] Delete guards: production version / model with production → `409` unless `?force=true`
- [x] Hand-authored **OpenAPI 3.1** spec embedded + served at `/v1/openapi.json`; test asserts
      it covers the resources and every `$ref` resolves
- [x] Postgres `custom_properties` filtering (`cp.<k>`, JSONB `@>`) — done post-M9; a richer
      `filter` expression grammar is still deferred (§03.3)

## M6 — Admin UI ✅

**Goal:** the human console (§06). BFF endpoints + SPA served on :8080.
**Done:** a Vite/React/TypeScript + Tailwind v4 SPA with shadcn-style components, embedded via
`go:embed` and served by the `:8080` BFF; verified end-to-end (Go tests + a live run).
**Design system:** monochrome (grayscale only, no accent color), 1px hairline borders, sharp
(zero-radius) corners, mono type for ids/digests; auto light/dark.

- [x] BFF (`adminui/bff.go`): `/api/overview`, `/api/models` (rollup: version count +
      production pointer), `/api/models/{m}`, `/api/models/{m}/versions/{v}`, `/api/activity`;
      calls the same core in-process, empty collections coalesced to `[]`
- [x] Web console SPA (`adminui/web/`): Overview (counts, stage bars, recent activity), Models
      (searchable table), Model detail (version timeline), Version detail, Activity feed
- [x] Lineage edges + audit **timeline** views on the version detail page
- [x] Search box (header → `/models?q=`); client-side routing with SPA index fallback
- [x] `go:embed all:web/dist` + `make web` (pnpm build); dist committed so `go build` needs no Node
- [x] Tests: SPA served at `/` + fallback for client routes; BFF aggregates/rollup/detail

## M7 — Lineage & Provenance ✅

**Goal:** the differentiator (§07). Typed edges + graph traversal.
**Done:** bounded, cycle-safe traversal (ancestry upstream, impact downstream) exposed on the
Model API and rendered in the console; verified end-to-end + unit-tested.

- [x] Edge create/list/delete; relation + target validation — delivered in **M5**
- [x] Ancestry (upstream) + impact-analysis (downstream) traversal: bounded `depth`, cycle-safe
      (each version expanded once), relation filter, reaches external dataset refs;
      `core.TraverseLineage` walks the store's indexed edge lookup (dialect-agnostic; a
      per-dialect `WITH RECURSIVE` can replace the neighbor-walk behind the port for scale)
- [x] `GET /v1/models/{m}/versions/{v}/lineage?direction=&depth=&relations=` → `{nodes, edges}`
      (flat edge list without `direction`); `GetVersionByID` added for node labeling; OpenAPI updated
- [x] Console: version detail renders **Provenance (upstream)** + **Impact (downstream)** graphs
      (BFF `/api/…/graph`), version nodes linked
- [x] Tests: ancestry, depth bound, impact, relation filter, cycle safety (core); graph over HTTP
- [x] SDK auto-capture (`produced_by`, `derived_from`) — delivered in **M10**
      (`sdk/python/lineage/client.py`: run + `git://<sha>` provenance, `derived_from` parents)

## M8 — Observability ✅

**Goal:** production ops (§09).
**Done:** a hand-rolled, dependency-free Prometheus registry + `/metrics`, real readiness, and
structured JSON access logs with correlation IDs — all verified live. Tracing and the SLO rules
landed after M9: spans export over OTLP from the running binary (API → core → store, verified
against a stub collector), and the six alert rules render and pass `promtool check rules`.

- [x] **Prometheus registry** (`observability/metrics`, hand-rolled counters/gauges/histograms
      with labels + text exposition — no `client_golang` dep): RED (`surface`/**templated**
      `route`/`method`/`status` + duration histogram), resolve cache hit/miss, versions
      published, transitions by `to`-stage, singleton demotions, signed URLs, finalize latency,
      digest-mismatch; domain gauges (totals + stage distribution) and DB pool stats refreshed
      at scrape via `OnScrape`
- [x] Core stays metrics-free: a `domain.Meter` port (no-op default) is injected with
      `core.WithMeter` (non-breaking functional option); hooks in resolve/publish/transition/finalize
- [x] **Structured JSON request logs** (`slog`): surface, route, method, status, latencyMs,
      `requestId`, `actor`, `traceId`; `X-Request-Id` echoed; **W3C `traceparent`** trace-id
      propagated for correlation — never logs secrets/signed URLs/bytes
- [x] Real **`/readyz`**: composed store + default-storage `Stat` checks gate traffic; `/healthz` liveness
- [x] Tests: registry (counter/gauge/histogram cumulative buckets, scrape hooks, label escaping),
      ops handler (health/ready/metrics + failure gating), meter hooks fire from the core
- [x] **OTLP span export** (OTel SDK): a `domain.Tracer` port with a no-op default, injected via
      `core.WithTracer` — the core never imports the SDK, mirroring `Meter` (§01). The
      `observability/tracing` adapter owns the provider, OTLP/HTTP exporter, and W3C propagation;
      store spans come from a decorator over the `MetadataStore` **port**, so SQLite/Postgres/
      memory are covered once and the store adapters stay telemetry-free
- [x] Server spans carry the **templated** route (renamed after the mux matches), so span names
      stay bounded like the metric labels; 5xx marks the span errored, 4xx does not; `requestId`
      falls back to the live span's trace id so logs and traces join
- [x] Tracing is **off unless `otlpEndpoint` is set** — disabled means the decorator is not in
      the call path at all; the chart renders the env, sampling is parent-based
- [x] SLO alert rules (§09.5) as a `PrometheusRule` chart asset over the existing RED metrics:
      resolve availability + p99, publish success, digest mismatch, singleton violation, down
- [x] Tests: real OTLP export to a stub collector, upstream `traceparent` adoption, disabled-path
      identity, store-decorator spans + error marking, middleware route templating and 4xx/5xx;
      live binary → collector round-trip; `helm lint`/`template` both profiles + `promtool`

## M9 — Deployment (Helm) ✅

**Goal:** the Helm axiom realized (§08).
**Acceptance:** `helm install` on a fresh cluster → working registry (dev profile).
**Done:** chart in `deploy/helm/lineage`; `helm lint` + `helm template` clean on both profiles;
guards fail-closed; multi-stage `Dockerfile` (cgo-free static → distroless non-root).

- [x] Chart: Deployment (two surfaces + ops port), three Services, two Ingresses; env rendered
      from values (secret-bearing DSN/keys via `secretKeyRef`, never plaintext, §08.6)
- [x] **Migrate Job** = Helm `pre-install`/`pre-upgrade` hook running `lineage migrate` (new
      subcommand) for Postgres; SQLite migrates at startup; `values-dev.yaml` / `values-prod.yaml`
- [x] **SQLite ⇒ single replica** enforced (template `fail` on `replicaCount>1` or autoscaling);
      PVC for sqlite/fs; `Recreate` strategy for the single-writer; postgres/redis are external
      (subcharts reserved as a documented opt-in — external managed DB is the prod path)
- [x] Hardened: non-root, `readOnlyRootFilesystem`, dropped caps, seccomp; **NetworkPolicy**
      (restricts Admin UI, opens Model API + ops), **PDB** + **HPA** (prod), **ServiceMonitor**
- [x] `Dockerfile` (node build console → cgo-free static Go binary → distroless nonroot) + `make docker`
- [x] Validated: `helm lint`/`template` both profiles, guard failures, `lineage migrate` smoke

## M10 — SDK & CLI ✅

**Goal:** client ergonomics (§10). Generated from OpenAPI (needs M5).

- [x] Python SDK: `publish`, `transition`, `resolve`, `download`, lineage helpers; all three
      upload modes (signed direct, multipart, stream-through) with streamed bodies; SHA-256
      verification on upload and download; optional git/run lineage capture
- [x] Go CLI `lineage`: model list, version publish/promote, resolve, pull, lineage traversal;
      multipart upload and multi-artifact pull
- [x] Generation + versioning pipeline: `sdk/generate.py` derives an operation → path manifest
      from the served OpenAPI source and the SDK routes every request through it;
      `make sdk-check` regenerates and compiles it
- [x] Validated end-to-end on both storage tiers: `fs` (stream-through) and S3/MinIO
      (signed direct + 70 MiB multipart), plus `go test ./cmd/lineage-cli`

## M11 — Model Insights ✅

**Goal:** the fact API for model composition (§11) — param counts, layer breakdown,
framework, precision/quantization, disk vs memory footprint, evaluations, and an
architecture fingerprint that classifies version-to-version change. The registry stores and
queries these facts; producers outside it derive them (§11.1).
**Acceptance:** two versions whose facts were submitted over the API yield
`GET /v1/models/{m}/diff?from=A&to=B` with the correct §11.4.1 verdict (`reweighted` vs
`recast` vs `rearchitected`) plus metric deltas, while the binary opens no artifact on any
insight path — no framework code, no header parsing, no artifact reads.
**Depends on:** M5 (Model API contract) · M2 (per-dialect store) · M6 (console panels).
M10 is not a dependency; the SDK is one producer among several, added later.
**Done:** four tables behind the `MetadataStore` port, green on memory + SQLite + real
Postgres; the four-hash verdict table with per-tensor partial diffs; PATCH-merge writes with
per-field attribution; the published producer contract; console panels. Verified live on the
SQLite binary — two producers merging without clobber, a bf16→int8 pair diffing to `recast`
(−9.6 GB on disk, −10.0 GB resident, −1.7 points of MMLU), the `409` immutability guard, and
persistence across a restart.

### 11a — Schema & core

- [x] Tables `version_insight`, `layer_block`, `footprint`, `evaluation` (§11.3) — per-dialect
      migrations + `MetadataStore` port methods; `ON DELETE CASCADE` from `model_version`
- [x] `lineage_edge.properties json?` (§11.3.6) so `derived_from` can carry `{method:"quantize"}`
- [x] Domain entities + enums (`source`, `param_count_method`, `dtype_dominant`); `coverage`
      records extractability rather than substituting a value — unknown is `null` (§11.2)
- [x] Per-field provenance: `field_sources` + `reporter_*`, so independent producers coexist
      without clobbering each other (§11.2, §11.6.1)
- [x] Store conformance suite extended (`storetest`) → green on memory + SQLite + Postgres

### 11b — Fingerprint & diff

All of this computes over stored facts only — the same posture as lineage traversal (§07).

- [x] Canonical arch-doc normalization spec (sorted tensor names, normalized op names,
      collapsed repeats), published so independent producers hash identically; the registry
      validates the shape, producers compute the hashes
- [x] Verdict classifier over the stored four-hash ladder, from the §11.4.1 lookup table
      (not a heuristic) + per-tensor merkle partial diff → changed-name patterns (detects a
      LoRA merge, §11.4.2)
- [x] Partial facts: missing `weights_hash` ⇒ a narrowed verdict naming the missing inputs;
      no facts ⇒ `verdict:"unknown"` rather than an inference (§11.4.3, §11.6.2)
- [x] Metric delta join over (`suite`,`metric`,`split`,`harness_version`) with
      `higher_is_better` applied; empty intersection ⇒ `comparable:false`, never a bogus delta
- [x] Tests: each verdict row, partial/unknown verdicts, ordering stability of the
      canonical form, non-comparable metric pairs

### 11c — API

- [x] `PATCH …/insight` merges per field (absent = unchanged, explicit `null` = clear),
      recording `field_sources` per merged field — the default write (§11.6.1)
- [x] `PUT …/insight` full replace (single-owner case); `POST`/`GET …/evaluations` (append-only);
      `PUT`/`GET …/footprints[/{scenario}]` (upsert by scenario); `GET …/insight?include=layers,sources`
- [x] `GET /v1/models/{m}/diff?from=&to=` + cross-model `GET /v1/diff?from=a@1&to=b@2`
- [x] Versioned JSON Schema: payload declares `schemaVersion`; unknown version ⇒ `400`;
      unknown fields rejected rather than dropped; no partial writes
- [x] Governance (§11.7): audit-in-transaction; **`weights_hash` conflict ⇒ `409`** unless
      `?force=true`; evaluations append-only
- [x] `resolve ?include=insight` compact block (`paramCount`,`dtype`,`diskBytes`,
      `minDeviceMemoryBytes`) — gated so the hot cached path stays small (§04, axiom 8)
- [x] OpenAPI updated; scalar filters on both engines, `arch_doc` JSONB predicates
      Postgres-only (§02.7)
- [x] Tests: multi-producer merge (three writers, disjoint fields, no clobber), schema-version
      rejection, `409` on fingerprint contradiction, idempotent replay

### 11d — Producer contract (published, not implemented here)

The registry ships no extractor (§11.5, §11.9); it owes producers a contract stable enough
to build against.

- [x] JSON Schema published + served alongside the OpenAPI document, versioned independently
- [x] Golden fixture set (one per format family) + a conformance test any producer can run
- [x] Canonical arch-doc normalization documented precisely enough that two producers agree
      on the same hash for the same model
- [x] Worked producer examples in the docs (curl + SDK), incl. the `declared`-only path for a
      format that reveals nothing

### 11e — Console (§11.8)

- [x] Version detail Insights panel: params, framework, precision, disk vs memory, layer
      breakdown (repeats collapsed); every value labelled with its `source` and reporter
- [x] Compare view: verdict, hash ladder, changed-tensor summary, metric deltas
- [x] Estimated footprints rendered with their basis, not as a bare byte count
- [x] Absent facts render as "not reported", distinct from a zero or an empty value

**Non-goals** (§11.9): the registry derives no fact from an artifact (weights, headers, or
config); no scanner ships in this repo; no eval orchestration; no verification of accuracy
claims; no determination of why weights changed.

## M12 — Version Portrait ✅

**Goal:** two procedurally generated marks per version in the console (§12) — a square
**Fingerprint** (identity, from `insight.hashes`) and a wide **Portrait** (structure, from
`insight.layers`). Independent sections, deterministic, browser-side at paint time.
**Done:** `b22a6e9`…`5916f69`. Specced after M11 and built in the same pass, so it was never
given a milestone row until now.

- [x] `VersionPortrait` / `VersionFingerprint` / `VersionMark` components, drawn only from
      facts a producer already reported — no new field, table, or API (§12.1)
- [x] The honesty rule (§12.2): absent input renders an empty section; neither mark ever
      substitutes for the other; nothing reported ⇒ no mark
- [x] Interactive portrait with per-level ring colour and dimension details; comparison
      fingerprints on the Compare view

---

# Next — Compliance & Evidence (M13–M16)

Specced in `15`–`19` (`ddf4654`). `15` is posture only and ships no code; the four
milestones below are the four capabilities it governs.

**The stance, which constrains every task here:** Lineage is an *evidence substrate*, not a
compliance product. It emits stored facts in a regulation's structure and **names every
heading it cannot fill**. It never decides a risk class, never asserts a modification is
substantial in law, and never implies its audit log satisfies the Act's runtime logging
articles (§15.3). A task that would blur one of those lines is out of scope, not behind.

**Sequencing** follows `15.7`. Phase numbers are annotated per task, since the phases
interleave across docs while the milestones stay doc-aligned.

## M13 — EU Risk Classification & Drift ⬜

**Goal:** record how risky a model is — a **declared** operator claim, never an inference —
and detect when that claim has gone out of date (`16`).
**Acceptance:** classify a model `high_annex_iii`, publish a new version, then
`GET /v1/models?classificationState=stale` returns it with
`staleReasons:["version_published_since"]` — **with no background job having run**, proving
drift is computed at read time rather than stored.
**Depends on:** M2 (per-dialect store) · M5 (API contract) · M6 (console).
**Phase:** 1, plus its half of phase 3.

- [ ] `classification` table (`16.7.1`) — per-dialect migrations, `MetadataStore` port
      methods, `ON DELETE CASCADE` from `model`
- [ ] Fields `eu_system_risk_class` / `eu_gpai_tier` carry the **`eu_` prefix** (`16.3.1`);
      `intended_purpose`, `basis`, `classified_at`, `review_due_at` stay unprefixed —
      jurisdictional vs shared is part of the contract, not a naming preference
- [ ] `source` is always `declared`; **no `derived` path exists in the schema** so a future
      producer cannot write one (`16.7.2`)
- [ ] `PUT`/`GET /v1/models/{m}/classification` — full replace, not `PATCH` (`16.8`); audit
      action `classification.set` in the same transaction
- [ ] Validation (`16.6`): enum membership; class ⇒ `intendedPurpose`; high-risk ⇒ `basis`;
      `reviewDueAt > now`; `classifiedAt`/`classifiedBy` server-set and request values ignored
- [ ] **Drift predicate** (`16.5`) as a read-time computation — four disjuncts, each
      returning its own reason; `staleReasons[]` returns *every* one that fired
- [ ] `classificationState` is three-valued (`unclassified`/`stale`/`current`), not a boolean
      — `unclassified` is not a kind of stale (`16.4`)
- [ ] Inventory filter on `GET /v1/models?euSystemRiskClass=&euGpaiTier=&classificationState=`
- [ ] Console (`16.9`): inventory view with stale models marked **and their reason**; version
      detail Compliance panel; `unclassified` never renders as `minimal`
- [ ] OpenAPI updated; scalar filters work on both engines
- [ ] Tests: each drift clause fires independently; `unclassified ≠ stale`; every validation
      rule; the deliberate false positive in clause 3 (`updated_at` on the production version)
      is asserted as intended behaviour, not fixed

## M14 — Evidence Export ⬜

**Goal:** one call produces the dossier a regulator asks for, from facts `02`–`11` already
hold, with every unfilled heading emitted as explicit data (`18`).
**Acceptance:** generating an `annex_iv` bundle twice **across a clock change** yields
byte-identical output; `gapSummary` reports `{held:2, partial:5, notHeld:3}`; all three
not-held sections are present with their fixed notes rather than absent.
**Depends on:** M13 (the class every bundle carries) · M11 (insights, evaluations) ·
M15 (the retention floor it echoes) · M16 (review state a filing may cite).
**Phase:** 2 (annex-xii), the other half of 3, then 6 (annex-iv).

- [ ] `evidence_bundle` table (`18.8.1`) — records that a bundle was produced and its digest;
      **the body is not stored**, since it is a pure function of facts plus profile and a
      stored copy could disagree with the source
- [ ] `gap_counts` frozen at generation — the one exception, so "we filed with three gaps in
      March" stays answerable after those gaps are filled
- [ ] Profile registry with `annex_xii` first (`18.3`) — the obligation in force since Aug 2025
- [ ] Canonical serialization per `11.4.5`, with `bundle.digest` and `bundle.generatedAt`
      **excluded from the hashed form** (a digest cannot cover itself; a timestamp defeats
      reproducibility)
- [ ] **Determinism test as an acceptance gate**, not an afterthought (`18.7`)
- [ ] `POST …:evidence` + `GET …/evidence` history; audit action `evidence.generate` — a
      read-shaped operation audited anyway, because who exported what and when is the question
- [ ] `?include=docs` → zip with `bundle.json` at root and referenced `DOC`/`METRICS`
      artifacts under `artifacts/`, fetched through `05` signed URLs
- [ ] Gap notes as **constants in the binary** (`18.6`), so the same gap reads identically in
      every install and can be matched by a consumer
- [ ] `annex_iv` profile (`18.4`) — 2 held / 5 partial / 3 not held, and that is the correct
      result, not a shortfall
- [ ] Errors: bundle for an unclassified model ⇒ `409 reason:"unclassified"` rather than a
      bundle with a null class
- [ ] Console (`18.10`): bundle history with digests and frozen gap counts; **`gapSummary`
      renders as a checklist, never a failure state** — a red badge would train users to
      distrust a truthful bundle
- [ ] Tests: byte-identical regeneration; gap counts per profile; every not-held section
      present; zip contents resolve

## M15 — Retention, Legal Hold & Audit Sealing ⬜

**Goal:** evidence survives, and the audit log can prove it was not rewritten (`19`).
**Acceptance:** `DELETE` on a held model returns `409` with `reason:"legal_hold"`; editing a
row inside a sealed epoch makes `:verify` report `root_mismatch` at that epoch; deleting one
makes it report `leaf_count_mismatch`; `:proof` for a sealed row verifies against its root.
**Depends on:** M2 · M5. Independent of M13/M14 — schedule by value, not by blockers.
**Phase:** 4 (hold + floor), 7 (sealing).

### 15a — Legal hold & retention floor (phase 4)

- [ ] `legal_hold` on `model` / `model_version`; `hold.set` / `hold.release` as **separate
      audited actions** — clearing a hold is the event an auditor cares about
- [ ] **⚠ The non-additive change:** `DELETE` refuses on a held subject, or one younger than
      the configured floor, with `409 failed_precondition` + `details.reason`. Existing
      teardown automation may need a retry branch (§00.11.13)
- [ ] Transitive: a hold on a model covers its versions, refused with the model in `heldBy`
- [ ] Hold blocks **destruction only** — `PATCH`, transitions, and archival still work
- [ ] Retention config (`19.4`) echoed at `/healthz` **and in every bundle**, so a filing can
      cite the floor the registry actually ran under; `0` disables and is a real choice
- [ ] `POST …:hold` / `…:release`; `reason` recorded on the audit event, not the row

### 15b — Merkle epoch sealing (phase 7)

- [ ] `audit_event.epoch = floor(at / sealIntervalMs)` — derived from the row's own clock,
      **reading no other row**. This is what keeps the write path free of coordination
- [ ] `audit_epoch` table (`19.6.1`) — one Merkle root per closed window, epochs chained by
      `prev_root`; append-only and never updated
- [ ] Background sealer with `sealGraceSeconds`, so a transaction begun inside a window
      commits before its epoch closes
- [ ] RFC 6962 construction (`19.5.1`): domain-separated leaf/node hashing, leaves ordered by
      ULID `id`, odd node promoted
- [ ] Defaults **on** (`19.5.2`) — the write cost that justified opt-in belonged to the
      per-row chain design, not to the goal; axiom 7 says auditable by default
- [ ] `GET /v1/audit:verify` reporting `root_mismatch` / `leaf_count_mismatch` /
      `prev_root_mismatch`, plus `openEpochSince`
- [ ] `GET /v1/audit/{id}:proof` — `O(log n)` inclusion path, so a third party verifies one
      event without reading the log
- [ ] The open epoch is **reported, not glossed** (`19.5.3`): `:proof` inside it returns
      `409 reason:"epoch_unsealed"` with `sealsAt`
- [ ] Tests: tamper detection for edit / delete / whole-epoch removal; proof verification;
      enabling mid-life starts at the current epoch and does **not** backfill

## M16 — EU Modification Review ⬜

**Goal:** warn when editing someone else's model may have transferred provider liability
under Art. 25 (`17`).
**Acceptance:** publish a fine-tune with a `derived_from` edge on a high-risk model → it
appears in `GET /v1/reviews?status=open` with verdict `reweighted` and the declared method;
recording a review closes it; a client-supplied `verdictAtReview` is rejected.
**Depends on:** M13 (the class that gates the queue) · M11 (`11.4` verdicts).
**Phase:** 5.

- [ ] `modification_review` table (`17.5.1`) — append-only like `evaluation`; a re-review is a
      new row and the queue keys on the latest per (`version_id`,`edge_id`)
- [ ] `verdict_at_review` is **server-set and frozen**, so a producer submitting a
      `weights_hash` later cannot rewrite what a reviewer actually saw
- [ ] Queue query (`17.4`), all four conditions — including that **`unknown` is eligible**:
      "we cannot tell what changed" is precisely the case wanting human eyes, and excluding it
      would make a missing hash look like a clean bill of health
- [ ] `POST …/reviews` rejects a client-supplied verdict with `400`; `GET /v1/reviews?status=`
- [ ] Response carries `basis` (which hashes were present per side), so a partial verdict is
      identifiable as partial rather than read as confident
- [ ] `undetermined` is a real outcome — "looked at it, needs counsel" must be distinguishable
      from "nobody opened it"
- [ ] Console (`17.7`): queue with verdict, declared method, and **both fingerprints side by
      side** (`12.6.2` already renders at 64px). The queue never blocks an action
- [ ] Wire drift clause 4 back to M13 — an open review item makes a classification stale
- [ ] Tests: each queue condition; `unknown` queued; latest-row-per-pair; frozen verdict
      survives a later hash write

**Non-goals across M13–M16** (`15.6`): determining a risk class; asserting substantial
modification; conformity assessment, CE marking, or EU database submission; Art. 12/19 runtime
inference logging; risk management, human oversight, or cybersecurity (named as bundle gaps,
not built); advising on retention periods.

## M17 — OCI/ORAS Storage Driver ✅

**Goal:** close the one deferred v1 decision (§00.11.4) — an OCI registry as a first-class
`StorageBackend` (`05.3.1`).
**Acceptance:** publish a version to a registry and every artifact lands in **one** manifest;
`Stat` returns the artifact's own content digest; resolve carries an `ociImage` a KServe
`InferenceService` can use verbatim; a plain OCI client reads the manifest we wrote.
**Done:** verified against a live `registry:2` — driver round-trip, a spec-shaped manifest
fetched with nothing but the standard `Accept` header, and the real binary publishing,
resolving and serving `/content` end-to-end with `LINEAGE_STORAGE_DRIVER=oci`.

- [x] `oci://<registry>/<repo>[:tag][@sha256:…][#<layer>]` grammar (`domain.ParseOCIURI`),
      distribution-spec name/tag validation; the `#fragment` mirrors `lineage://…#artifact`
- [x] Distribution v1.1 client, **stdlib-only** like SigV4: manifest get/put/delete, blob
      head/get/push (streamed, hashed in flight), Docker registry v2 **bearer-token flow**
      with per-scope caching (a `pull,push` token satisfies later pulls)
- [x] **One manifest per version, one layer per artifact**, titled with
      `org.opencontainers.image.title`; layers hold bytes **verbatim**, so a layer's digest
      *is* the artifact's content digest and `05.5` integrity carries over unchanged
- [x] `Put` folds a new layer into the version's manifest (creating it on first write,
      replacing a same-named layer in place); serialized per `(repo, tag)`
- [x] `SignGet` returns the registry's blob redirect — a presigned URL on object-store-backed
      registries; `ErrStorageUnsupported` where blobs are served inline, so delivery falls
      back to stream-through
- [x] **Port change:** `SignPut` split out of `Signing` — a registry offloads reads but has no
      presignable write target, so uploads stream through while resolve still offloads
- [x] `ociImage` on the resolution (`04.2`), omitted when artifacts span >1 image
- [x] GC opt-out: `ListObjects` → `ErrStorageUnsupported`, swept as a no-op rather than an
      error; registry lifecycle policies own blob retention (`05.8`)
- [x] Rejected uploads discard their bytes — tidy on a blob backend, **load-bearing** here,
      since a rejected layer would otherwise sit inside a pullable image with no GC to reap it
- [x] Config (`LINEAGE_OCI_*`) + Helm `storage.oci` with a credentials Secret
- [x] Tests: URI grammar; an in-process fake registry driving round-trip, manifest
      accumulation, in-place replacement, **concurrent-put layer safety**, redirect vs inline
      signing, layer/manifest deletion, and token reuse; core-level `ociImage`,
      stream-through selection and GC skip; live-registry integration gated by
      `LINEAGE_TEST_OCI_REGISTRY`

**Known limits, accepted rather than engineered around** (`05.3.1`):

- Lineage pushes an OCI **artifact**, not a runnable image. KServe **modelcars** mounts a real
  image with tar layers — build that in CI and **register it by reference**; Lineage `Stat`s
  it and resolution hands back the same ref. Repacking artifacts into tar layers would break
  the digest identity that makes `Stat` free and integrity verifiable.
- The manifest read-modify-write lock is **process-local**. Two replicas finalizing different
  artifacts of the same version concurrently can lose a layer. One CI job publishing one
  version — the normal case — is unaffected; the distribution spec has no portable
  conditional manifest PUT that would fix it properly.
