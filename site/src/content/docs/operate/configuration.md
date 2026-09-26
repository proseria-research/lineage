---
title: Configuration
description: Every environment variable the lineage binary reads, with its default and meaning.
sidebar:
  order: 1
---

Lineage is configured only through environment variables. There is no config file and no
flags. The Helm chart renders these from `values.yaml`; see [Deploy](/operate/deploy/).

## Parsing rules

| Kind | Behaviour on a bad value |
| --- | --- |
| Retention and seal integers | Startup fails with `configuration: …`. A floor nobody can read is not guessed. |
| `LINEAGE_ACTOR_HEADER` | Startup fails unless it is a valid RFC 9110 header name. |
| Engine and driver names | Startup fails with `unknown driver/engine`. |
| Durations, `LINEAGE_REDIS_DB`, `LINEAGE_TRACE_SAMPLE_RATIO` | Silently fall back to the default. |
| Booleans (`*_PATH_STYLE`, `*_PLAIN_HTTP`) | Only the exact string `true` enables. |

An empty variable is treated as unset. Durations use Go syntax: `90s`, `30m`, `24h`.

## Listeners

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_ADMIN_ADDR` | `:8080` | Admin UI: the web console and its `/api` backend. |
| `LINEAGE_MODEL_API_ADDR` | `:8081` | Model API: `/v1` publish, resolve, fetch. |
| `LINEAGE_METRICS_ADDR` | `:9090` | Ops: `/healthz`, `/readyz`, `/metrics`. |

The port selects the surface. There is no path prefix to separate them.

## Metadata store

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_DB_ENGINE` | `sqlite` | `sqlite`, `postgres`, or `memory`. `memory` loses everything on restart. |
| `LINEAGE_DB_PATH` | `lineage.db` | SQLite file path, or the Postgres DSN. Ignored for `memory`. |

See [Storage](/operate/storage/) for how the engines differ.

## Resolution cache

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_CACHE_ENGINE` | `memory` | `memory` or `redis`. |
| `LINEAGE_REDIS_ADDR` | `localhost:6379` | Redis `host:port`. Pinged at startup; unreachable is fatal. |
| `LINEAGE_REDIS_PASSWORD` | empty | Redis password. |
| `LINEAGE_REDIS_DB` | `0` | Redis database index. |

With `memory`, invalidation is in-process. On more than one replica, a promotion is seen by
other replicas only when their entry expires (60 s). Use `redis` for multi-replica installs.

## Storage backend

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_STORAGE_DRIVER` | `fs` | `fs`, `s3`, or `oci`. |
| `LINEAGE_STORAGE_ROOT` | `./data/artifacts` | `fs` only: root directory for artifact files. |

### S3-compatible (`s3`)

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_S3_BUCKET` | empty | Bucket. Required; startup fails without it. |
| `LINEAGE_S3_REGION` | empty | Region. Empty means `us-east-1`. |
| `LINEAGE_S3_ENDPOINT` | empty | Endpoint URL for MinIO, R2, Ceph. Empty means `https://s3.<region>.amazonaws.com`. |
| `LINEAGE_S3_PATH_STYLE` | `false` | `true` for path-style addressing (MinIO, Ceph, many R2 setups). |
| `LINEAGE_S3_ACCESS_KEY` | empty | Static access key. Needs `LINEAGE_S3_SECRET_KEY` too. |
| `LINEAGE_S3_SECRET_KEY` | empty | Static secret key. |
| `LINEAGE_S3_SESSION_TOKEN` | empty | Session token, used only with the static keys. |

When both static keys are unset, the driver resolves credentials from a chain, first match
wins:

| Order | Source | Variables read |
| --- | --- | --- |
| 1 | Environment | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` |
| 2 | Web identity (IRSA) | `AWS_ROLE_ARN`, `AWS_WEB_IDENTITY_TOKEN_FILE`, `AWS_ROLE_SESSION_NAME` (default `lineage`) |
| 3 | Container credentials (ECS) | `AWS_CONTAINER_CREDENTIALS_RELATIVE_URI`, or `AWS_CONTAINER_CREDENTIALS_FULL_URI` with `AWS_CONTAINER_AUTHORIZATION_TOKEN` |
| 4 | EC2 instance role | IMDSv2, no variables |

Temporary credentials are cached and refreshed five minutes before expiry.

### OCI registry (`oci`)

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_OCI_REGISTRY` | empty | Registry `host[:port]`, no path. Required. |
| `LINEAGE_OCI_REPOSITORY` | empty | Repository prefix Lineage owns. A model's repository is `<prefix>/<model>`. |
| `LINEAGE_OCI_USERNAME` | empty | Robot account or token user. Unset means anonymous. |
| `LINEAGE_OCI_PASSWORD` | empty | Password or token. |
| `LINEAGE_OCI_PLAIN_HTTP` | `false` | `true` to talk HTTP instead of HTTPS (in-cluster or dev registries). |

## Garbage collection

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_STORAGE_GC` | `retain` | `retain` never deletes bytes. `sweep` runs the background sweeper. |
| `LINEAGE_GC_PREFIX` | empty | Prefix the sweeper lists. Empty means the whole bucket or root. |
| `LINEAGE_GC_GRACE` | `24h` | Minimum object age before it is eligible. |
| `LINEAGE_GC_INTERVAL` | `1h` | Time between sweeps. The first sweep runs one interval after startup. |

Read [Storage](/operate/storage/#garbage-collection) before enabling `sweep`.

## Delivery and signing

No variables. These values are fixed in the binary:

| Setting | Value |
| --- | --- |
| Signed download URL lifetime | 15 minutes, minted per response, never cached |
| Upload session and signed upload URL lifetime | 1 hour |
| Multipart upload threshold (`s3` only) | 64 MiB |
| Resolution cache entry TTL | 60 seconds (backstop; promotions invalidate eagerly) |

## Governance and retention

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_RETENTION_MIN_ARCHIVED_VERSION_DAYS` | `0` | DELETE of a model or version is refused while the youngest record it would destroy is younger than this. `0` disables the floor. |
| `LINEAGE_RETENTION_MIN_AUDIT_AGE_DAYS` | `0` | Reported audit-retention floor. Not enforced: nothing deletes audit events. |

Both must be non-negative integers. The chart sets both to `3650`; the binary defaults to `0`.
The values in force are echoed at `/healthz` and `GET /v1/retention`. See
[Compliance](/governance/compliance/).

## Audit sealing

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_AUDIT_ATTESTATION` | `on` | Merkle epoch sealing of the audit log. Only `off` disables it. |
| `LINEAGE_SEAL_INTERVAL_SECONDS` | `60` | Epoch length, and the bound on the unsealed window. Must be positive. |
| `LINEAGE_SEAL_GRACE_SECONDS` | `5` | Delay after an epoch ends before it is sealed. Must be `>= 0` and less than the interval. |

Changing the interval on a registry that has already sealed makes the sealer refuse to seal
and log the refusal every interval until the old value is restored. See
[Audit](/governance/audit/).

## Identity

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_ACTOR_HEADER` | `X-Lineage-Actor` | Request header whose value is recorded verbatim as the actor on audit events. Never used for authorization. |

## Tracing

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_OTLP_ENDPOINT` | `OTEL_EXPORTER_OTLP_ENDPOINT`, else empty | OTLP/HTTP collector. `host:port` is plaintext; `http://` or `https://` URLs are used as given. Empty disables tracing. |
| `LINEAGE_SERVICE_NAME` | `lineage` | `service.name` resource attribute. |
| `LINEAGE_TRACE_SAMPLE_RATIO` | `1.0` | Parent-based head sampling ratio, `0`–`1`. |

## Console

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_PUBLIC_MODEL_API_URL` | empty | Model API URL shown in the console's usage snippets. Trailing `/` is trimmed. Empty means the console's own host on the Model API port. |
| `LINEAGE_DOCS_URL` | `https://lineage.proseria.dev` | Target of the console's Documentation link. |

## Companion binaries

These are read by the client-side binaries, not by `lineage`.

| Variable | Binary | Default | Meaning |
| --- | --- | --- | --- |
| `LINEAGE_ENDPOINT` | `lineage-init` | `http://lineage-model-api:8081` | Model API base URL. |
| `LINEAGE_ENDPOINT` | `lineage-seed` | `http://localhost:8081` | Model API base URL. |
| `LINEAGE_SERVER` | `lineage-cli` | `http://localhost:8081` | Model API base URL. |
| `LINEAGE_ACTOR` | all three | empty (`seed@lineage.dev` for `lineage-seed`) | Sent as `X-Lineage-Actor`. |

## Minimal production example

Postgres, S3 through IRSA, shared Redis, sealing and a ten-year floor. Put the DSN and any
passwords in Secrets.

```bash
LINEAGE_DB_ENGINE=postgres
LINEAGE_DB_PATH=postgres://lineage@db.internal:5432/lineage?sslmode=require   # from a Secret

LINEAGE_CACHE_ENGINE=redis
LINEAGE_REDIS_ADDR=redis.internal:6379

LINEAGE_STORAGE_DRIVER=s3
LINEAGE_S3_BUCKET=acme-lineage-models    # dedicated to Lineage
LINEAGE_S3_REGION=eu-west-1

LINEAGE_RETENTION_MIN_ARCHIVED_VERSION_DAYS=3650
LINEAGE_RETENTION_MIN_AUDIT_AGE_DAYS=3650

LINEAGE_PUBLIC_MODEL_API_URL=https://models.example.com
LINEAGE_OTLP_ENDPOINT=otel-collector.observability:4318
```

Everything else keeps its default: listeners on `:8080`/`:8081`/`:9090`, sealing on at 60 s,
GC set to `retain`.

Next: [Deploy](/operate/deploy/) · [Storage](/operate/storage/) · [Observability and security](/operate/observability/)
