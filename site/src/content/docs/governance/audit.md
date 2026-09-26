---
title: Audit log
description: The append-only record of every state change, how to read it, how to verify it has not been altered, and how retention and legal hold protect evidence.
sidebar:
  order: 2
---

Every state change writes one audit event **in the same database transaction as the change**.
If the change committed, its event exists; if the event cannot be written, the change rolls
back and the request fails. There is no asynchronous pipeline to fall behind.

Events are append-only. No API edits or deletes one, and they outlive the model or version
they describe.

## Event shape

```json
{
  "id": "01K5Q9D3M8...",
  "at": 1790000000000,
  "actor": "release-bot@example.com",
  "action": "version.stage_changed",
  "subjectType": "model_version",
  "subjectId": "01K5Q8W7C1...",
  "summary": "fraud-detector@1.4.0 → production",
  "data": { "from": "staging", "to": "production", "reason": "shadow eval green for 48h" },
  "epoch": 29833333
}
```

| Field | Meaning |
| --- | --- |
| `id` | ULID; sorts by creation time |
| `at` | Epoch milliseconds |
| `actor` | Value of the identity header on the request; absent if none was sent |
| `action` | `subject.verb`, below |
| `subjectType` / `subjectId` | `model`, `model_version`, `artifact` or `deployment`, and its ID |
| `summary` | Human-readable line |
| `data` | Action-specific structured detail, when any |
| `epoch` | Sealing window. Absent means the row is not attested |

A promotion writes one `version.stage_changed` event for the promoted version. The demoted
incumbent gets no event of its own.

## Actions

`action` names the change as `subject.verb`, grouped here by subject type.

| Subject | Actions |
| --- | --- |
| `model` | `model.create`, `model.update` (includes archive and unarchive), `model.delete`, `classification.set`, `change_plan.declare`, `change_plan.supersede`, `hold.set`, `hold.release` |
| `model_version` | `version.create`, `version.update`, `version.stage_changed`, `version.delete`, `lineage.add`, `lineage.delete`, `insight.update`, `insight.replace`, `evaluation.create`, `footprint.update`, `review.record`, `validation.record`, `validation.conditions_cleared`, `hold.set`, `hold.release` |
| `artifact` | `artifact.register`, `artifact.upload`, `artifact.update`, `artifact.delete` |
| `deployment` | `deployment.create`, `deployment.update`, `deployment.delete` |

Governance actions carry their decision in `data` (for example `classification.set` records
the regime and class; `review.record` the outcome and frozen verdict; `hold.*` the reason).

## Actor attribution

Lineage does not authenticate. Your ingress, gateway or mesh does, then passes the identity in
a header: `X-Lineage-Actor` by default, renamed with `LINEAGE_ACTOR_HEADER`. Lineage records
the value verbatim.

:::caution
The header is trusted. Your perimeter must overwrite it on every inbound request, or a client
can write any name into the audit log.
:::

## Reading the log

Two feeds, both paginated and pinnable to a point in time.

| Method | Path | Returns |
| --- | --- | --- |
| `GET` | `/v1/audit` | All events. Filters: `subjectType`, `subjectId` |
| `GET` | `/v1/models/{model}/audit` | Events whose subject is the model itself |

Both are newest first, cursor-paginated (`pageSize` default 50, max 500; follow
`nextPageToken` until empty) and accept `asOf`: an epoch-ms instant that returns only events
with `at <= asOf`, so a report pinned to a date reads the same rows later.

The per-model feed does **not** include version, artifact or deployment events. For a
version's history, filter the global feed by its ID:

```bash
curl -s "localhost:8081/v1/audit?subjectType=model_version&subjectId=01K5Q8W7C1...&asOf=1790000000000"
```

There is no export endpoint. To archive the log elsewhere, page through `/v1/audit`.

## Integrity: sealed epochs

Each event is stamped at write with an epoch: `at` divided by the interval in milliseconds, rounded down. A background sealer
computes a Merkle root (RFC 6962 hashing) over each closed epoch's events and links it to the
previous sealed root. Empty epochs are skipped, so gaps in epoch numbers are normal.

For example, epoch `29833331` is sealed with `root₁`, the Merkle root of its events. Epoch
`29833332` had no events and was skipped. Epoch `29833333` is sealed with `root₂`, recorded
with `prevRoot = root₁`. Epoch `29833334` is open: it is not sealed until the interval plus
grace has passed.

Because every root carries the one before it, changing any sealed event changes its epoch's
root and breaks every link after it.

Each leaf hashes the whole row: `id`, `at`, `actor`, `action`, `subjectType`, `subjectId`,
`summary` and `data`. Rewording a summary breaks the root.

| Setting | Env var | Default |
| --- | --- | --- |
| Enabled | `LINEAGE_AUDIT_ATTESTATION` (`off` disables) | on |
| Interval | `LINEAGE_SEAL_INTERVAL_SECONDS` | `60` |
| Grace | `LINEAGE_SEAL_GRACE_SECONDS` | `5`; must be shorter than the interval |

- Rows in the open epoch are not yet protected. The exposure is bounded by the interval.
- Enabling attestation does not backfill: rows written while it was off stay unattested.
- Changing the interval after seals exist stops sealing (logged each interval) until it is
  restored, since the new numbering could collide with sealed epochs.

### Verify

`GET /v1/audit:verify` recomputes every sealed epoch and checks the chain. Bound it with
`fromEpoch` and `toEpoch`. A detected break is `200` with `ok: false`, not an error status.
With attestation disabled the endpoint returns `409 attestation_disabled`.

```json
{
  "ok": false,
  "attestationStartedAt": 1789999800000,
  "epochsChecked": 3,
  "leavesChecked": 41,
  "openEpochSince": 1790000040000,
  "firstBreak": { "epoch": 29833333, "kind": "leaf_count_mismatch",
                  "expected": 17, "found": 16, "sealedAt": 1790000065000 }
}
```

`firstBreak.kind` is `leaf_count_mismatch` when a row in the epoch was deleted (or added),
`root_mismatch` when a row was edited, and `prev_root_mismatch` when a sealed epoch was removed
wholesale.

### Inclusion proof

`GET /v1/audit/{id}:proof` returns `leafHash`, `leafIndex`, `leafCount`, sibling `path` and
the epoch `root`, so a third party can check one event without the rest of the log.
`409` with `details.reason` `not_attested` (row predates attestation) or `epoch_unsealed`
(retry after `details.sealsAt`).

## Retention floor

`GET /v1/retention` (also echoed at `/healthz` on the ops port) returns the floor the process is
running under.

```json
{ "minAuditAgeDays": 3650, "minArchivedVersionDays": 3650 }
```

- `minArchivedVersionDays` (`LINEAGE_RETENTION_MIN_ARCHIVED_VERSION_DAYS`): `DELETE` of a
  model or version is refused while the youngest record it would destroy is younger than this.
  For a model, that includes every version in the cascade.
- `minAuditAgeDays` (`LINEAGE_RETENTION_MIN_AUDIT_AGE_DAYS`): reported only. Nothing deletes
  audit events.

The binary defaults both to `0` (disabled). The Helm chart sets `3650`. Nothing is ever deleted
on a timer; the floor only refuses deletes.

## Legal hold

A hold freezes a model or version against deletion, for litigation or a regulator inquiry.

| Method | Path |
| --- | --- |
| `POST` | `/v1/models/{model}:hold` · `/v1/models/{model}:release` |
| `POST` | `/v1/models/{model}/versions/{version}:hold` · `…/{version}:release` |

```bash
curl -X POST localhost:8081/v1/models/fraud-detector:hold \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: legal@example.com' \
  -d '{"reason":"Regulator inquiry REF-2026-118"}'
```

The response is the model or version, now with `legalHold: {"heldSince": …, "heldBy": …}`.

- `reason` is required in both directions. It goes on the `hold.set` / `hold.release` event,
  not the row.
- Holding a held subject is `409 already_held` (`heldSince` is never refreshed). Releasing an
  unheld one is `409 not_held`.
- A model hold covers its versions. A held version blocks deleting its model.

**A hold blocks** `DELETE` of the held model or version, and of anything whose delete would
cascade into it. `?force=true` does not override it; holds are checked before the production
guard. **A hold does not block** publishing, metadata edits, promotion, lineage or governance
writes, or artifact changes (those follow the version lock, see [Publishing](/api/publishing/)).

A refused delete:

```json
{
  "type": "https://lineage.dev/errors/failed_precondition",
  "title": "failed_precondition", "status": 409, "code": "failed_precondition",
  "detail": "ancestor 'model/fraud-detector' is under legal hold; delete refused",
  "details": { "reason": "legal_hold", "heldSince": 1790000000000,
               "heldBy": "legal@example.com", "heldSubject": "model/fraud-detector" }
}
```

A floor refusal has `details.reason: "retention_floor"` with `floorDays` and `ageDays`. When
both apply, the hold is reported.

Next: [Compliance](/governance/compliance/) · [Configuration](/operate/configuration/) · [Conventions](/api/conventions/)
