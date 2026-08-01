---
title: Container image
description: A distroless, non-root, statically linked image — how it is built and how to run it without Kubernetes.
sidebar:
  order: 4
---

One image, one binary, three ports. Built in three stages: the console, the Go binary, and a
minimal runtime.

```dockerfile
FROM node:22-alpine AS web          # 1. build the embedded admin console
FROM golang:1.25-alpine AS build    # 2. static, cgo-free Go binary with the console embedded
FROM gcr.io/distroless/static-debian12:nonroot
```

The final image is `distroless/static` running as `nonroot`. No shell, no package manager,
nothing to exec into.

The binary is cgo-free — `modernc.org/sqlite` and `pgx` are pure Go — so it is statically
linked and needs no libc in the runtime layer.

## Build it

```bash
make docker
```

```bash
docker build --build-arg VERSION=$(git describe --tags --always) -t lineage:dev .
```

## Run it

```bash
docker run --rm \
  -p 8081:8081 -p 8080:8080 -p 9090:9090 \
  -v lineage-data:/data \
  -e LINEAGE_DB_PATH=/data/lineage.db \
  -e LINEAGE_STORAGE_ROOT=/data/artifacts \
  ghcr.io/proseria-research/lineage:latest
```

The volume matters. Without it, SQLite and the filesystem artifact root live in the container
layer and vanish with the container.

## Compose

```yaml
services:
  lineage:
    image: ghcr.io/proseria-research/lineage:latest
    ports: ["8081:8081", "8080:8080"]
    environment:
      LINEAGE_DB_ENGINE: postgres
      LINEAGE_DB_PATH: postgres://lineage:lineage@db:5432/lineage?sslmode=disable
      LINEAGE_CACHE_ENGINE: redis
      LINEAGE_REDIS_ADDR: cache:6379
      LINEAGE_STORAGE_DRIVER: s3
      LINEAGE_S3_ENDPOINT: http://minio:9000
      LINEAGE_S3_BUCKET: models
      LINEAGE_S3_PATH_STYLE: "true"
      LINEAGE_S3_ACCESS_KEY: minioadmin
      LINEAGE_S3_SECRET_KEY: minioadmin
    depends_on: [db, cache, minio]

  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: lineage
      POSTGRES_PASSWORD: lineage
      POSTGRES_DB: lineage

  cache:
    image: redis:7-alpine

  minio:
    image: minio/minio
    command: server /data
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
```

This is the full production shape on a laptop — Postgres, Redis, and S3-compatible storage —
which makes it the right place to test signed-URL and multipart paths.

## Ports

| Port | Surface | Expose to |
| --- | --- | --- |
| `8081` | Model API | Machines |
| `8080` | Admin console | People |
| `9090` | Ops | Your monitoring only |

## Health

```bash
curl -s localhost:9090/healthz     # process alive
curl -s localhost:9090/readyz      # dependencies reachable
```

:::note[No shell, no curl in the image]
A distroless image has neither, so a Docker `HEALTHCHECK` using `CMD-SHELL` or `curl` will not
work. Probe it from outside the container, or under Kubernetes use HTTP probes against
`/healthz` and `/readyz` — the Helm chart already wires both.
:::

## Companion images

| Image | Purpose |
| --- | --- |
| `ghcr.io/proseria-research/lineage` | The registry |
| `ghcr.io/proseria-research/lineage-init` | KServe storage-initializer for `lineage://` URIs |

See [KServe](/delivery/kserve/).

## Next

- [Deploy with Helm](/deploy/helm/)
- [Configuration](/operate/configuration/)
- [Local development](/deploy/local-development/)
