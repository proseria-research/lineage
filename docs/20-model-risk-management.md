# 20 — Model Risk Management

> **Regime: financial supervision (US · UK · Canada).** Serves [SR 26-2](https://www.federalreserve.gov/supervisionreg/srletters/SR2602.htm),
> [PRA SS1/23](https://www.bankofengland.co.uk/prudential-regulation/publication/2023/may/model-risk-management-principles-for-banks-ss)
> and [OSFI E-23](https://www.osfi-bsif.gc.ca/en/guidance/guidance-library/guideline-e-23-model-risk-management-2027)
> from one field set, because the three ask for the same three things (§3). The tier field is
> `mrm_*`-prefixed and takes its own `classification` row, per `16.3.1` and `16.3.2`.
>
> Status: **Proposed**. A governed model inventory: a risk tier per model, an independent
> validation record per version, and evidence that production models are still being watched.
> The landscape is `15.2.2`; the profile that exports it is `18`.

## 1. Scope

| In scope | Out of scope |
|---|---|
| A declared **risk tier** per model | Deciding a tier, or computing one (`15.3`) |
| Recording an **independent validation** and its outcome | Performing validation, or judging its quality |
| Flagging models whose validation has gone stale or unmonitored | Blocking a promotion, or downgrading a tier |
| Exporting all of it as an `mrm` bundle (`18`) | The firm's MRM policy, capital impact, or committee minutes |

## 2. Why This One Fits

Every other regime in `15` asks Lineage to describe a model. This one asks for a **governed
inventory of models** — which is what the registry already is. The three pillars map onto
things that mostly exist:

| Pillar | Where it already lives | What is missing |
|---|---|---|
| Model inventory | `model`, `model_version` (`02`) | a risk **tier** |
| Development documented | `11` insights, `07` lineage, `audit_event` | — |
| **Independent validation** | — | the record itself (§5) |
| Ongoing monitoring | `evaluation` (`11.3.4`), `deployment` | a way to notice its **absence** (§7) |

This is also the only regime here with fifteen years of examiner precedent and staffed teams
behind it — the budget exists rather than arriving with a deadline.

## 3. What the Three Regimes Agree On

One field set serves all three because the disagreements are about *who is in scope*, not
*what to record*.

| | SR 26-2 (US) | SS1/23 (UK) | E-23 (Canada) |
|---|---|---|---|
| In force | 17 Apr 2026 | 17 May 2024 | **1 May 2027** |
| Applies to | mostly >$30bn assets | firms with internal model approval | **all** FRFIs |
| Model inventory + risk tiering | ✅ | ✅ | ✅ |
| Independent validation / effective challenge | ✅ | ✅ | ✅ |
| Ongoing monitoring | ✅ | ✅ | ✅ |
| AI/ML explicitly in scope | ⚠️ **genAI and agentic excluded** | yes | yes, technology-neutral |

**The SR 26-2 carve-out is a scope statement, not a governance holiday.** A firm still governs
the models it excludes, under its own framework. Lineage does not guess which side a model
falls on — that is `15.3` again — so scope is **declared**, via an `out_of_scope` tier that
requires a stated basis (§4).

## 4. `mrm_tier` — a Second Regime on the Existing Table

`16.3` promised that a second regime arrives as a prefixed enum column plus its own row on the
same `classification` table, with no renaming and no migration of what is there. This is that
promise being collected: one column, one new `regime` value, `mrm`.

**Its own row, not its own columns on the EU row** (`16.3.2`). The model-risk team tiers a model
on its own schedule, for its own reasons, on its own validation cycle — so `classified_at`,
`classified_by`, `basis` and `review_due_at` are answered separately here, using the same
columns one row down. Sharing the EU row would mean a tiering write in June moving the anchor
that the EU drift check measures against (`16.5`), silently clearing a staleness raised in
March.

| Value | Meaning |
|---|---|
| `untiered` | default. A visible state, never rendered as low risk |
| `tier_1` · `tier_2` · `tier_3` | firm-assigned, `tier_1` highest |
| `out_of_scope` | deliberately outside the firm's MRM framework — **requires `basis`** |

**Why three numbered tiers and not the firm's own labels.** Supervisors require tiering
without mandating names, and every firm has its own. A free-text tier makes the inventory
query useless; three ordered buckets keep it a real query, and the console maps them onto
local names as a display concern. A firm needing a fourth bucket is the signal to revisit
this, not a reason to pre-build it (`00.11.5`).

`basis` carries *why this tier* — the reasoning, not the conclusion — and is required for
`tier_1` and `out_of_scope`. Same rule as `16.6`: the two answers that most need a reason are
the most severe and the one that opts out. It is the shared `16.7.1` column, holding this
regime's reasoning because this is this regime's row — there is no `mrm_basis`.

## 5. `validation` — a Judgement, Not a Measurement

`11` already stores `evaluation`. Validation is a different kind of fact and must not be
folded into it.

| | `evaluation` (`11.3.4`) | `validation` (this doc) |
|---|---|---|
| Is | a measurement | a **judgement** |
| Produced by | a harness | a person or team independent of the developer |
| `source` | `measured` \| `declared` | always `declared` (`11.2`) |
| Answers | "what did it score" | "is it fit to rely on, and who says so" |
| Repeats | per suite/metric/split | per version, append-only |

An `outcome` is one of four. `conditional` exists because it is the common real answer, and a
registry that forced it into approved-or-not would lose the conditions that make it true.

| Outcome | Meaning |
|---|---|
| `approved` | fit for use as scoped |
| `conditional` | fit **subject to `conditions`** — required non-empty |
| `rejected` | not fit for use |
| `undetermined` | reviewed, cannot conclude yet — the `17.5.1` precedent |

Append-only, like `evaluation`: a re-validation is a new row, and the current answer is the
latest row per version. Nothing is ever overwritten, because *what we believed in March* is
the question an examiner asks.

## 6. Independence Is Evidenced, Not Enforced

"Effective challenge" means the validator is not the developer. Lineage holds both names —
`model_version.author` and `validation.validated_by` — so it can say when they match.

```
independenceEvidenced(v) := validation.validated_by IS NOT NULL
                            AND validation.validated_by != model_version.author
```

**It flags, it does not refuse.** A one-person team, a shared service account, or an actor
header that carries a team rather than a person are all legitimate and would all trip it. The
bundle emits `independenceEvidenced: false` with the reason, and a human decides whether that
is a finding — `15.3` applied to a field where the registry genuinely cannot know.

## 7. `mrmState` — Reusing the Drift Machinery

`16.5.1` is precise about what carries over: the **shape**, not the query. This one anchors on
a validation record per **version**, where `16.5` anchors on a classification per **model**, and
the ladder below has four states rather than three. What is identical is the design — a list of
independent triggers, evaluated on read, returning every reason that fired, repairing nothing.
Two of the four triggers are this regime's own.

| State | Meaning |
|---|---|
| `untiered` | no `mrm` classification row, or `mrm_tier = 'untiered'` |
| `unvalidated` | tiered, but no validation row, or the latest is `rejected` |
| `stale` | validated, and at least one trigger fired |
| `current` | tiered, validated, nothing fired |

Computed on read, so it cannot itself go stale. Given the latest validation `val` for version
`v` of model `m`:

```
stale(v) :=  (val.valid_until IS NOT NULL AND val.valid_until < now)          → validation_expired
          OR EXISTS(model_version v2 : v2.model_id = m
                    AND v2.created_at > val.validated_at)                      → version_published_since
          OR (v.stage = 'production'
              AND NOT EXISTS(evaluation e : e.version_id = v.id
                             AND e.run_at > v.stage_changed_at))               → unmonitored_in_production
          OR val.outcome = 'conditional'
             AND val.conditions_cleared_at IS NULL                             → conditions_outstanding
```

Every reason that fired is returned, not the first (`16.5`).

```mermaid
flowchart TB
    val["latest validation<br/>validated_at · valid_until · outcome"]
    val --> a{"valid_until<br/>passed?"}
    val --> b{"newer version<br/>published?"}
    val --> c{"in production with no<br/>evaluation since promotion?"}
    val --> d{"conditional, conditions<br/>not cleared?"}
    a -->|yes| s["<b>stale</b> + reason<br/>listed by filter · shown in console<br/>never auto-corrected"]
    b -->|yes| s
    c -->|yes| s
    d -->|yes| s
```

**Clause 3 is the one worth the trouble.** "A model went to production and nobody has measured
it since" is precisely the ongoing-monitoring failure all three regimes are written to catch,
and it is answerable from rows Lineage already has. It needs `stage_changed_at` on
`model_version` — the one non-additive-looking change here, and it is a nullable column
backfilled from `audit_event`, so it is still a forward-only migration (`02.7`).

## 8. Data Model

Additive only (`02.7`).

### 8.1 `classification` — one new column, one new `regime` value

| Column | Type | Regime | Notes |
|---|---|---|---|
| `mrm_tier` | enum? | **MRM** | `untiered` (default on MRM rows) \| `tier_1` \| `tier_2` \| `tier_3` \| `out_of_scope` |

`regime` gains the value `mrm`; the `16.7.1` CHECK gains a branch — `regime = 'mrm'` requires
`mrm_tier` non-null and the `eu_*` group null. An MRM assessment is a row with
`(model_id, 'mrm')`.

No existing column changes shape. `intended_purpose`, `basis`, `classified_at`, `classified_by`
and `review_due_at` are the shared `16.7.1` columns, answered again on this row — which is why
this regime needs no `mrm_basis` of its own (§4).

### 8.2 `validation` (many per version, append-only)

| Column | Type | Notes |
|---|---|---|
| `id` | id | PK |
| `version_id` | id | FK→`model_version.id` ON DELETE CASCADE |
| `outcome` | enum | `approved` \| `conditional` \| `rejected` \| `undetermined` |
| `scope` | str? | what was validated — "credit decisioning use only" |
| `findings` | str? | what the validator concluded |
| `conditions` | str? | required non-empty when `outcome = 'conditional'` |
| `conditions_cleared_at` | ts? | set by a later write; drives clause 4 (§7) |
| `valid_until` | ts? | null = no expiry, itself surfaced |
| `evidence_artifact_id` | id? | FK→`artifact.id` where `kind` in (`DOC`,`METRICS`) — the report itself |
| `validated_by` | str? | `X-Lineage-Actor` at write; §6 compares it to `model_version.author` |
| `validated_at` | ts | server-set |

### 8.3 `model_version` — one new column

| Column | Type | Notes |
|---|---|---|
| `stage_changed_at` | ts? | when the version last entered its current stage; backfilled from `audit_event` |

### 8.4 Indexes

| Table | Index | Purpose |
|---|---|---|
| `classification` | (`regime`, `mrm_tier`) | the inventory query |
| `validation` | (`version_id`, `validated_at`) | latest-row-per-version |
| `validation` | (`valid_until`) | staleness sweep, clause 1 |
| `evaluation` | (`version_id`, `run_at`) | clause 3 anti-join |

## 9. API

Model API (`:8081`, `/v1`), conventions per `03.1`.

```
GET   /v1/models/{m}/classifications/mrm
PUT   /v1/models/{m}/classifications/mrm         mrmTier + the shared 16.7.1 fields
POST  /v1/models/{m}/versions/{v}/validations
GET   /v1/models/{m}/versions/{v}/validations
GET   /v1/models?mrmTier=&mrmState=
```

`16.8`'s endpoint, with `mrm` in the regime slot. The EU row is written at
`…/classifications/eu_ai_act` and the two never collide — which is what lets both stay
full-replace `PUT`s.

Audit actions: `validation.record`, `validation.conditions_cleared`, appended in the same
transaction (`02.5` invariant 4).

### 9.1 Record a validation

```bash
curl -X POST "$LINEAGE/v1/models/fraud-detector/versions/1.4.0/validations" \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: mrm@acme.example' -d '{
    "outcome": "conditional",
    "scope": "Card-not-present decisioning only. Not validated for merchant onboarding.",
    "findings": "AUC stable across segments; PSI elevated on the travel MCC group.",
    "conditions": "Re-measure PSI on travel MCC monthly; escalate above 0.25.",
    "validUntil": 1830000000000,
    "evidenceArtifactId": "01JAV…"
  }'
```

→ `201`. `validatedBy` and `validatedAt` are **server-set**; supplying either is
`400 invalid_argument`, for the `17.6.1` reason — the value of the field is that the registry
witnessed it.

### 9.2 The inventory query

The question an examiner opens with, in one call:

```
GET /v1/models?mrmTier=tier_1&mrmState=stale
```

```json
{ "items": [
    { "name": "fraud-detector", "owner": "risk-eng",
      "mrm": {
        "tier": "tier_1",
        "basis": "Drives automated card-not-present declines above $500.",
        "state": "stale",
        "staleReasons": ["unmonitored_in_production", "conditions_outstanding"],
        "latestValidation": {
          "outcome": "conditional", "validatedAt": 1773000000000,
          "validatedBy": "mrm@acme.example", "validUntil": 1830000000000,
          "independenceEvidenced": true },
        "source": "declared" } }
  ], "nextPageToken": null }
```

### 9.3 Errors

`03.9` vocabulary, no new codes.

| Situation | Code | HTTP | `details` |
|---|---|---|---|
| `mrmTier` in (`tier_1`,`out_of_scope`) without `basis` | `unprocessable` | 422 | `field: "basis"` |
| `outcome = conditional` without `conditions` | `unprocessable` | 422 | `field: "conditions"` |
| `validUntil` not `> now` | `invalid_argument` | 400 | |
| Client-supplied `validatedBy` / `validatedAt` | `invalid_argument` | 400 | |
| Clearing conditions on a non-`conditional` validation | `failed_precondition` | 409 | `reason: "not_conditional"` |

## 10. The `mrm` Profile (`18`)

A third profile on the `18.2` seam. Five sections; the shape and gap rules are `18.5`
unchanged.

| # | Section id | Covers | Status | Source |
|---|---|---|---|---|
| 1 | `mrm_inventory` | Model identity, owner, tier, basis, intended purpose | ✅ | `model`, `classification` |
| 2 | `mrm_development` | Architecture, lineage, third-party components, change history | ✅ | `11.3`, `07.2`, `audit_event` |
| 3 | `mrm_validation` | Outcome, scope, findings, conditions, independence | ✅ | `validation` (§8.2) |
| 4 | `mrm_monitoring` | Evaluations since promotion, deployments served | ◐ | `evaluation`, `deployment`; **thresholds and the monitoring plan not held** |
| 5 | `mrm_governance` | Committee approval, policy, capital treatment | ✗ | Not a registry fact |

Fixed gap notes (`18.6`):

| Section | Note |
|---|---|
| `mrm_monitoring` | *"Partially held. The registry records measurements taken and deployments served; monitoring thresholds and the monitoring plan are not registry facts."* |
| `mrm_governance` | *"Not held by the registry. Committee approval, MRM policy and capital treatment are firm process artefacts; Lineage records model facts."* |

**Three held, one partial, one not held.** Better coverage than `annex_iv` (`18.4`) because
this regime asks about the model, and Lineage is a model registry.

## 11. Console (`06`)

- **Inventory view** gains a tier column and an `mrmState` filter, beside `16`'s EU columns —
  the same table, a second regime's lens.
- **Stale is shown with its reason**, as in `16.9`. `unmonitored_in_production` is the one
  worth surfacing loudest: it is a live gap, not a paperwork one.
- `untiered` renders as `untiered`, never as `tier_3`. Guessing low is the expensive direction.
- Validation history is a timeline, not a current-value field — the supersession is the point.

## 12. Deferred

| Item | When |
|---|---|
| A fourth tier, or firm-defined tier names | When a real install needs it (§4) |
| Monitoring **thresholds** (PSI limits, drift bounds) | These are a monitoring system's facts; Lineage would be storing someone else's config |
| Model-family (rather than version) validation | If a firm validates at the model level; the table would move, not change shape |
| Automatic scope inference for the SR 26-2 genAI carve-out | Never. `15.3` — the registry does not decide what a regulation covers |
| Committee workflow, sign-off routing | Not a registry concern. The bundle is an input to whatever tool does it |

## 13. See Also

| For | Doc |
|---|---|
| The regime landscape and why this one is the cheapest serious win | `15.2.2`, `15.2.7` |
| The prefixed-column rule this collects on, and the per-regime row | `16.3.1`, `16.3.2` |
| The drift predicate reused here | `16.5` |
| Append-only review precedent (`undetermined`, frozen server-set fields) | `17.5` |
| `evaluation` — the measurement this is not | `11.3.4` |
| Bundle shape, gap rules, determinism | `18.5`–`18.7` |
| Entities, audit invariants, migrations | `02` |
