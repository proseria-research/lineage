---
title: Choosing a metadata store
description: SQLite for development and edge, Postgres for production — what is identical between them, and what is not.
sidebar:
  order: 2
---

Lineage speaks to its database through a `MetadataStore` port with a **per-dialect adapter**
behind it. The logical schema is shared; the SQL and the migrations are not. Each engine gets
the query shape it is actually good at.

| | SQLite | Postgres |
| --- | --- | --- |
| Role | Development, small installs, edge | HA, production |
| Dependencies | None — a file | A Postgres server |
| Replicas | **One.** Single-writer | Many |
| Singleton lock | Serialized writes | `SELECT … FOR UPDATE` |
| Label filtering | Stored, not indexed for containment | JSONB containment push-down |
| Driver | `modernc.org/sqlite`, cgo-free | `jackc/pgx/v5` |

**Core registry behaviour is identical on both.** Publish, transition, resolve, lineage,
audit — same semantics, same invariants, verified by one shared conformance suite that runs
against both engines. Some advanced queries are Postgres-only, and that is the deliberate
line between the tiers.

## SQLite

```bash
LINEAGE_DB_ENGINE=sqlite
LINEAGE_DB_PATH=/data/lineage.db
```

Runs in WAL mode. Zero dependencies, which is the whole point: a laptop, a single-node
install, an air-gapped box, or an edge site all work with a file on a volume.

:::caution[One replica, enforced]
SQLite is single-writer. A multi-replica SQLite install would corrupt or deadlock, so the
Helm chart **rejects it at template time** rather than letting you find out in production.
:::

Back it up by copying the database file plus its `-wal` and `-shm` siblings while the process
is stopped, or with `sqlite3 … ".backup"` while it runs. See
[Backup and restore](/operate/backup-and-restore/).

## Postgres

```bash
LINEAGE_DB_ENGINE=postgres
LINEAGE_DB_PATH='postgres://lineage@db.internal:5432/lineage?sslmode=require'
```

What Postgres buys you:

- **Horizontal scale.** Run as many replicas as you need behind one database.
- **Row-level locking.** The singleton production invariant is enforced with `FOR UPDATE` on
  the model row, so concurrent promotions serialize cleanly.
- **JSONB filtering.** `labels` and `customProperties` are JSONB, and containment queries are
  pushed into the database instead of filtered in the process.
- **Read replicas** for the resolve hot path, when you get there.

Put the DSN in a Secret. The Helm chart takes `database.postgres.dsnSecret` for exactly this
reason — a DSN in a ConfigMap is a password in a ConfigMap.

## Migrations

Both engines use a versioned, forward-only migrator tracked in a `schema_version` table. Each
dialect has its own migration set, because the schemas are not written in the same SQL.

Under Helm, a migrate Job runs `pre-install` and `pre-upgrade` for Postgres
(`migrations.auto`, on by default). SQLite migrates in-process at startup.

Forward-only means there are no down migrations. Roll back by restoring a backup, not by
un-applying schema changes.

## Which to run

Start with SQLite. It is a real registry, not a toy, and it will carry a single team a long
way.

Move to Postgres when any of these becomes true:

- You need more than one replica — for availability, not just throughput.
- You want to query labels or custom properties at scale.
- Your organisation already runs managed Postgres and would rather back that up than a file.

Migrating means exporting through the API and republishing into a fresh install. There is no
in-place engine conversion, so make the call before the registry has a year of history in it
if you can.

## Next

- [Configuration](/operate/configuration/)
- [Backup and restore](/operate/backup-and-restore/)
- [Production checklist](/deploy/production-checklist/)
