---
title: Storage
description: Metadata stores, artifact storage drivers, delivery modes, garbage collection, and backup and restore.
sidebar:
  order: 3
---

Lineage keeps two kinds of state. Metadata (models, versions, artifact records, audit) lives
in a relational store Lineage owns. Artifact bytes live in a storage backend; Lineage records
their URI, digest and size.

## Metadata stores

| | SQLite | Postgres |
| --- | --- | --- |
| `LINEAGE_DB_ENGINE` | `sqlite` (default) | `postgres` |
| `LINEAGE_DB_PATH` | File path | DSN, e.g. `postgres://user:pass@host:5432/lineage?sslmode=require` |
| Driver | `modernc.org/sqlite`, cgo-free | `pgx` |
| Connection settings | WAL journal, `busy_timeout=5000`, foreign keys on, pool of 1 connection | Driver defaults; no pool cap set |
| Writers | One. All writes serialize through the single connection. | Many. Promotions lock the model row (`FOR UPDATE`); artifact writes take `FOR SHARE` on the version row. |
| Label and property filters | Evaluated in Go | Pushed down as JSONB containment (`@>`) |
| Replicas | Exactly 1 (the chart enforces it) | 1 or more |
| Use for | Laptops, edge, small single-node installs | Production and HA |

Registry behaviour is the same on both. `LINEAGE_DB_ENGINE=memory` exists for tests and keeps
nothing across restarts.

### Migrations

- One ordered, forward-only list of schema statements, applied in order and recorded in a
  `schema_version` table.
- Every process runs pending migrations when it opens the store. `lineage migrate` does only
  that and exits; the chart runs it as a pre-install/pre-upgrade Job for Postgres.
- There are no down migrations. Roll back by restoring a backup taken before the upgrade.

### High availability

Lineage connects to one DSN and does not route reads to replicas. Postgres HA (failover,
replication, backups) is your database platform's job. For multi-replica Lineage, also use
Redis for the resolution cache; see [Deploy](/operate/deploy/#production-checklist) for the
state that stays per-pod.

## Artifact storage drivers

One backend is active per install, selected by `LINEAGE_STORAGE_DRIVER`.

| Driver | Stores in | URI Lineage records |
| --- | --- | --- |
| `fs` | A directory (`LINEAGE_STORAGE_ROOT`), usually a PVC | `file://<model>/<version>/<artifact>`, relative to the root |
| `s3` | Any S3-compatible bucket | `s3://<bucket>/<model>/<version>/<artifact>` |
| `oci` | An OCI registry: one manifest per version, one layer per artifact | `oci://<registry>/<prefix>/<model>:<version>#<artifact>` |

There is no GCS or Azure Blob driver. Use an S3-compatible endpoint if your store offers one.

### How bytes move

| Capability | `fs` | `s3` | `oci` |
| --- | --- | --- | --- |
| Signed download URL | No | Yes, SigV4, 15 min | Only if the registry answers a blob GET with a redirect to an absolute URL |
| Signed upload URL | No | Yes, 1 h | No |
| Multipart upload | No | Yes, from 64 MiB | No |
| Download without a signed URL | Stream-through `/content` | Stream-through `/content` | Stream-through `/content` |
| Upload without a signed URL | Stream-through `uploadContent` | n/a | Stream-through `uploadContent` |
| GC sweep | Yes | Yes | Skipped |

Signed URLs are minted per response and never cached. When signing is unsupported or fails,
the artifact's `/content` endpoint streams the bytes through Lineage. Stream-through puts the
bytes on the Model API pod's network and CPU; size pods accordingly for `fs` and `oci`.

### `s3`: AWS, MinIO, R2, Ceph

| Store | Settings |
| --- | --- |
| AWS S3 | `LINEAGE_S3_BUCKET`, `LINEAGE_S3_REGION`. Leave the endpoint empty. |
| MinIO | `LINEAGE_S3_ENDPOINT=http://minio:9000`, `LINEAGE_S3_PATH_STYLE=true`, static keys |
| Cloudflare R2 | `LINEAGE_S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com`, usually `LINEAGE_S3_PATH_STYLE=true`, static keys |
| Ceph RGW | `LINEAGE_S3_ENDPOINT=<rgw url>`, `LINEAGE_S3_PATH_STYLE=true`, static keys |

Region defaults to `us-east-1` and is part of the SigV4 signature, so set it to what the
store expects. Consumers download from signed URLs directly, so the endpoint must be
reachable from them, not only from Lineage.

Credentials: static keys (`LINEAGE_S3_ACCESS_KEY`, `LINEAGE_S3_SECRET_KEY`) take precedence.
Without them, Lineage tries `AWS_*` environment variables, then IRSA web identity, then ECS
container credentials, then the EC2 instance role (IMDSv2). On EKS, annotate the chart's
ServiceAccount with the role ARN and set no keys. The role needs `s3:GetObject`, `s3:PutObject`,
`s3:DeleteObject` (GC and rejected uploads), `s3:ListBucket` (GC) and
`s3:AbortMultipartUpload`.

### `oci`: registries

- `LINEAGE_OCI_REGISTRY` is `host[:port]` only. `LINEAGE_OCI_REPOSITORY` is the prefix Lineage
  writes under.
- Uploads always stream through Lineage. Lineage pushes an OCI artifact, not a runnable image.
- Downloads use a signed URL only when the registry redirects blob GETs to object storage
  with an absolute URL; otherwise they stream through.
- Manifest updates are serialized per `(repository, tag)` inside one process. Two replicas
  finalizing different artifacts of the same version at the same moment can lose a layer.
  Publish each version from one job.
- Blob lifetime belongs to the registry's own retention and GC.

## Garbage collection

The default, `LINEAGE_STORAGE_GC=retain`, never deletes bytes. Deleting an artifact removes
its metadata row and leaves the object in place.

With `sweep`, a background loop in every Lineage process runs every `LINEAGE_GC_INTERVAL`
(first run one interval after startup):

1. List every object under `LINEAGE_GC_PREFIX`.
2. Keep it if it was modified within `LINEAGE_GC_GRACE`.
3. Keep it if any artifact row references exactly its URI.
4. Otherwise delete it.

- It deletes **every** unreferenced object older than the grace under the prefix, whoever
  wrote it. With an empty prefix that is the whole bucket or `fs` root. Enable `sweep` only
  on a bucket or root dedicated to Lineage.
- Lineage writes at `<model>/<version>/<artifact>` with no fixed top-level prefix, so the
  prefix cannot fence off Lineage's objects inside a shared bucket.
- The grace period protects uploads in flight. Keep it longer than the 1 h upload lifetime;
  the default `24h` is.
- `oci` is skipped without error.
- There is no on-demand trigger. Results are logged only when something is deleted:
  `gc: swept N/M unreferenced objects under "<prefix>"`. Errors log `gc: sweep error:` and the
  next interval retries.
- With several replicas each pod sweeps independently. A delete error ends that pod's pass
  until the next interval.

Turn on bucket versioning or a soft-delete policy before enabling `sweep`.

## Backup and restore

Back up both stores. Metadata without bytes points at nothing; bytes without metadata are
unidentifiable.

| Order | Backup | Restore |
| --- | --- | --- |
| 1 | Artifact bytes | Metadata |
| 2 | Metadata | Artifact bytes |

Taking bytes first means every metadata row points at an object the byte backup contains.

### Postgres

```bash
pg_dump --format=custom --no-owner "$DSN" > lineage-$(date +%F).dump
pg_restore --clean --if-exists --no-owner -d "$DSN" lineage-2026-09-01.dump
```

Run a binary at least as new as the dump's schema. An older dump is migrated forward on the
next start.

### SQLite

The database runs in WAL mode, so recent writes live in `lineage.db-wal` until a checkpoint.
The image has no shell or `sqlite3`, so back up from outside the container:

- **Online:** from a pod that mounts the same PVC, `sqlite3 /data/lineage.db ".backup '/backup/lineage.db'"`.
- **Offline:** scale the Deployment to 0 and copy `lineage.db`, `lineage.db-wal` and
  `lineage.db-shm` together, or take a `VolumeSnapshot`.

Copying `lineage.db` alone while the process runs loses recent writes.

### Artifact bytes

Use the backend's own tools: S3 versioning and replication, registry replication for `oci`,
volume snapshots for `fs`.

### Verify a restore

```bash
curl -s http://lineage-ops:9090/readyz
curl -s http://lineage-ops:9090/metrics | grep -E '^lineage_(models|versions|artifacts) '
curl -s http://lineage-model-api:8081/v1/audit:verify          # "ok": true expected
curl -s "http://lineage-model-api:8081/v1/models/<model>/resolve?stage=production"
```

`/v1/audit:verify` recomputes the sealed audit epochs; a restore that altered audit rows
reports `ok: false`. See [Audit](/governance/audit/).

Next: [Configuration](/operate/configuration/) · [Deploy](/operate/deploy/) · [Resolve](/api/resolve/)
