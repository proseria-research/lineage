---
title: Local development
description: Run Lineage from source, rebuild the console, run the tests, and seed a realistic dataset.
sidebar:
  order: 3
---

## Prerequisites

- **Go 1.25+** — required for everything.
- **Node 20+ and pnpm** — only to rebuild the admin console.
- **Docker and Helm** — only for image and chart work.

The console's built assets are committed, so `go build` and `go test` never need Node.

## Run

```bash
git clone https://github.com/proseria-research/lineage
cd lineage
make run          # or: go run ./cmd/lineage
```

Defaults to SQLite in `./lineage.db` and filesystem storage under `./data/artifacts`.

| Port | Surface |
| --- | --- |
| `:8081` | Model API (`/v1`) |
| `:8080` | Admin console |
| `:9090` | `/healthz`, `/readyz`, `/metrics` |

## Make targets

| Target | Does |
| --- | --- |
| `make build` | Compile the binary (rebuilds the console first) |
| `make run` | Build and run |
| `make test` | `go test ./...` — SQLite, embedded Postgres, and miniredis run in-process |
| `make fmt vet` | Format and static-check |
| `make web` | Rebuild the embedded console (Vite / pnpm) |
| `make web-dev` | Console dev server with hot reload |
| `make seed` | Populate a realistic dataset |
| `make sdk` | Regenerate the Python SDK from the OpenAPI spec |
| `make cli` | Build the CLI |
| `make docker` | Build the container image |
| `make helm-lint` | Validate the chart against both value profiles |

## Seed data

```bash
make seed                       # five models across every stage
make seed SEED_FLAGS=-reset     # replace previously seeded data
```

You get uploaded and by-reference artifacts, lineage edges, deployments, and a real audit
trail — enough to make the console and the graph views worth looking at.

The seeder talks to the Model API like any other client, so `LINEAGE_ENDPOINT` (default
`http://localhost:8081`) can point it at a port-forward or a remote install.

## Tests

```bash
make test
```

Self-contained: it spins up a throwaway Postgres and an in-memory Redis, so no external
services are needed. The store conformance suite runs against memory, SQLite, and Postgres,
which is how the two dialects are kept behaviourally identical.

## Working on the console

```bash
make web-dev     # Vite dev server with hot reload
make web         # produce the assets that get embedded
```

The console is a Vite/React SPA compiled into the binary with `go:embed`. Its built assets
live under `internal/api/adminui/web/dist` and are committed — run `make web` when you change
the console, and commit the result.

## Configuration while developing

Everything is environment-driven. Point at a different store without touching code:

```bash
LINEAGE_DB_ENGINE=postgres \
LINEAGE_DB_PATH='postgres://lineage@localhost:5432/lineage?sslmode=disable' \
go run ./cmd/lineage
```

```bash
LINEAGE_STORAGE_DRIVER=s3 \
LINEAGE_S3_ENDPOINT=http://localhost:9000 \
LINEAGE_S3_BUCKET=models \
LINEAGE_S3_PATH_STYLE=true \
LINEAGE_S3_ACCESS_KEY=minioadmin \
LINEAGE_S3_SECRET_KEY=minioadmin \
go run ./cmd/lineage
```

MinIO on `:9000` is the quickest way to exercise the signed-URL paths locally.

## Layout

```
cmd/
  lineage/            main: load config → wire adapters into ports → serve
  lineage-cli/        the command-line client
  lineage-init/       KServe storage-initializer for lineage:// URIs
  lineage-seed/       demo data
internal/
  domain/             entities, enums, the stage machine, errors, PORT INTERFACES
  core/               business logic over the ports
  api/modelapi/       /v1 machine API + the OpenAPI spec
  api/adminui/        :8080 BFF + the embedded console
  adapters/           store, storage, cache, events
  observability/      probes + the Prometheus registry
  config/             environment-driven configuration
deploy/helm/lineage/  the chart
```

Ports and adapters: the domain core depends only on port interfaces. Adapters are wired at
startup and are never imported by the core.

## Next

- [Container image](/deploy/container-image/)
- [Deploy with Helm](/deploy/helm/)
- [Configuration](/operate/configuration/)
