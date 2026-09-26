---
title: Compliance
description: Risk classification, modification review, model risk management and change control plans, as recorded and flagged through the Model API.
sidebar:
  order: 1
---

Lineage records governance assessments made by people, and flags when something has changed
since. It never assigns a class, judges a change, or blocks a publish, promotion or delete on
governance grounds. Every state below is computed on read from stored facts, so it cannot
itself go stale.

:::note
Lineage records facts. It does not make legal determinations. Nothing here is legal advice.
:::

## What gets recorded, per framework

Each framework maps to a set of records Lineage keeps.

| Framework | What Lineage records |
| --- | --- |
| EU AI Act | System risk class, GPAI tier, intended purpose, basis, review date; staleness; Art. 25 modification review of `derived_from` edges |
| SR 26-2 / PRA SS1/23 / OSFI E-23 | Model risk tier, independent validations with conditions and expiry, monitoring check on the production version |
| FDA PCCP | Declared change envelopes with effective windows; conformance of each derivation to the plan in force |
| Any | The [audit log](/governance/audit/), legal hold, retention floor |

ISO/IEC 42001 and NIST AI RMF have no dedicated records. The audit log is general evidence for
them.

All server-owned fields (`classifiedAt`, `classifiedBy`, `verdictAtReview`, `reviewedBy`,
`validatedBy`, `validatedAt`, `declaredBy`, `declaredAt`, `effectiveTo`) are set from the
actor header and server clock. Sending one is `400`. Every write below is audited.

## EU AI Act classification

One row per model per regime. Regimes: `eu_ai_act`, `mrm`.

| Method | Path | Notes |
| --- | --- | --- |
| `PUT` | `/v1/models/{model}/classifications/{regime}` | Replace the assessment whole. Audited as `classification.set` |
| `GET` | `/v1/models/{model}/classifications/{regime}` | `404` when never classified |
| `GET` | `/v1/models/{model}/classifications` | Every regime's row |
| `GET` | `/v1/models?include=classification` | Inventory with each model's EU row |

Fields on the `eu_ai_act` row:

| Field | Values |
| --- | --- |
| `euSystemRiskClass` | `unclassified` (default), `minimal`, `limited`, `high_annex_iii`, `high_annex_i`, `prohibited` |
| `euGpaiTier` | `none` (default), `gpai`, `gpai_systemic` |
| `intendedPurpose` | Required unless the class is `unclassified` (`422`) |
| `basis` | Required for `high_annex_iii` and `high_annex_i` (`422`) |
| `reviewDueAt` | Epoch ms, in the future. Omit for no scheduled review |

```bash
curl -X PUT localhost:8081/v1/models/fraud-detector/classifications/eu_ai_act \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: dpo@example.com' \
  -d '{"euSystemRiskClass":"high_annex_iii","euGpaiTier":"none",
       "intendedPurpose":"Creditworthiness assessment of retail applicants",
       "basis":"Annex III 5(b): credit scoring of natural persons",
       "reviewDueAt":1821536000000}'
```

```json
{
  "modelId": "01K5M2Q0A7...", "regime": "eu_ai_act",
  "euGpaiTier": "none", "euSystemRiskClass": "high_annex_iii",
  "intendedPurpose": "Creditworthiness assessment of retail applicants",
  "basis": "Annex III 5(b): credit scoring of natural persons",
  "classifiedAt": 1790000000000, "classifiedBy": "dpo@example.com",
  "reviewDueAt": 1821536000000, "source": "declared", "state": "current"
}
```

### Staleness

`state` is `unclassified`, `current` or `stale`. `unclassified` (no row, or an explicit
`unclassified` class) is never a kind of `stale`. A `stale` row lists **every** reason that
fired in `staleReasons`, each measured against `classifiedAt`:

| `staleReasons` | Fires when |
| --- | --- |
| `review_due_passed` | `reviewDueAt` is in the past |
| `version_published_since` | A version was published after classification |
| `production_changed_since` | A version entered production (promotion or rollback), or an artifact was added to the production version |
| `derivation_since` | The `derived_from` edge behind an open modification-review item was created after classification |

Metadata edits do not count toward `production_changed_since`.

Re-`PUT` the assessment to reset the anchor, even if the answer is unchanged. Inventory
filters: `euSystemRiskClass`, `euGpaiTier`, `classificationState`.

## Modification review (Art. 25)

A human records whether a derivation of a high-risk or GPAI model is a substantial modification.

A `derived_from` edge on a model classified `high_annex_iii`/`high_annex_i`, or with a GPAI tier
other than `none`, enters the review queue unless its fingerprint verdict is `identical`.
`unknown` is included: an unmeasured change needs human eyes. The verdict is the one from the
[version diff](/api/lineage/#version-diff).

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/v1/reviews?status=open` | Global queue. `status`: `open`, `closed`, or omit for both |
| `POST` | `/v1/models/{model}/versions/{version}/reviews` | Record a decision. `201`, audited as `review.record` |
| `GET` | `/v1/models/{model}/versions/{version}/reviews` | All decisions on a version, newest first |

Each queue item carries the current `verdict`, `candidates`, `missing`, per-level `hashes`,
`basis`, the edge's `declaredMethod`, and the model's class. An item is `closed` once any
review exists for its edge; `review` holds the latest one.

```bash
curl -X POST localhost:8081/v1/models/fraud-detector/versions/1.4.0/reviews \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: counsel@example.com' \
  -d '{"edgeId":"01K5Q8X2N4...","outcome":"not_substantial",
       "note":"Retrained on Q2 data; intended purpose and performance envelope unchanged"}'
```

```json
{
  "id": "01K5R1B6F3...", "versionId": "01K5Q8W7C1...", "edgeId": "01K5Q8X2N4...",
  "verdictAtReview": "reweighted", "outcome": "not_substantial",
  "note": "Retrained on Q2 data; intended purpose and performance envelope unchanged",
  "reviewedBy": "counsel@example.com", "reviewedAt": 1790000500000
}
```

- `outcome`: `not_substantial`, `substantial`, `undetermined`.
- `edgeId` must be a `derived_from` edge leaving this version (`400` otherwise).
- `verdictAtReview` is frozen at write. If a producer later submits a missing hash, the queue
  shows the new `verdict` beside the old `verdictAtReview`.
- Reviews are append-only. A re-review is a new row. Recording one does not require the model
  to be classified.

## Model risk management

Three parts: a tier (the `mrm` classification row), validations (per version, append-only),
and a state computed on read.

### Tier

`PUT /v1/models/{model}/classifications/mrm` with `mrmTier`, `basis`, `reviewDueAt`,
optionally `intendedPurpose`. EU fields on this path are `400`. `mrmTier` values:

- `untiered`: the default. Never read as low risk.
- `tier_1`, `tier_2`, `tier_3`: firm-assigned materiality, highest first. `tier_1` requires
  `basis`.
- `out_of_scope`: declared exclusion from the framework. Requires `basis`.

### Validations

Validators record their outcome per version. History is append-only.

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/v1/models/{model}/versions/{version}/validations` | `201`, audited as `validation.record` |
| `GET` | `/v1/models/{model}/versions/{version}/validations` | History, newest first, plus this version's `state` |
| `POST` | `/v1/models/{model}/versions/{version}/validations/{id}:clearConditions` | Conditional rows only, once (`409` `not_conditional` / `already_cleared`). Audited as `validation.conditions_cleared` |

```bash
curl -X POST localhost:8081/v1/models/fraud-detector/versions/1.4.0/validations \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: mrm-validator@example.com' \
  -d '{"outcome":"conditional","scope":"Card-not-present fraud scoring",
       "findings":"Stable on holdout; drift monitoring not yet wired",
       "conditions":"Weekly drift evaluation recorded against production",
       "validUntil":1821536000000}'
```

| Field | Rule |
| --- | --- |
| `outcome` | `approved`, `conditional`, `rejected`, `undetermined` |
| `conditions` | Required for `conditional` (`422`) |
| `validUntil` | Epoch ms in the future; omit for no expiry |
| `evidenceArtifactId` | An artifact of kind `DOC` or `METRICS` on this version |

The response adds `independenceEvidenced`: `true` when `validatedBy` is set and differs from
the version's `author`. `false` is a flag, not a refusal.

### State

The model-level state (on the `mrm` row) is about the production version, or the newest
version if none is in production; the row names it in `version`. The first match wins:

1. `untiered`: no `mrm` row, or tier `untiered`.
2. `unvalidated`: no validation, or the latest is `rejected` or `undetermined`.
3. `stale`: the latest is `approved`/`conditional` and at least one `staleReasons` entry below
   fires.
4. `current`: otherwise.

| `staleReasons` | Fires when |
| --- | --- |
| `validation_expired` | `validUntil` has passed |
| `version_published_since` | Any version of the model was published after the validation |
| `unmonitored_in_production` | In production, and no evaluation has run since it entered production |
| `conditions_outstanding` | `conditional` and not yet cleared |

Record evaluations with `POST …/evaluations` ([insight](/api/lineage/#version-insight)) to
clear `unmonitored_in_production`. Inventory: `GET /v1/models?include=mrm`, filters `mrmTier`,
`mrmState`.

## Change control plans

A plan is a model's pre-authorised envelope of changes, written in fingerprint verdicts.
Conformance compares each `derived_from` edge against the plan in force when its version was
published.

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/v1/models/{model}/change-plans` | Declare. `201`, audited as `change_plan.declare`, plus `change_plan.supersede` when replacing a plan |
| `GET` | `/v1/models/{model}/change-plans` | All plans, superseded included |
| `GET` | `/v1/models/{model}/versions/{version}/conformance` | One item per `derived_from` edge. `409 no_predecessor` if none |
| `GET` | `/v1/change-plans/conformance?status=outside_plan,undetermined` | Global queue over models with plans |

```bash
curl -X POST localhost:8081/v1/models/fraud-detector/change-plans \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: ra@example.com' \
  -d '{"ref":"K261234","summary":"Retraining on new data; precision changes allowed",
       "allowedVerdicts":["identical","reweighted","recast"],
       "allowedMethods":["fine_tune","quantize"]}'
```

| Field | Rule |
| --- | --- |
| `summary` | Required |
| `allowedVerdicts` | At least one of `identical`, `reweighted`, `recast`, `rescaled`, `rearchitected`. `unknown` is not plannable |
| `allowedMethods` | Omit for unconstrained. An empty list is `400` |
| `protocolArtifactId` | A `DOC` artifact on one of this model's versions |
| `effectiveFrom` | Epoch ms, defaults to now; past values allowed |
| `supersedes` | The open plan this one replaces. Without it, overlapping an open plan is `409 plan_overlap` |

Windows are half-open, `[effectiveFrom, effectiveTo)`, so exactly one plan is in force at any
instant. Each edge gets one `conformance` value:

| `conformance` | Meaning |
| --- | --- |
| `within_plan` | Verdict allowed, and method allowed or unconstrained |
| `outside_plan` | `reasons`: `verdict_not_allowed`, `method_not_allowed`, or both |
| `undetermined` | Verdict is `unknown` (includes edges to external URIs) |
| `uncovered` | The model has plans, but none was in force at publish. Not a violation |
| `no_plan` | The model has no plan. Per-version read only |

`outside_plan` is a fact about the plan, not a finding that a new submission is required.

Next: [Audit log](/governance/audit/) · [Lineage and version insight](/api/lineage/) · [Promotion](/api/promotion/)
