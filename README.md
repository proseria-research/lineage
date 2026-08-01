<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/mark-dark.svg" />
  <img src="docs/assets/mark-light.svg" alt="Lineage" width="56" height="56" />
</picture>

# Lineage

**The self-hostable system of record and model registry for ML/AI models.**

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)
[![Deploy](https://img.shields.io/badge/Deploy-Helm-0F1689?logo=helm&logoColor=white)](deploy/helm/lineage)
[![Status](https://img.shields.io/badge/status-active-brightgreen.svg)](MILESTONES.md)

[Documentation](docs/) · [Quickstart](#quickstart) · [API](docs/03-model-api.md) · [Roadmap](MILESTONES.md)

</div>

---

Lineage is a model registry and governance layer that sits between experimentation and
production. It tracks what models and versions exist, where their artifacts live, what
lifecycle stage each is in, who owns them, and how serving systems fetch them.

## Highlights

**Registry & governance**
- Models, versions, artifacts, deployments, and a typed **lineage graph**, with an
  append-only audit trail on every change.
- Lifecycle stages (`draft → staging → production → archived`) with a singleton `production`
  invariant — promoting a version transactionally archives the incumbent.
- Complete REST `/v1` API with a hand-authored **OpenAPI 3.1** spec, `Idempotency-Key`
  replay, cursor pagination, and write-once artifact-content guards.

**Frictionless delivery for inference systems**
- One-call `resolve` returns a native `storageUri`, a fresh signed URL, digest, size, and
  model format — KServe, Modal, and Baseten pull with near-zero glue.
- HTTP-cacheable (`ETag` / `304`) with event-driven invalidation; a `/content` broker that
  redirects to a signed URL or streams through (Range-capable) for filesystem backends.
- `lineage://model/stage` resolves at pull time through a KServe storage-initializer, so a
  promotion takes effect with no redeploy.

**Pluggable, verified storage**
- Filesystem (dev / air-gapped) or any **S3-compatible** backend — AWS, MinIO, Cloudflare
  R2, Ceph — with signed GET/PUT, multipart uploads, digest verification, and
  reference-counted garbage collection.
- Signature V4 is hand-rolled (no AWS SDK dependency), with an IRSA / ECS / IMDS credential
  chain. Verified against a live MinIO server.

**Metadata that scales down and up**
- **SQLite** (zero-dependency dev / edge) or **Postgres** (HA / production, with `FOR UPDATE`
  locking and JSONB filtering) behind one `MetadataStore` port and a shared logical schema.
  Verified against a real embedded Postgres.
- Resolution cache is in-memory (dev) or **Redis** (prod), behind a single port.

**Embedded admin console**
- A Vite / React single-page app with a monochrome design system, compiled into the binary
  via `go:embed` — dashboards, model and version detail, the lineage graph, an audit
  timeline, and stage promotion — served by an in-process backend-for-frontend.

**Production operations**
- `/healthz`, a real `/readyz` (dependency reachability), and Prometheus `/metrics` from a
  dependency-free registry: RED metrics with templated route labels, cache and lifecycle
  counters, and domain gauges. Structured JSON access logs carry a request id and W3C
  `traceparent` correlation.
- A first-class **Helm chart** and a distroless, non-root, statically linked image — one
  `helm install` yields a working, secure registry.

## How Lineage compares

Lineage is built for teams that want to **own** their model registry — self-hosted, over
their own database and object storage, with governance and delivery built in. It compares
against **Kubeflow Model Registry** (the closest analog) and **Hugging Face Hub**.

| Capability | Lineage | Kubeflow Model Registry | Hugging Face Hub |
| --- | --- | --- | --- |
| **Footprint** | Single self-contained Go binary + Helm chart | Kubernetes / Kubeflow, MLMD-based | SaaS (managed; enterprise / VPC) |
| **Metadata store** | SQLite (dev / edge) or Postgres (HA) — your database | MLMD on MySQL | Managed, Git-backed repos |
| **Artifact storage** | S3 / GCS / Azure / filesystem, signed-URL delivery | External URI references | HF-hosted (Git-LFS / Xet) |
| **Lineage / provenance** | Typed graph: ancestry + impact traversal | MLMD lineage | Informal `base_model` tag |
| **Serving delivery** | `resolve` → signed URL + `lineage://` KServe initializer | KServe | HF Inference Endpoints |
| **Lifecycle & governance** | Stages + singleton production, fully audited | Lifecycle state | Tags & model cards |
| **API contract** | REST + OpenAPI 3.1 (SDK / CLI generated) | REST + Python client | REST + `huggingface_hub` |
| **Authentication** | Delegated to infrastructure | Cluster identity | Built-in accounts & tokens |

**Why these differences matter**

- **Minimal operational surface.** A single self-contained binary and a Helm chart run the
  whole system — from a laptop with SQLite to high availability on Postgres, from the same
  image. There is no separate metadata service to deploy, operate, and secure alongside it.
- **Your models stay yours.** Metadata lives in your own database and artifact bytes in your
  own object store; Lineage keeps only pointers. Nothing is hosted on a third party's
  infrastructure, and the storage remains portable — plain relational tables and native
  object-store URIs.
- **Provenance you can actually query.** `derived_from` / `trained_on` / `produced_by` /
  `deployed_as` are typed edges, so you can answer *"what produced this model?"* (ancestry)
  and *"if this dataset is bad, what's affected?"* (impact) — not just read an informal
  `base_model` tag.
- **Built for the pull path.** One `resolve` call returns a native `storageUri`, a fresh
  signed URL, and a digest, and `lineage://model/stage` resolves at pull time — so a
  promotion reaches KServe, Modal, or Baseten with no redeploy and near-zero glue.
- **Governance without adopting a platform.** Lifecycle stages with a singleton `production`
  invariant and an append-only audit on every change give you promotion control and a
  compliance trail without buying into an entire ML platform.

Hugging Face Hub remains excellent for public model sharing and discovery — Lineage is
deliberately **not** a public hub, and does not try to be.

## Design principles

1. **Self-hosting is the primary distribution model.** The hosted experience never
   compromises the self-hosted one.
2. **One binary, two surfaces, two ports.** A single process serves the Admin console
   (`:8080`) and the machine-facing Model API (`:8081`). The port selects the surface.
3. **Storage-agnostic, metadata-authoritative.** Lineage owns metadata and pointers; artifact
   bytes live in pluggable backends and flow directly between storage and consumer.
4. **Auditable by default.** Every state transition is recorded as a durable domain event.
5. **Authentication is the infrastructure's job.** Lineage trusts already-authenticated
   requests and records an infra-provided identity header for audit attribution.

## Quickstart

### Run locally

```bash
make run          # or: go run ./cmd/lineage
```

Three listeners come up:

| Port    | Surface                                                        |
| ------- | ------------------------------------------------------------- |
| `:8081` | **Model API** (`/v1`) — publish, resolve, fetch (machines)    |
| `:8080` | **Admin console** — human web UI + backend-for-frontend       |
| `:9090` | **Ops** — `/healthz`, `/readyz`, `/metrics`                   |

Publish a model, promote it, and resolve it:

```bash
curl -XPOST localhost:8081/v1/models -H X-Lineage-Actor:me \
  -d '{"name":"fraud-detector"}'

curl -XPOST localhost:8081/v1/models/fraud-detector/versions -H X-Lineage-Actor:me \
  -d '{"name":"1.4.0","artifacts":[{"name":"model.onnx","uri":"s3://m/1.4.0","modelFormat":{"name":"onnx"}}]}'

curl -XPOST localhost:8081/v1/models/fraud-detector/versions/1.4.0:transition -d '{"to":"staging"}'
curl -XPOST localhost:8081/v1/models/fraud-detector/versions/1.4.0:transition -d '{"to":"production"}'

curl "localhost:8081/v1/models/fraud-detector/resolve?stage=production"
```

Open the console at <http://localhost:8080> and the API contract at
<http://localhost:8081/v1/openapi.json>.

### Load sample data

With the registry running, seed a demo dataset — five models across every stage, with
uploaded and by-reference artifacts, lineage edges, deployments, and a real audit trail:

```bash
make seed                       # or: go run ./cmd/lineage-seed
make seed SEED_FLAGS=-reset     # replace previously seeded data
```

It talks to the Model API like any other client, so `LINEAGE_ENDPOINT` (default
`http://localhost:8081`) points it at a port-forward or a remote install.

### Deploy with Helm

```bash
# dev: SQLite on a PVC, filesystem storage, zero external dependencies
helm install lineage deploy/helm/lineage -f deploy/helm/lineage/values-dev.yaml

# prod: external Postgres + S3, autoscaling, PodDisruptionBudget, NetworkPolicy, ServiceMonitor
helm install lineage deploy/helm/lineage -f deploy/helm/lineage/values-prod.yaml \
  --set database.postgres.dsnSecret.name=lineage-db \
  --set ingress.modelApi.host=api.example.com
```

SQLite is single-writer, so SQLite installs are pinned to one replica — the chart rejects a
multi-replica SQLite configuration at template time.

## Architecture

One binary, two surfaces on two ports, over a shared domain core (ports & adapters). The core
depends only on **port interfaces**; adapters are wired at startup and never imported by the
core.

```
cmd/
  lineage/            main: load config → wire adapters into ports → serve
  lineage-init/       KServe storage-initializer for lineage:// URIs
internal/
  domain/             entities, enums, the stage machine, errors, and PORT INTERFACES
  core/               business logic over the ports (publish, transition, resolve, GC, graph)
  api/
    modelapi/         /v1 machine API + hand-authored OpenAPI spec
    adminui/          :8080 BFF + the embedded web console (web/, go:embed)
  adapters/
    store/{memory,sqlite,postgres}   MetadataStore (shared sqlstore + per-dialect seam)
    storage/{fs,s3}                  StorageBackend (s3 is a hand-rolled SigV4 client)
    cache/{memory,redis}             ResolutionCache
    events/                          EventBus
  observability/      health probes + a dependency-free Prometheus registry
  config/             environment-driven configuration
deploy/helm/lineage/  the Helm chart (dev/prod value profiles)
docs/                 numbered design documents (00–11)
```

**Runtime dependencies** are deliberately minimal: `modernc.org/sqlite` (cgo-free) and
`jackc/pgx/v5` for the metadata stores, and `redis/go-redis` for the Redis cache. Everything
else — the S3 driver and SigV4 signer, the Prometheus registry, and the OpenAPI document — is
standard library or hand-authored.

## Configuration

All configuration is environment-driven.

| Variable                    | Default            | Description                                                     |
| --------------------------- | ------------------ | -------------------------------------------------------------- |
| `LINEAGE_MODEL_API_ADDR`    | `:8081`            | Model API listen address                                       |
| `LINEAGE_ADMIN_ADDR`        | `:8080`            | Admin console listen address                                   |
| `LINEAGE_METRICS_ADDR`      | `:9090`            | Ops (health / metrics) listen address                          |
| `LINEAGE_DB_ENGINE`         | `sqlite`           | `sqlite` \| `postgres` \| `memory`                             |
| `LINEAGE_DB_PATH`           | `lineage.db`       | SQLite file path, or Postgres DSN                              |
| `LINEAGE_CACHE_ENGINE`      | `memory`           | `memory` \| `redis`                                            |
| `LINEAGE_REDIS_ADDR`        | `localhost:6379`   | Redis address (when `redis`)                                   |
| `LINEAGE_REDIS_PASSWORD`    | —                  | Redis password (when `redis`)                                  |
| `LINEAGE_REDIS_DB`          | `0`                | Redis database index (when `redis`)                            |
| `LINEAGE_STORAGE_DRIVER`    | `fs`               | `fs` \| `s3`                                                   |
| `LINEAGE_STORAGE_ROOT`      | `./data/artifacts` | Filesystem backend root                                        |
| `LINEAGE_S3_BUCKET`         | —                  | S3 bucket                                                      |
| `LINEAGE_S3_REGION`         | —                  | S3 region                                                     |
| `LINEAGE_S3_ENDPOINT`       | —                  | S3 endpoint override (for MinIO / R2 / Ceph)                   |
| `LINEAGE_S3_ACCESS_KEY`     | —                  | Static access key; omit to use the IRSA / ECS / IMDS chain     |
| `LINEAGE_S3_SECRET_KEY`     | —                  | Static secret key; omit to use the IRSA / ECS / IMDS chain     |
| `LINEAGE_S3_PATH_STYLE`     | `false`            | `true` for MinIO / Ceph                                        |
| `LINEAGE_STORAGE_GC`        | `retain`           | `sweep` enables reference-counted garbage collection           |
| `LINEAGE_GC_GRACE`          | `24h`              | Minimum object age before it is eligible for GC                |
| `LINEAGE_GC_INTERVAL`       | `1h`               | GC sweep period                                                |
| `LINEAGE_GC_PREFIX`         | —                  | Storage prefix the sweeper is scoped to                        |
| `LINEAGE_ACTOR_HEADER`      | `X-Lineage-Actor`  | Trusted identity header recorded on audit events               |

Authentication is out of scope by design — the surrounding infrastructure
(ingress / gateway / mesh / NetworkPolicy) owns authN/authZ, and the binary trusts the actor
header for audit attribution only.

## Contributing

Contributions are welcome — issues, discussions, and pull requests alike.

### Prerequisites

- **Go 1.25+** — required for everything.
- **Node 20+ and pnpm** — only to rebuild the admin console.
- **Docker and Helm** — only for image and chart work.

### Build and test

```bash
make build        # compile the binary
make test         # go test ./...  (SQLite, embedded Postgres, and miniredis run in-process)
make fmt vet      # format and static-check
make web          # rebuild the embedded console (Vite / pnpm)
make docker       # build the container image
make helm-lint    # validate the Helm chart against both value profiles
```

The console's built assets are committed under `internal/api/adminui/web/dist`, so `go build`
and `go test` never require Node — run `make web` only when changing the console. The test
suite is self-contained: it spins up a throwaway Postgres and an in-memory Redis, so no
external services are needed.

### Conventions

- **Ports and adapters.** The domain core (`internal/core`, `internal/domain`) depends only
  on port interfaces. Adapters are wired at startup and are never imported by the core.
- **Documentation.** Design docs live in [`docs/`](docs/), numbered in reading order; every
  diagram is a Mermaid block; prose is kept concise. Architectural decisions are recorded in
  [`docs/00-preplanning.md`](docs/00-preplanning.md) §11, which is the decision record.
- **Tests.** New behavior ships with tests. Keep `go test ./...`, `gofmt`, and `go vet` clean.

### Pull requests

1. Keep each change focused and explain the motivation.
2. Run `make fmt vet test` and ensure the full suite passes.
3. Reference the relevant design document or milestone where it adds context.

## Documentation

Design documents live in [`docs/`](docs/), numbered in reading order:

| Doc | Topic |
| --- | --- |
| [`00`](docs/00-preplanning.md) | Framing, axioms, and decisions |
| [`01`](docs/01-architecture-overview.md) | Architecture overview |
| [`02`](docs/02-data-model.md) | Data model |
| [`03`](docs/03-model-api.md) | Model API |
| [`04`](docs/04-delivery-api.md) | Consumption & delivery |
| [`05`](docs/05-storage-and-artifacts.md) | Storage & artifacts |
| [`06`](docs/06-admin-ui-api.md) | Admin console |
| [`07`](docs/07-lineage-and-provenance.md) | Lineage & provenance |
| [`08`](docs/08-deployment-helm.md) | Deployment & Helm |
| [`09`](docs/09-observability-and-ops.md) | Observability & ops |
| [`10`](docs/10-sdk-and-cli.md) | SDK & CLI |
| [`11`](docs/11-model-insights.md) | Model insights & architecture diff |

## Project status

Actively developed. Milestones **M0–M9 and M11 are complete** — architecture, persistence,
storage, delivery, the full `/v1` API, the admin console, the lineage graph, observability,
the Helm chart, and model insights (composition facts, architecture fingerprint, and
version diff). Remaining: an OpenAPI-generated **Python SDK and CLI** (M10). Progress is
tracked in [`MILESTONES.md`](MILESTONES.md).

## License

Licensed under the [Apache License 2.0](LICENSE).
