---
title: Production checklist
description: Everything to settle before a Lineage install carries real models.
sidebar:
  order: 2
---

Work through this before the registry becomes something people depend on. Most of it is
one-time.

## Data

- [ ] **Postgres, not SQLite.** SQLite is single-writer and pinned to one replica.
- [ ] **DSN in a Secret**, with `sslmode=require`.
- [ ] **Backups running and tested.** A backup nobody has restored is a hypothesis.
- [ ] **Connection pool sized** against `lineage_db_connections_in_use`.
- [ ] **Migrate Job enabled** (`migrations.auto: true`).

## Storage

- [ ] **S3-compatible backend**, not the filesystem driver. The filesystem driver cannot sign,
      so every byte goes through the registry process.
- [ ] **Dedicated bucket or prefix.**
- [ ] **Object versioning on**, so a bad delete is recoverable.
- [ ] **IRSA or an equivalent role**, rather than static keys.
- [ ] **Garbage collection decided.** Default `retain` is safe; if you enable `sweep`, set
      `LINEAGE_GC_PREFIX` and start with a long grace period.

## Cache

- [ ] **Redis**, if you run more than one replica. With `memory`, a promotion handled by one
      pod does not invalidate another pod's cache except by TTL.
- [ ] **Password in a Secret.**

## Availability

- [ ] **At least three replicas.**
- [ ] **PodDisruptionBudget** with `minAvailable: 2`.
- [ ] **Autoscaling** bounded sensibly.
- [ ] **Resource requests and limits set.**
- [ ] **Probes wired** — `/healthz` to liveness, `/readyz` to readiness.

## Security

- [ ] **NetworkPolicy enabled.**
- [ ] **Authentication in front of both ingresses** — machine identity on `:8081`, SSO on
      `:8080`.
- [ ] **Actor header stripped and overwritten at the edge.** If a client can set
      `X-Lineage-Actor` itself, the audit trail records whatever it claims.
- [ ] **Ops port `:9090` not exposed** outside the cluster.
- [ ] **TLS everywhere**, including to Postgres and Redis.

See [Security model](/operate/security-model/).

## Observability

- [ ] **ServiceMonitor enabled**, with labels matching your Prometheus operator's selector.
- [ ] **PrometheusRule enabled**, thresholds tuned to your own numbers rather than left at the
      defaults.
- [ ] **Logs shipped**, with `request_id` and `traceparent` preserved.
- [ ] **OTLP endpoint set** if you run tracing.
- [ ] **A dashboard** showing: resolve rate and latency, cache hit ratio,
      `lineage_versions_by_stage{stage="production"}`, publish rate, and
      `lineage_digest_mismatch_total`.

## Conventions

- [ ] **A label schema**, agreed and written down. See [Labels and search](/guides/labels-and-search/).
- [ ] **A version naming scheme** per model — semver, dates, or run ids. One of them, not
      three.
- [ ] **Publishers send `Idempotency-Key`**, namespaced by resource.
- [ ] **Every automated client sets an actor.** `ci@…`, `kserve`, `eval-harness` — not blank.
- [ ] **Promotion has a `reason`.** It is the field people read a year later.

## Integration

- [ ] **Consumers resolve by stage**, not by pinned version.
- [ ] **Consumers take every `MODEL` artifact**, not just the first.
- [ ] **Consumers verify digests** after download.
- [ ] **Nothing caches a signed URL.**
- [ ] **Deployments recorded** at rollout, with a useful `externalRef`.

## Before you go live

Run the drill. In a scratch namespace, restore the backup and confirm:

1. The registry starts and `/readyz` passes.
2. Every model resolves at `production`.
3. One artifact downloads and matches its digest.

Then do it once more with the person who will actually be on call.

## Next

- [Deploy with Helm](/deploy/helm/)
- [Observability](/operate/observability/)
- [Backup and restore](/operate/backup-and-restore/)
