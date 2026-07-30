# Lineage

A self-hostable, feature-complete **AI model registry** — the system of record for ML
models from post-experimentation to production. Benchmarked against Kubeflow Model
Registry for **capability parity, not wire compatibility**.

> Design docs live in [`docs/`](docs/) (`00`–`11`). Start with
> [`docs/00-preplanning.md`](docs/00-preplanning.md) and
> [`docs/01-architecture-overview.md`](docs/01-architecture-overview.md).

## Status

**Early, working.** The Go skeleton compiles, runs, and serves the core happy path
(create model → publish version + artifacts → stage transitions → resolve). Metadata now
persists in **SQLite or Postgres** (per-dialect adapters behind the `MetadataStore` port,
§02.7); an in-memory store remains for tests/dev. Storage still uses the `fs` backend
(S3 is a stub); Redis cache is a stub.

Set the engine with `LINEAGE_DB_ENGINE=sqlite|postgres|memory`. Progress is tracked in
[`MILESTONES.md`](MILESTONES.md) (M0–M2 done; M3 storage in progress).

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
internal/
  domain/               entities, enums, stage machine, errors, PORT INTERFACES
  core/                 business logic over the ports (publish, transition, resolve)
  api/
    modelapi/           /v1 machine API (§03/§04)
    adminui/            :8080 human console BFF (§06)
  adapters/
    store/{memory,sqlite,postgres}   MetadataStore (all real; shared sqlstore + Dialect, §02.7)
    storage/{fs,s3}                  StorageBackend (fs works; s3 stub, §05)
    cache/memory                     ResolutionCache (§04.4)
    events                           EventBus
  observability/        health + metrics (§09)
  config/               env-driven config
```

Zero external dependencies (stdlib only) at this stage.

## Config (env)

| Var | Default | Meaning |
|---|---|---|
| `LINEAGE_MODEL_API_ADDR` | `:8081` | Model API listen addr |
| `LINEAGE_ADMIN_ADDR` | `:8080` | Admin UI listen addr |
| `LINEAGE_METRICS_ADDR` | `:9090` | ops listen addr |
| `LINEAGE_STORAGE_ROOT` | `./data/artifacts` | fs backend root |
| `LINEAGE_ACTOR_HEADER` | `X-Lineage-Actor` | trusted identity header for audit |

Auth is **out of scope** (infra's job, §00 axiom 4) — the binary trusts the actor header.

## Next

- **M3:** S3 `StorageBackend` with signed URLs + the upload initiate/finalize flow (§05)
- **M4:** resolve cache wired to the event bus; `lineage://` initializer (§04)
- **M5:** full `/v1` (artifacts/lineage/deployments CRUD, cursor pagination), OpenAPI (§03)
- Helm chart (§08), SDK/CLI (§10)
