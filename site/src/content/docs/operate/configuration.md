---
title: Configuration
description: Every environment variable Lineage reads, what it defaults to, and which ones you actually need to set.
sidebar:
  order: 1
---

All configuration is environment-driven. There is no config file — the same binary reads the
same variables whether it runs from a shell, a container, or a Helm release.

## Listeners

| Variable | Default | Description |
| --- | --- | --- |
| `LINEAGE_MODEL_API_ADDR` | `:8081` | Model API (`/v1`) listen address |
| `LINEAGE_ADMIN_ADDR` | `:8080` | Admin console listen address |
| `LINEAGE_METRICS_ADDR` | `:9090` | Ops — `/healthz`, `/readyz`, `/metrics` |

The port selects the surface. Expose `:8081` to machines, `:8080` to people, and keep `:9090`
inside the cluster.

## Metadata store

| Variable | Default | Description |
| --- | --- | --- |
| `LINEAGE_DB_ENGINE` | `sqlite` | `sqlite` \| `postgres` \| `memory` |
| `LINEAGE_DB_PATH` | `lineage.db` | SQLite file path, or the Postgres DSN |

`memory` exists for tests. It loses everything on restart.

See [Choosing a metadata store](/operate/metadata-store/).

## Resolution cache

| Variable | Default | Description |
| --- | --- | --- |
| `LINEAGE_CACHE_ENGINE` | `memory` | `memory` \| `redis` |
| `LINEAGE_REDIS_ADDR` | `localhost:6379` | Redis address, when `redis` |
| `LINEAGE_REDIS_PASSWORD` | — | Redis password |
| `LINEAGE_REDIS_DB` | `0` | Redis database index |

Multiple replicas need `redis`. See [Caching and invalidation](/delivery/caching/).

## Storage

| Variable | Default | Description |
| --- | --- | --- |
| `LINEAGE_STORAGE_DRIVER` | `fs` | `fs` \| `s3` |
| `LINEAGE_STORAGE_ROOT` | `./data/artifacts` | Filesystem backend root |
| `LINEAGE_S3_BUCKET` | — | Bucket |
| `LINEAGE_S3_REGION` | — | Region |
| `LINEAGE_S3_ENDPOINT` | — | Endpoint override, for MinIO / R2 / Ceph |
| `LINEAGE_S3_ACCESS_KEY` | — | Static access key; omit to use the credential chain |
| `LINEAGE_S3_SECRET_KEY` | — | Static secret key; omit to use the credential chain |
| `LINEAGE_S3_PATH_STYLE` | `false` | `true` for MinIO and Ceph |

Omitting the static keys is the better option in a cluster — the credential chain picks up
IRSA, EKS Pod Identity, ECS task roles, or IMDSv2 automatically. See
[Storage backends](/operate/storage-backends/).

## Garbage collection

| Variable | Default | Description |
| --- | --- | --- |
| `LINEAGE_STORAGE_GC` | `retain` | `sweep` enables reference-counted collection |
| `LINEAGE_GC_GRACE` | `24h` | Minimum object age before it is eligible |
| `LINEAGE_GC_INTERVAL` | `1h` | Sweep period |
| `LINEAGE_GC_PREFIX` | — | Storage prefix the sweeper is scoped to |

The default retains bytes forever. See [Garbage collection](/operate/garbage-collection/).

## Identity

| Variable | Default | Description |
| --- | --- | --- |
| `LINEAGE_ACTOR_HEADER` | `X-Lineage-Actor` | Trusted identity header recorded on audit events |

Lineage does not authenticate. It reads this header and records it. Your perimeter must
overwrite it on every inbound request — see [Security model](/operate/security-model/).

## Profiles

**Laptop.** Everything defaults correctly. `make run` and you have a registry.

**Small self-hosted install.** SQLite on a persistent volume, filesystem storage, one
replica:

```bash
LINEAGE_DB_ENGINE=sqlite
LINEAGE_DB_PATH=/data/lineage.db
LINEAGE_STORAGE_DRIVER=fs
LINEAGE_STORAGE_ROOT=/data/artifacts
```

**Production.** Postgres, S3, Redis, multiple replicas:

```bash
LINEAGE_DB_ENGINE=postgres
LINEAGE_DB_PATH=postgres://lineage@db.internal:5432/lineage?sslmode=require
LINEAGE_CACHE_ENGINE=redis
LINEAGE_REDIS_ADDR=redis.internal:6379
LINEAGE_STORAGE_DRIVER=s3
LINEAGE_S3_BUCKET=acme-models
LINEAGE_S3_REGION=eu-west-1
LINEAGE_STORAGE_GC=sweep
```

Keep the DSN and any static keys in a Secret, not in a ConfigMap. The Helm chart does this
for you — see [Deploy with Helm](/deploy/helm/).

## Next

- [Choosing a metadata store](/operate/metadata-store/)
- [Storage backends](/operate/storage-backends/)
- [Production checklist](/deploy/production-checklist/)
