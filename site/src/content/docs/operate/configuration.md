---
title: Configuration
description: Every environment variable the lineage binary reads, with its default and meaning.
sidebar:
  order: 1
---

Lineage is configured only through environment variables. There is no config file and no
flags. The Helm chart renders these from `values.yaml`; see [Deploy](/operate/deploy/).

## Parsing rules

These rules decide what happens when a value is bad or missing.

- Retention and seal integers: a bad value fails startup with `configuration: …`. A floor
  nobody can read is not guessed.
- `LINEAGE_ACTOR_HEADER`: startup fails unless it is a valid RFC 9110 header name.
- Engine and driver names: an unknown name fails startup with `unknown driver/engine`.
- Durations, `LINEAGE_REDIS_DB` and `LINEAGE_TRACE_SAMPLE_RATIO`: a bad value silently falls
  back to the default.
- Booleans (`*_PATH_STYLE`, `*_PLAIN_HTTP`): only the exact string `true` enables.

An empty variable is treated as unset. Durations use Go syntax: `90s`, `30m`, `24h`.

## Listeners, metadata and cache

These set the three ports, the metadata store and the resolution cache.

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_ADMIN_ADDR` | `:8080` | Admin UI: web console and its `/api` backend |
| `LINEAGE_MODEL_API_ADDR` | `:8081` | Model API: `/v1` publish, resolve, fetch |
| `LINEAGE_METRICS_ADDR` | `:9090` | Ops: `/healthz`, `/readyz`, `/metrics` |
| `LINEAGE_DB_ENGINE` | `sqlite` | `sqlite`, `postgres` or `memory` |
| `LINEAGE_DB_PATH` | `lineage.db` | SQLite file path, or the Postgres DSN |
| `LINEAGE_CACHE_ENGINE` | `memory` | `memory` or `redis` |
| `LINEAGE_REDIS_ADDR` | `localhost:6379` | Redis `host:port` |
| `LINEAGE_REDIS_PASSWORD` | empty | Redis password |
| `LINEAGE_REDIS_DB` | `0` | Redis database index |

- The port selects the surface. There is no path prefix to separate them.
- `memory` as the DB engine loses everything on restart; `LINEAGE_DB_PATH` is ignored for it.
  See [Storage](/operate/storage/) for how the engines differ.
- Redis is pinged at startup; unreachable is fatal.
- With the `memory` cache, invalidation is in-process. On more than one replica, a promotion
  is seen by other replicas only when their entry expires (60 s). Use `redis` for
  multi-replica installs.

## Storage backend

These select where artifact bytes live and how Lineage reaches them.

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_STORAGE_DRIVER` | `fs` | `fs`, `s3` or `oci` |
| `LINEAGE_STORAGE_ROOT` | `./data/artifacts` | `fs` only: root directory for artifact files |
| `LINEAGE_S3_BUCKET` | empty | Bucket. Required. |
| `LINEAGE_S3_REGION` | empty | Region. Empty means `us-east-1`. |
| `LINEAGE_S3_ENDPOINT` | empty | Endpoint for MinIO, R2, Ceph |
| `LINEAGE_S3_PATH_STYLE` | `false` | `true` for path-style addressing |
| `LINEAGE_S3_ACCESS_KEY` | empty | Static access key |
| `LINEAGE_S3_SECRET_KEY` | empty | Static secret key |
| `LINEAGE_S3_SESSION_TOKEN` | empty | Session token, only with the static keys |
| `LINEAGE_OCI_REGISTRY` | empty | Registry `host[:port]`, no path. Required. |
| `LINEAGE_OCI_REPOSITORY` | empty | Repository prefix Lineage owns |
| `LINEAGE_OCI_USERNAME` | empty | Robot account or token user |
| `LINEAGE_OCI_PASSWORD` | empty | Password or token |
| `LINEAGE_OCI_PLAIN_HTTP` | `false` | `true` for HTTP instead of HTTPS |

- `s3`: startup fails without a bucket. An empty endpoint means
  `https://s3.<region>.amazonaws.com`. Path-style addressing suits MinIO, Ceph and many R2
  setups. The access key needs `LINEAGE_S3_SECRET_KEY` too.
- `oci`: a model's repository is `<prefix>/<model>`. An unset username means anonymous.
  Plain HTTP is for in-cluster or dev registries.

### S3 credentials

When both static keys are unset, the driver resolves credentials from this chain. The first
match wins:

1. Environment: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`.
2. Web identity (IRSA): `AWS_ROLE_ARN`, `AWS_WEB_IDENTITY_TOKEN_FILE`, `AWS_ROLE_SESSION_NAME`
   (default `lineage`).
3. Container credentials (ECS): `AWS_CONTAINER_CREDENTIALS_RELATIVE_URI`, or
   `AWS_CONTAINER_CREDENTIALS_FULL_URI` with `AWS_CONTAINER_AUTHORIZATION_TOKEN`.
4. EC2 instance role: IMDSv2, no variables.

Temporary credentials are cached and refreshed five minutes before expiry.

## Garbage collection

These control whether unreferenced artifact bytes are deleted.

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_STORAGE_GC` | `retain` | `retain` never deletes bytes; `sweep` runs the sweeper |
| `LINEAGE_GC_PREFIX` | empty | Prefix the sweeper lists. Empty means the whole bucket or root. |
| `LINEAGE_GC_GRACE` | `24h` | Minimum object age before it is eligible |
| `LINEAGE_GC_INTERVAL` | `1h` | Time between sweeps |

The first sweep runs one interval after startup. Read
[Storage](/operate/storage/#garbage-collection) before enabling `sweep`.

## Delivery and signing

These values are fixed in the binary; no variable changes them.

- Signed download URLs last 15 minutes. They are minted per response and never cached.
- Upload sessions and signed upload URLs last 1 hour.
- Multipart upload starts at 64 MiB (`s3` only).
- Resolution cache entries live 60 seconds. This is a backstop; promotions invalidate eagerly.

## Governance, audit and identity

These set retention floors, audit sealing and the actor header.

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_RETENTION_MIN_ARCHIVED_VERSION_DAYS` | `0` | Floor on DELETE of a model or version, in days |
| `LINEAGE_RETENTION_MIN_AUDIT_AGE_DAYS` | `0` | Reported audit-retention floor, in days. Not enforced. |
| `LINEAGE_AUDIT_ATTESTATION` | `on` | Merkle epoch sealing. Only `off` disables it. |
| `LINEAGE_SEAL_INTERVAL_SECONDS` | `60` | Epoch length. Must be positive. |
| `LINEAGE_SEAL_GRACE_SECONDS` | `5` | Delay before an ended epoch is sealed |
| `LINEAGE_ACTOR_HEADER` | `X-Lineage-Actor` | Header recorded as the actor on audit events |

- Retention: DELETE is refused while the youngest record it would destroy is younger than the
  floor. `0` disables the floor. Nothing deletes audit events, so the audit floor is only
  reported. Both must be non-negative integers. The chart sets both to `3650`; the binary
  defaults to `0`. The values in force are echoed at `/healthz` and `GET /v1/retention`. See
  [Compliance](/governance/compliance/).
- Sealing: the interval is also the bound on the unsealed window. The grace must be `>= 0`
  and less than the interval. Changing the interval on a registry that has already sealed
  makes the sealer refuse to seal and log the refusal every interval until the old value is
  restored. See [Audit](/governance/audit/).
- Actor header: the value is recorded verbatim. It is never used for authorization.

## Tracing and console

These configure OpenTelemetry export and the links the console shows.

| Variable | Default | Meaning |
| --- | --- | --- |
| `LINEAGE_OTLP_ENDPOINT` | `OTEL_EXPORTER_OTLP_ENDPOINT`, else empty | OTLP/HTTP collector. Empty disables tracing. |
| `LINEAGE_SERVICE_NAME` | `lineage` | `service.name` resource attribute |
| `LINEAGE_TRACE_SAMPLE_RATIO` | `1.0` | Parent-based head sampling ratio, `0`–`1` |
| `LINEAGE_PUBLIC_MODEL_API_URL` | empty | Model API URL in the console's usage snippets |
| `LINEAGE_DOCS_URL` | `https://lineage.proseria.ca` | Target of the console's Documentation link |

- A `host:port` OTLP endpoint is plaintext; `http://` or `https://` URLs are used as given.
- The public Model API URL has a trailing `/` trimmed. Empty means the console's own host on
  the Model API port.

## Companion binaries

The client-side binaries read these, not `lineage`. Each takes the Model API base URL from
one variable:

- `lineage-init`: `LINEAGE_ENDPOINT` (default `http://lineage-model-api:8081`).
- `lineage-seed`: `LINEAGE_ENDPOINT` (default `http://localhost:8081`).
- `lineage-cli`: `LINEAGE_SERVER` (default `http://localhost:8081`).

All three send `LINEAGE_ACTOR` as `X-Lineage-Actor`. It defaults to empty, except
`seed@lineage.dev` for `lineage-seed`.

## Minimal production example

This runs Postgres, S3 through IRSA, shared Redis, sealing and a ten-year floor. Put the DSN and any
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
