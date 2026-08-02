# 19 — Retention & Legal Hold

> **Regime: neutral.** Deliberately *not* marked EU. Legal hold and retention floors are
> jurisdiction-neutral — the term comes from US litigation practice, and every framework that
> asks for records asks for them to still exist. Only the **default values** (3650 days, §4)
> cite Art. 18, and those are config, not schema. Rule in `15.4.3`.
>
> Status: **Proposed**. Both decisions resolved — `00.11.13` ✅ deletion **refuses**;
> `00.11.14` ✅ tamper-evidence by **Merkle epoch sealing**, on by default. Makes evidence
> survive: deletion refuses where the law requires retention, and the audit log proves it was
> not rewritten. Posture is `15`; the bundle that cites the floor is `18`.

## 1. Scope

| In scope | Out of scope |
|---|---|
| Refusing deletions that would destroy retained evidence | Advising what retention period applies |
| Reporting the configured floor so a filing can cite it | Automatic expiry or deletion of anything |
| Tamper-evidence over `audit_event` (§5), on by default | Art. 12/19 runtime inference logs (`15.3.3`) |

**Nothing here deletes anything.** Every mechanism is a refusal or an attestation. A registry
that garbage-collected on a retention timer would be a liability, not a feature.

## 2. What Is Missing Today

Art. 18 requires documentation kept ten years past withdrawal; Art. 19 requires provider-held
logs for at least six months. `02.5` invariant 5 already retains `audit_event` past subject
deletion — the spirit is there. Three gaps:

| Gap | Addition |
|---|---|
| Deletion can destroy evidence | `legal_hold` on `model` / `model_version` (§3) |
| No stated floor | Config keys (§4), reported at `/healthz` and in every bundle |
| No tamper evidence | Merkle epoch sealing (§5) |

## 3. Legal Hold (`00.11.13` ✅)

A hold marks a subject as evidence in an active matter. It is set by a human and it blocks
exactly one thing.

### 3.1 Semantics

- **Explicit both ways.** `hold.set` and `hold.release` are separate audited actions.
  Clearing a hold is the event an auditor cares about, so it is never implicit and never a
  side effect of another operation.
- **`DELETE` on a held subject ⇒ `409 failed_precondition`** with
  `details: { reason: "legal_hold", heldSince, heldBy }`.
- **Transitive down.** A hold on a `model` covers its versions; deleting a version under a
  held model is refused with the model named in `details.heldBy`.
- **Blocks destruction only.** Metadata `PATCH`, stage transitions, and archival all still
  work. A held model keeps moving through its lifecycle — a hold is not a freeze.
- **Independent of `state`.** `model.state = ARCHIVED` is lifecycle; `legal_hold` is
  evidence. Either can be set without the other.

```mermaid
flowchart TB
    d["DELETE /v1/models/{m}"] --> h{"legal_hold<br/>on model or ancestor?"}
    h -->|yes| r1["<b>409</b> reason: legal_hold<br/>heldSince · heldBy"]
    h -->|no| f{"younger than<br/>retention floor? (§4)"}
    f -->|yes| r2["<b>409</b> reason: retention_floor<br/>floorDays · ageDays"]
    f -->|no| ok["delete, cascade per 02.5"]
```

### 3.2 Why refusal rather than soft-delete

A soft-delete hides the row and satisfies the caller. Six months later nobody can tell whether
the record was retained on purpose or merely not yet purged. A `409` puts the decision in
front of a human at the moment it matters, and the refusal itself is not audited as a change
because nothing changed — the attempt is visible in access logs (`09`), not the audit trail.

## 4. Retention Floor (`00.11.13` ✅)

```yaml
compliance:
  retention:
    minAuditAgeDays: 3650          # Art. 18 — 10 years. 0 disables the floor.
    minArchivedVersionDays: 3650
  auditAttestation:
    enabled: true                  # §5.2 — on by default; costs nothing on the write path
    sealIntervalSeconds: 60        # §5.3 — also the unsealed-window bound
    sealGraceSeconds: 5
```

- A `DELETE` targeting a subject younger than the floor is refused exactly like a hold, with
  `details.reason = "retention_floor"`, `floorDays`, `ageDays`.
- `/healthz` and **every bundle** echo the configured values (`18.5` →
  `bundle.retentionFloor`), so a filing can cite the floor the registry was actually running
  under rather than the one someone believes was configured.
- `0` disables a floor. It is a real choice for a dev install and must not be confused with an
  unset value — the chart's default is `3650`, not empty.

## 5. Tamper-Evident Audit — Merkle Epoch Sealing (`00.11.14` ✅)

**The write path takes no coordination.** A per-row hash chain would have — each row needing
the previous row's hash forces a total order and serializes every audit write behind one
sequence. That was the whole objection, and it is an artefact of the algorithm, not of the
goal.

Sealing in **epochs** removes it. Rows are written concurrently and unordered; a background
sealer periodically computes one Merkle root over a closed window. This is the Certificate
Transparency construction (RFC 6962), applied to a much smaller log.

```mermaid
flowchart TB
    subgraph w["write path — no coordination"]
        r1["audit_event<br/>epoch=E"] & r2["audit_event<br/>epoch=E"] & r3["audit_event<br/>epoch=E"]
    end
    w --> s["sealer — background, after grace<br/>sort by id · Merkle root"]
    s --> ae["<b>audit_epoch</b><br/>epoch · root · count · prev_root"]
    ae -->|"prev_root"| ae2["audit_epoch E+1"]
    ae --> v["GET /v1/audit:verify<br/>recompute · compare · inclusion proof"]
```

### 5.1 Construction (normative)

**Epoch assignment**, at write, from the server clock — no read of any other row:

```
epoch = floor(at / sealIntervalMs)
```

**Leaf and node hashing**, domain-separated per RFC 6962 so a leaf can never be forged as an
internal node:

```
leaf(row)      = sha256( 0x00 ‖ canonical(row) )
node(l, r)     = sha256( 0x01 ‖ l ‖ r )
canonical(row) = at ‖ actor ‖ action ‖ subject_type ‖ subject_id ‖ data
```

Fields joined with `\x00` and canonicalized per `11.4.5`. Leaves are ordered by `audit_event.id`
byte-wise ascending — ULIDs are already sortable by creation time (`02.1`), so the order is
deterministic without a sequence. An odd node is promoted unchanged to the next level.

**Epoch chaining.** Each `audit_epoch` row carries `prev_root`, so the epochs form the chain
the rows no longer need to. There are ~525k epochs per decade at a 60s interval, not millions
of rows — chaining at that granularity is free.

### 5.2 Why this can now default **on**

| | Per-row chain | Merkle epochs |
|---|---|---|
| Write path | Serialized behind a per-install sequence | **One derived integer column. No coordination.** |
| Postgres under `13` load | A real contention point | No effect |
| Cost location | Every write, synchronously | A background job, once per interval |
| Detects row edit | ✅ | ✅ |
| Detects row deletion | ✅ | ✅ — leaf count and root both change |
| Single-row proof | Walk the whole chain | `O(log n)` inclusion proof |

The reason the chain was opt-in was its write cost. That cost is gone, so **`auditAttestation`
defaults on** — which is what axiom 7 ("auditable by default") should have meant all along.

### 5.3 The honest gap: the open epoch

**Rows in the currently-open epoch are not yet protected.** A tamper inside the sealing window
is undetectable, because there is no root to disagree with yet.

This is real and bounded by `sealIntervalSeconds` (default 60). It is the price of removing
write-path coordination, and it is the correct trade: an attacker who can write to the
database can also do so faster than any interval, so the property that matters is *durable
history is provably intact*, not *the last second is*. `:verify` reports `openEpochSince` so
the unsealed window is explicit rather than assumed.

The sealer waits `sealGraceSeconds` (default 5) past an epoch's end before sealing it, so a
transaction that began inside the window commits before its epoch closes.

### 5.4 Enabling it later

Attestation enabled after the fact **starts at the current epoch**. It does not backfill — a
backfilled root proves nothing, since whoever could rewrite history could recompute the root
over the rewrite.

`:verify` reports `attestationStartedAt`, so the covered window is explicit rather than
implied by the table's existence.

## 6. Data Model

Additive columns on existing tables; no table changes shape (`02.7`).

| Table | Column | Notes |
|---|---|---|
| `model` | `legal_hold` bool | default false (§3) |
| `model_version` | `legal_hold` bool | default false |
| `audit_event` | `epoch` int64? | `floor(at / sealIntervalMs)`, set at write. Null only for rows predating §5 |

### 6.1 `audit_epoch` (one row per sealed window)

| Column | Type | Notes |
|---|---|---|
| `epoch` | int64 | **PK** — the window index (§5.1) |
| `root` | str | `sha256:` Merkle root over the epoch's leaves |
| `prev_root` | str? | previous epoch's `root`; null at `attestationStartedAt` |
| `leaf_count` | int64 | rows sealed — deletion changes this as well as the root |
| `sealed_at` | ts | |

Append-only and never updated. A sealed epoch is immutable by construction: re-sealing would
be indistinguishable from tampering.

### 6.2 Indexes

| Index | Purpose |
|---|---|
| `audit_event` (`epoch`) | the sealer's window scan; inclusion-proof lookup |

`audit_epoch` needs none beyond its PK — it is scanned in `epoch` order or hit by key.

Hold state does not need an index either: it is read on the delete path by primary key, never
scanned.

## 7. API

Model API (`:8081`, `/v1`), conventions per `03.1`.

```
POST /v1/models/{m}:hold          ·  POST /v1/models/{m}:release
POST /v1/models/{m}/versions/{v}:hold  ·  …:release
GET  /v1/audit:verify
```

Audit actions: `hold.set`, `hold.release` (`02.5` invariant 4).

### 7.1 Set a hold

```bash
curl -X POST "$LINEAGE/v1/models/fraud-detector:hold" \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: counsel@acme.example' \
  -d '{ "reason": "Regulator inquiry REF-2026-118" }'
```

`reason` is recorded on the audit event, not on the row — the row carries only the boolean and
its provenance, and the *why* belongs in the trail with everything else.

### 7.2 Verify

```
GET /v1/audit:verify[?fromEpoch=&toEpoch=]
```

Recomputes each sealed epoch's root from its rows and compares, then checks `prev_root`
linkage between epochs.

```json
{ "ok": true,
  "attestationStartedAt": 1780000000000,
  "epochsChecked": 8642,
  "leavesChecked": 48211,
  "openEpochSince": 1785000000000,
  "firstBreak": null }
```

A break names the epoch and what disagreed — `root_mismatch` (a row was edited),
`leaf_count_mismatch` (a row was deleted), or `prev_root_mismatch` (an epoch was removed
wholesale):

```json
{ "ok": false,
  "firstBreak": { "epoch": 29738, "kind": "leaf_count_mismatch",
                  "expected": 17, "found": 16, "sealedAt": 1784200000000 } }
```

### 7.3 Prove a single row

```
GET /v1/audit/{id}:proof
```

Returns the `O(log n)` inclusion path — the sibling hashes from leaf to root, plus the epoch's
sealed `root`. Lets a third party verify one event without reading the log, which is the thing
a per-row chain could not do cheaply.

### 7.4 Errors

`03.9` vocabulary, no new codes — `details.reason` carries the specificity, which keeps that
error table stable.

| Situation | Code | HTTP | `details` |
|---|---|---|---|
| `DELETE` on a held subject | `failed_precondition` | 409 | `reason: "legal_hold"`, `heldSince`, `heldBy` |
| `DELETE` inside the retention floor | `failed_precondition` | 409 | `reason: "retention_floor"`, `floorDays`, `ageDays` |
| `:release` on a subject not held | `failed_precondition` | 409 | `reason: "not_held"` |
| `:verify` / `:proof` when attestation is disabled | `failed_precondition` | 409 | `reason: "attestation_disabled"` |
| `:proof` for a row in the open epoch | `failed_precondition` | 409 | `reason: "epoch_unsealed"`, `sealsAt` (§5.3) |

## 8. Console (`06`)

- A held subject shows a hold marker with `heldSince` and `heldBy` on its detail page, and the
  delete action is disabled with the reason inline — not hidden.
- Attestation status (`enabled`, `attestationStartedAt`, `openEpochSince`, last verify result) belongs on the ops/health
  view (`09`), not on a model page. It is a property of the install.

## 9. Deferred

| Item | When |
|---|---|
| Hold expiry dates | Post-v1. An expiring hold is a scheduler, and a hold that lapses silently is worse than one someone has to clear |
| Per-model retention overrides | If an install needs two floors. One configured floor is the honest v1 |
| External anchoring of an epoch root (timestamping authority, or a public log) | Post-v1, with signed bundles (`18.11`). The epoch root is already the right thing to anchor — one hash per interval, not per row |
| Retention floor on artifacts in the backend | `05.8` GC already refuses to touch objects it did not write; a floor there is a storage-driver concern |

## 10. See Also

| For | Doc |
|---|---|
| Posture, boundary, build order | `15` |
| `bundle.retentionFloor`, `auditAttestation` | `18.5` |
| Audit invariants, cascade rules | `02.5` |
| Error codes, `details` conventions | `03.9` |
| Chart values, config surface | `08` |
| Write-path cost of the chain | `13` |
| Artifact GC and backend deletion | `05` |
