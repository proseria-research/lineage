---
title: Backup and restore
description: What to back up, in what order to restore it, and why metadata and bytes have to be treated as one system.
sidebar:
  order: 6
---

A Lineage install is two stores that must stay consistent with each other:

1. **Metadata** — the SQLite file or the Postgres database. Small, and the only irreplaceable
   part.
2. **Artifact bytes** — your object store or filesystem root. Large, and probably already
   backed up by whoever runs it.

Metadata without bytes gives you a registry full of dangling pointers. Bytes without metadata
give you a bucket of files nobody can identify. Back up both, and take metadata **after**
bytes.

## Postgres

Standard `pg_dump`, or whatever your managed provider already does.

```bash
pg_dump --format=custom --no-owner \
  "postgres://lineage@db.internal:5432/lineage" > lineage-$(date +%F).dump
```

```bash
pg_restore --clean --if-exists --no-owner \
  -d "postgres://lineage@db.internal:5432/lineage" lineage-2026-08-01.dump
```

Restore into a database whose schema version matches the binary you are going to run. A dump
from an older schema needs the migrator to run afterwards — start the binary and let it, or
run the migrate Job.

## SQLite

Online, while the process runs:

```bash
sqlite3 /data/lineage.db ".backup '/backups/lineage-$(date +%F).db'"
```

Offline, with the process stopped, copy all three files:

```bash
cp /data/lineage.db /data/lineage.db-wal /data/lineage.db-shm /backups/
```

:::caution[Copying only the .db file loses writes]
In WAL mode, recent transactions live in `-wal` until a checkpoint. A copy of `lineage.db`
alone is a copy from some point in the past. Use `.backup`, or stop the process and take all
three files.
:::

## Artifact bytes

Whatever your storage already offers. On S3-compatible backends:

- Enable **object versioning** — it protects against a bad delete, including one from
  [garbage collection](/operate/garbage-collection/).
- Enable **cross-region replication** if your recovery objectives need it.
- Keep the lifecycle policy in step with your retention rules.

On the filesystem backend, the artifact root is an ordinary directory. Snapshot the volume or
rsync it.

## Ordering

**Back up bytes first, metadata second.** Metadata taken after bytes may reference an object
the byte backup missed — recoverable, since you know exactly which artifact is affected. The
other order gives you objects nothing points at, which is silent.

**Restore metadata first, bytes second**, for the same reason in reverse: the registry comes
up knowing what should exist, and you can verify against it.

## Verify a restore

```bash
# Does the registry think its dependencies are healthy?
curl -s localhost:9090/readyz

# Do the counts match what you expect?
curl -s localhost:9090/metrics | grep -E '^lineage_(models|versions|artifacts) '

# Can a production model still resolve, and does the object exist?
curl -s "localhost:8081/v1/models/fraud-detector/resolve?stage=production" \
  | jq -r '.artifacts[].storageUri'
```

Resolve every model at `production` and confirm each `storageUri` exists in storage. That is
the check that catches a half-restored system, and it is worth scripting.

## Retention

The audit trail is append-only and is the record of who changed what. If your retention
requirements outlive your database backups, export the global feed on a schedule:

```bash
curl -s "localhost:8081/v1/audit?pageSize=500" > audit-$(date +%F).json
```

Page through `nextPageToken` and ship the result wherever your other audit data lives.

## Disaster recovery drill

Restore into a scratch namespace and check three things: the registry starts, the production
version of every model resolves, and one artifact downloads and matches its digest. Anything
short of that has not been tested.

## Next

- [Choosing a metadata store](/operate/metadata-store/)
- [Garbage collection](/operate/garbage-collection/)
- [Production checklist](/deploy/production-checklist/)
