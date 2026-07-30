# Lineage

A self-hostable, feature-complete **AI model registry** — the system of record for ML
models from post-experimentation to production. Benchmarked against Kubeflow Model
Registry for **capability parity, not wire compatibility**.

> Design docs live in [`docs/`](docs/) (`00`–`11`). Start with
> [`docs/00-preplanning.md`](docs/00-preplanning.md) and
> [`docs/01-architecture-overview.md`](docs/01-architecture-overview.md).

## Status

**Early, working.** The Go skeleton compiles, runs, and serves the core happy path
(create model → publish/upload artifacts → stage transitions → resolve). Metadata persists
in **SQLite or Postgres** (per-dialect adapters behind the `MetadataStore` port, §02.7); an
in-memory store remains for tests/dev. Artifacts go to a real **`fs`** (dev/air-gapped,
stream-through) or **`s3`** backend — any S3-compatible store (AWS · MinIO · R2 · Ceph) via
signed GET/PUT + multipart, with a full upload flow (initiate → PUT → finalize, digest-verified),
reference-counted GC, and an IRSA/ECS/IMDS credential chain (§05.4.1). The S3 driver + SigV4
are verified against a live MinIO server. Redis cache is a stub.

Resolution is one call (`storageUri` + fresh `signedUrl` + digest), HTTP-cacheable
(`ETag`/`304`) with event-driven cache invalidation; a `/content` broker endpoint redirects
to a signed URL or streams through (Range-capable) for `fs`; and `lineage://model/stage`
resolves at pull time via the `lineage-init` KServe storage-initializer.

The full `/v1` contract is implemented (§03): model/version/artifact/lineage/deployment CRUD
with PATCH, `:archive`, guarded `DELETE`, artifact-content immutability, `Idempotency-Key`
replay, cursor pagination, an audit feed, and a hand-authored **OpenAPI 3.1** spec served at
`/v1/openapi.json`.

The `:8080` **Admin console** is a Vite/React SPA (Tailwind v4, shadcn-style components;
monochrome, hairline borders, sharp corners) embedded via `go:embed` and served by an
in-process BFF — overview, models, model/version detail, lineage + audit timeline, activity.

The ops port (`:9090`) serves `/healthz`, real `/readyz` (store + storage reachability), and
`/metrics` — a hand-rolled, dependency-free Prometheus registry: RED (templated route labels),
resolve cache hit/miss, lifecycle counters, and domain/DB gauges. Requests emit structured
JSON logs with a `requestId` (+ W3C `traceparent` correlation).

Set the metadata engine with `LINEAGE_DB_ENGINE=sqlite|postgres|memory` and the storage
driver with `LINEAGE_STORAGE_DRIVER=fs|s3`. Progress is tracked in
[`MILESTONES.md`](MILESTONES.md) (M0–M8 done).

## Run

```bash
make run            # or: go run ./cmd/lineage
```

Three listeners come up (§01):

| Port | Surface |
|---|---|
| `:8081` | **Model API** (`/v1`) — publish, resolve, fetch (machines) |
| `:8080` | **Admin UI** — human web console BFF |
| `:9090` | ops — `/healthz`, `/readyz`, `/metrics` |

Quick tour:

```bash
curl -XPOST localhost:8081/v1/models -H X-Lineage-Actor:me -d '{"name":"fraud-detector"}'
curl -XPOST localhost:8081/v1/models/fraud-detector/versions -H X-Lineage-Actor:me \
  -d '{"name":"1.4.0","artifacts":[{"name":"model.onnx","uri":"s3://m/1.4.0","modelFormat":{"name":"onnx"}}]}'
curl -XPOST localhost:8081/v1/models/fraud-detector/versions/1.4.0:transition -d '{"to":"staging"}'
curl -XPOST localhost:8081/v1/models/fraud-detector/versions/1.4.0:transition -d '{"to":"production"}'
curl "localhost:8081/v1/models/fraud-detector/resolve?stage=production"
```

## Architecture (ports & adapters)

One binary, two surfaces on two ports, over a shared domain core (`docs/01`). The core
depends only on **port interfaces**; adapters are wired at startup and never imported by
the core.

```
cmd/lineage/            main: config → wire adapters into ports → serve
cmd/lineage-init/       KServe storage-initializer for lineage:// (§04.5)
internal/
  domain/               entities, enums, stage machine, errors, PORT INTERFACES
  core/                 business logic over the ports (publish, transition, resolve)
  api/
    modelapi/           /v1 machine API (§03/§04)
    adminui/            :8080 BFF + embedded web console (web/, go:embed, §06)
  adapters/
    store/{memory,sqlite,postgres}   MetadataStore (all real; shared sqlstore + Dialect, §02.7)
    storage/{fs,s3}                  StorageBackend (both real; s3 = hand-rolled SigV4, §05)
    cache/memory                     ResolutionCache (§04.4)
    events                           EventBus
  observability/        health + metrics (§09)
  config/               env-driven config
```

Dependencies: `modernc.org/sqlite` (cgo-free) and `jackc/pgx/v5` for the metadata stores;
everything else (incl. the S3 driver and SigV4) is stdlib only.

## Config (env)

| Var | Default | Meaning |
|---|---|---|
| `LINEAGE_MODEL_API_ADDR` | `:8081` | Model API listen addr |
| `LINEAGE_ADMIN_ADDR` | `:8080` | Admin UI listen addr |
| `LINEAGE_METRICS_ADDR` | `:9090` | ops listen addr |
| `LINEAGE_DB_ENGINE` | `sqlite` | `sqlite` \| `postgres` \| `memory` |
| `LINEAGE_DB_PATH` | `lineage.db` | SQLite file path / Postgres DSN |
| `LINEAGE_STORAGE_DRIVER` | `fs` | `fs` \| `s3` |
| `LINEAGE_STORAGE_ROOT` | `./data/artifacts` | fs backend root |
| `LINEAGE_S3_BUCKET` / `_REGION` / `_ENDPOINT` | — | s3 target (endpoint overrides for MinIO/R2/Ceph) |
| `LINEAGE_S3_ACCESS_KEY` / `_SECRET_KEY` | — | pin static keys; omit to use the IRSA/ECS/IMDS chain (§05.4.1) |
| `LINEAGE_S3_PATH_STYLE` | `false` | `true` for MinIO/Ceph |
| `LINEAGE_STORAGE_GC` | `retain` | `sweep` enables reference-counted GC (§05.8) |
| `LINEAGE_GC_GRACE` / `_INTERVAL` / `_PREFIX` | `24h` / `1h` / `""` | GC eligibility age, sweep period, owned prefix |
| `LINEAGE_ACTOR_HEADER` | `X-Lineage-Actor` | trusted identity header for audit |

Auth is **out of scope** (infra's job, §00 axiom 4) — the binary trusts the actor header.

## Next

- **M9:** Helm chart + deploy profiles (§08)
- **M10:** SDK & CLI generated from the OpenAPI spec (§10)
- **M11:** managed service (separate repo, §11)
