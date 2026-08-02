# 18 — Evidence Export

> **Regime: neutral.** Deliberately *not* marked EU. The mechanism is a **profile** — a
> mapping from stored facts to one document structure (§2) — and both shipped profiles happen
> to be EU. A NIST AI RMF or ISO/IEC 42001 profile is a mapping table and a fixture set
> (§11), not a second subsystem, so the doc name must not imply otherwise. Rule in `15.2.1`.
>
> Status: **Proposed**. One call produces the dossier a regulator asks for, from facts `02`–`11`
> already hold — and names every heading it cannot fill. Posture and boundary rules are `15`;
> the classification it carries is `16`; the retention floor it cites is `19`.

## 1. Scope

| In scope | Out of scope |
|---|---|
| Shaping stored facts into a regulation's structure | Asserting a filing is complete or correct |
| Emitting the gap list as first-class data | Filling, estimating, or narrating a gap |
| Making generation audited, deterministic, and self-attesting | Signing, certifying, or submitting |

## 2. Two Profiles

A **profile** is a mapping from stored facts to one regulation's document structure. Both
ship partial, and both say so.

| Profile | Serves | Coverage | Ships |
|---|---|---|---|
| `annex_xii` | GPAI provider → downstream integrator (Art. 53) | near-complete | **first** — the obligation binds now (`15.4`) |
| `annex_iv` | High-risk technical documentation (Art. 11) | 2 of 10 headings | with phase 6 (`15.7`) |

The profile is the extension seam. A third profile is a mapping table and a fixture set, not
a new subsystem.

## 3. `annex-xii` — GPAI to Downstream

Annex XII is substantially a model card, which is why registry coverage is good here:

| Annex XII element | Source |
|---|---|
| General description, intended tasks | `model.description`, `classification.intended_purpose` (`16.7.1`) |
| Release date, distribution methods | `model_version.created_at`, `artifact.uri` schemes |
| Architecture, parameter count | `version_insight`, `layer_block` (`11.3`) |
| Modality / format of inputs and outputs | `version_insight`, `artifact.model_format_*` |
| Versions, integration means | `02.3.2`, resolve contract (`04`) |
| Licence | `DOC` artifact + `label` |
| Training/testing/validation data, provenance | `trained_on` edges (`07.2`), `evaluation` (`11.3.4`) |

The 14-day downstream response deadline is an operational commitment the bundle makes cheap
to meet; **the registry does not track or enforce it.**

## 4. `annex-iv` — High-Risk Technical Documentation

The regulator's form has ten headings. Lineage fills two, half-fills five, and has no idea
about three. This table is the profile.

| # | Section id | Annex IV heading | Status | Source / where it actually lives |
|---|---|---|---|---|
| 1 | `annex_iv_1` | General description, intended purpose, provider, version | ◐ | `model`, `model_version`, `classification.intended_purpose` |
| 2 | `annex_iv_2` | Development process, architecture, compute, third-party pre-trained components | ◐ | `11.3`, `derived_from` + `produced_by` (`07.2`); **compute not held** |
| 3 | `annex_iv_3` | Data — datasets, provenance, labelling, cleaning | ◐ | `trained_on` refs only; **curation methodology not held** |
| 4 | `annex_iv_4` | Human oversight assessment (Art. 14) | ✗ | Not a registry fact |
| 5 | `annex_iv_5` | Validation and testing, metrics, discriminatory-impact testing | ✅ | `evaluation` + `evidence_artifact_id` (`11.3.4`) |
| 6 | `annex_iv_6` | Cybersecurity measures | ✗ | Not a registry fact |
| 7 | `annex_iv_7` | Capabilities, limitations, accuracy per group, unintended outcomes | ◐ | `evaluation` by `split`; narrative in `DOC` artifacts |
| 8 | `annex_iv_8` | Risk management system (Art. 9) | ✗ | Not a registry fact |
| 9 | `annex_iv_9` | Lifecycle changes, harmonised standards applied | ✅ | `audit_event` + stage history (`02.4`) — precisely what the audit log is for |
| 10 | `annex_iv_10` | Post-market monitoring plan (Art. 72) | ◐ | `deployment` gives the served inventory; **the plan is not held** |

**Two held, five partial, three not held is the correct result, not a shortfall.** A bundle
claiming ten of ten from a metadata store would be false.

## 5. Bundle Shape

```json
{
  "bundle": {
    "profile": "annex_iv",
    "schemaVersion": "1",
    "generatedAt": 1785000000000,
    "generatedBy": "compliance@acme.example",
    "registryVersion": "1.2.0",
    "digest": "sha256:9c1f…",
    "retentionFloor": { "auditDays": 3650, "archivedVersionDays": 3650 },
    "auditAttestation": { "enabled": true, "epochsSealed": 8642 }
  },
  "subject": {
    "model": "fraud-detector", "modelId": "01JAB…",
    "version": "1.4.0", "versionId": "01JAV…", "stage": "production"
  },
  "classification": {
    "regime": "eu_ai_act",
    "euSystemRiskClass": "high_annex_iii", "euGpaiTier": "none",
    "intendedPurpose": "Scores card-not-present transactions for manual review.",
    "basis": "Annex III §5(b) — creditworthiness adjacent; counsel review 2026-03-11.",
    "classifiedAt": 1773000000000, "classifiedBy": "risk@acme.example",
    "reviewDueAt": 1804500000000,
    "state": "stale", "staleReasons": ["version_published_since"],
    "source": "declared"
  },
  "sections": [
    {
      "id": "annex_iv_5", "status": "held",
      "title": "Validation and testing, metrics, discriminatory-impact testing",
      "content": {
        "evaluations": [
          { "suite": "fraud-holdout", "metric": "auc", "split": "q2-2026",
            "value": 0.947, "higherIsBetter": true,
            "harness": "evidently", "harnessVersion": "0.4.2",
            "runAt": 1772800000000, "source": "measured",
            "evidenceArtifact": "eval-q2-2026.json" }
        ]
      }
    },
    {
      "id": "annex_iv_3", "status": "partial",
      "title": "Data — datasets, provenance, labelling, cleaning",
      "content": {
        "trainedOn": [ { "ref": "s3://datasets/txn/q2-2026/", "recordedAt": 1772000000000 } ]
      },
      "gaps": [
        { "field": "labellingProcedure", "note": "Not held by the registry. Lineage records the dataset reference, not how it was labelled." },
        { "field": "cleaningMethodology", "note": "Not held by the registry. Lineage records the dataset reference, not its curation." }
      ]
    },
    {
      "id": "annex_iv_8", "status": "not_held_by_registry",
      "title": "Risk management system (Art. 9)",
      "gaps": [
        { "field": "*", "note": "Not held by the registry. The risk management system is a provider process; Lineage records model facts, not process artefacts." }
      ]
    }
  ],
  "gapSummary": {
    "held": 2, "partial": 5, "notHeld": 3,
    "notHeldSections": ["annex_iv_4", "annex_iv_6", "annex_iv_8"],
    "partialSections": ["annex_iv_1", "annex_iv_2", "annex_iv_3", "annex_iv_7", "annex_iv_10"]
  }
}
```

Section `status` is one of `held` · `partial` · `not_held_by_registry`.

**`gapSummary` is the `15.3.2` rule made mechanical.** A consumer gets the to-do list by
reading one object, without diffing the bundle against the regulation text.

## 6. Gap Notes Are Fixed Strings

Every note is a constant in the binary, not generated prose — so the same gap reads
identically in every install and can be matched by a consumer.

| Section | Note |
|---|---|
| `annex_iv_4` | *"Not held by the registry. Human oversight measures are a property of the deploying system."* |
| `annex_iv_6` | *"Not held by the registry. Cybersecurity controls belong to the deploying system and its infrastructure."* |
| `annex_iv_8` | *"Not held by the registry. The risk management system is a provider process; Lineage records model facts, not process artefacts."* |
| `annex_iv_10` | *"Partially held. `deployment` records give the served inventory; the post-market monitoring plan itself is not a registry fact."* |

## 7. The Bundle Is Itself Evidence

- **Audited.** Generating one appends `evidence.generate` — actor, time, profile, subject.
  A read-shaped operation that is audited anyway, because *who exported what and when* is
  exactly the question an auditor asks.
- **Self-attesting.** It carries `generatedAt`, `generatedBy`, `registryVersion`, and a
  `sha256` of its own canonical form, so a filing ties back to a registry state.
- **Deterministic.** Same facts + same profile ⇒ byte-identical bundle. Canonicalization
  follows `11.4.5` — UTF-8, LF, keys sorted byte-wise ascending, shortest round-tripping
  floats — with `bundle.digest` and `bundle.generatedAt` **excluded from the hashed form**: a
  digest cannot cover itself, and a timestamp would defeat reproducibility.
- **Acceptance criterion:** generate twice across a clock change, assert byte equality.

### 7.1 Why the body is not stored

`evidence_bundle` (§8.1) records that a bundle was produced and what it hashed to. The body is
**not** persisted — it is a pure function of facts plus profile, so storing it would create a
second truth that can disagree with the first.

`gap_counts` is the one exception, frozen at generation, so *"we filed with three gaps in
March"* stays answerable after those gaps are filled.

## 8. Data Model

Additive only (`02.7`).

### 8.1 `evidence_bundle` (many per version)

| Column | Type | Notes |
|---|---|---|
| `id` | id | PK |
| `version_id` | id | FK→`model_version.id` ON DELETE CASCADE |
| `profile` | enum | `annex_xii` \| `annex_iv` |
| `digest` | str | `sha256:` of the canonical bundle (§7) |
| `gap_counts` | json | `{held, partial, notHeld}` frozen at generation |
| `generated_at` | ts | |
| `generated_by` | str? | `X-Lineage-Actor` at write |

### 8.2 Indexes

| Index | Purpose |
|---|---|
| (`version_id`, `profile`, `generated_at`) | latest bundle per profile |

## 9. API

Model API (`:8081`, `/v1`), conventions per `03.1`.

```
POST /v1/models/{m}/versions/{v}:evidence      body {profile}, query ?include=docs
GET  /v1/models/{m}/versions/{v}/evidence      generation history
```

### 9.1 Generate

```bash
curl -X POST "$LINEAGE/v1/models/fraud-detector/versions/1.4.0:evidence" \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: compliance@acme.example' \
  -d '{ "profile": "annex_iv" }'
```

→ `201` + the §5 document.

`?include=docs` returns `application/zip` instead: `bundle.json` at the root, referenced
`DOC`/`METRICS` artifacts under `artifacts/`, fetched through `05` signed URLs.

### 9.2 Errors

`03.9` vocabulary, no new codes.

| Situation | Code | HTTP | `details` |
|---|---|---|---|
| Unknown `profile` | `invalid_argument` | 400 | `allowedValues` |
| Version's model has no classification | `failed_precondition` | 409 | `reason: "unclassified"` |
| `include=docs` with an unreachable backend | `failed_precondition` | 409 | `reason: "artifact_unavailable"`, `artifact` |

A bundle for an unclassified model is refused rather than emitted with a null class: the
classification is what makes the rest of the document mean anything.

## 10. Console (`06`)

- **Version detail — Compliance panel** — bundle history with digests and frozen gap counts,
  and a generate action per profile.
- **`gapSummary` renders as a checklist, not a success state.** Three unfilled headings is the
  normal, correct result and must not read as a failure — a red "3 missing" badge would train
  users to treat a truthful bundle as a broken one.
- Each gap links to where the fact actually lives, where that is a place at all.

## 11. Deferred

| Item | When |
|---|---|
| More profiles (NIST AI RMF, ISO/IEC 42001) | If a buyer asks. The seam exists (§2); the mapping work does not. |
| Signed bundles (registry-held key) | Post-v1. The self-digest (§7) covers integrity; signing needs key management Lineage does not have |
| Dataset-side depth for `annex_iv_3` | With `DATASET` artifacts (`11.10`) |
| Model-level (rather than version-level) bundles | If a filing turns out to want the family, not the iteration |
| PDF rendering | Never in the binary. JSON + zip is the contract; rendering is a consumer concern |

## 12. See Also

| For | Doc |
|---|---|
| Boundary rules, coverage map, build order | `15` |
| The classification carried in every bundle | `16` |
| The review state a filing may reference | `17` |
| Retention floor echoed in `bundle.retentionFloor` | `19` |
| Canonical form rules reused for the digest | `11.4.5` |
| `evaluation`, `layer_block`, `version_insight` | `11.3` |
| Signed-URL delivery for `?include=docs` | `05` |
