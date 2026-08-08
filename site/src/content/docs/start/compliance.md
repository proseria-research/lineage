---
title: Compliance & evidence
description: How Lineage supports the EU AI Act, ISO/IEC 42001, NIST AI RMF and supervisory model risk guidance — what it records today, what is on the roadmap, and where its responsibilities end.
sidebar:
  order: 5
---

Regulators, auditors and enterprise customers increasingly expect a documented record for
every model an organisation puts into production. In most teams that record is assembled by
hand, because the underlying facts are spread across a registry, a wiki and a spreadsheet.

Lineage holds most of those facts already. This page sets out which ones, how they map to the
major frameworks, what is available today, and what is still in design.

:::note[Scope]
Lineage provides the evidence that supports a compliance programme. It does not make
regulatory determinations: it will not classify a model's risk level for you, judge whether a
change is substantial in law, or certify that a filing is complete. Those are assessments
about a deployed system and its context, made with information a metadata registry does not
hold.

Nothing on this page is legal advice, and determining which frameworks apply to your
organisation remains your responsibility.
:::

## The facts every framework asks for

Frameworks differ in structure and vocabulary, but a registry-shaped obligation consistently
comes down to the same set of questions.

| The question | Where Lineage answers it |
| --- | --- |
| What models and versions exist? | The registry — see [core concepts](/start/concepts/) |
| What is serving production right now? | Lifecycle stages, with a single production version guaranteed per model |
| Who changed it, when, and on what basis? | The [audit trail](/guides/audit-trail/) |
| What produced it? | Typed [lineage edges](/guides/lineage-graph/) — `trained_on`, `derived_from`, `produced_by` |
| What changed between two versions? | The [architecture fingerprint and diff](/guides/version-diff/) |
| How was it evaluated? | Recorded evaluations, with suite, metric, split and harness |
| Where has it been deployed? | Deployment records covering the served inventory |

Only the presentation is specific to a framework. That is the central design decision behind
this work: because the evidence itself is framework-neutral, supporting an additional standard
is a mapping exercise rather than a new subsystem.

## Available today

Everything in the table above is implemented and running. Three properties of the record are
worth calling out, because they are what make it dependable under scrutiny.

**Events are written transactionally.** Every audit event is committed in the same transaction
as the change it describes. If a change committed, its event exists. There is no asynchronous
pipeline that can fall behind and no buffer that can drop entries.

**Events are append-only.** Audit records are never modified or deleted, and they are retained
beyond the removal of the model or version they describe. The history remains intact.

**Attribution comes from your identity infrastructure.** Lineage does not authenticate users.
Your ingress, gateway or service mesh does, and passes an identity through the
`X-Lineage-Actor` header, which Lineage records against every change. Because that header is
trusted, your perimeter must overwrite it on every inbound request — see the [security
model](/operate/security-model/).

## On the roadmap

The remaining capabilities are fully specified, and the specifications are published in the
repository alongside the code. The **Tier** column says which are part of the free product.

| Capability | What it provides | Tier | Status |
| --- | --- | --- | --- |
| **Risk classification** | A recorded classification per model and per framework, with the reasoning, the author and the review date attached — and automatic detection when a retrain, a promotion or a lapsed review date leaves it out of date | Free | **Available** |
| **Retention and legal hold** | Holds on models and versions involved in an active matter, a configurable retention floor, and cryptographic assurance that the audit trail has not been altered | Free | **Available** |
| **Modification review** | Review routing for significant derivations, presenting the measured architectural change alongside the intent your team declared | Free | In design |
| **Model risk records** | Model risk tiers, independent validation records, and detection of a tier-1 model that has gone unmonitored in production | Free | In design |
| **Change control plans** | Pre-declared change envelopes, and conformance of what actually shipped against them | Free | In design |
| **Evidence export** | Documentation generated in a framework's own structure — EU Annex IV and XII, ISO/IEC 42001, NIST AI RMF, supervisory model risk, FDA change control — with any section the registry cannot supply explicitly identified | Commercial | In design |

If you are evaluating Lineage against a compliance deadline, please plan on the basis of this
table. The registry, its audit record, risk classification, and retention and legal hold are
production-ready; the rest is specified and not yet built.

Note the split: **recording and detecting** are free at every level, including the drift
detection that is the hard part. What the commercial tier adds is the rendering step — turning
those records into a named framework's document. The facts themselves are always reachable
through the public API.

## Two principles behind the design

These govern how the export layer will behave, and they are the reason it is being designed
carefully rather than quickly.

### Gaps are identified, never filled

Where a framework asks for something the registry does not hold, the export will say so
explicitly rather than leave the section blank. A blank section reads as complete; an
identified gap is an actionable item.

The EU AI Act's high-risk technical documentation, for example, has ten headings. Lineage will
supply two in full, contribute partially to five, and hold nothing relevant to three — human
oversight measures, cybersecurity controls and the provider's risk management system are
organisational processes rather than model facts. That result is the accurate one. An export
claiming complete coverage from a metadata registry would not be, and the organisation filing
it would carry the consequences.

### A model is not an AI system

Most frameworks regulate AI *systems* — deployed, in a context, with a defined purpose — while
Lineage registers *models*. A single system may embed several models, and a single model may
serve several systems.

System-level facts are therefore stored as assessments made by a named person on a given date,
never as inferences drawn by the registry. A recorded classification states who assigned it and
why. Where no assessment has been made, that is shown as an explicit unclassified state rather
than defaulting to a low-risk value.

## Where Lineage's responsibilities end

Being specific about this is more useful than a longer list of claims.

| Out of scope | Reason |
| --- | --- |
| Determining a risk classification, or asserting that a modification is substantial | Legal assessments of a system, requiring context the registry does not hold |
| Conformity assessment, CE marking, or regulatory submissions | Filing processes. An export is an input to them, not a replacement |
| Runtime inference logging | A separate record answering a separate question. The audit trail covers changes to the registry, not the behaviour of a deployed system |
| Risk management, human oversight and cybersecurity documentation | Organisational processes, identified as gaps rather than filled |
| Advising on retention periods | Lineage will enforce and report the floor you configure; setting it is your decision |
| Registration filings and end-user disclosure | The output is a submission to an authority or a notice in an interface, neither of which is a registry concern |

## The regulatory landscape

Reviewed August 2026. This area moves quickly — treat the categories as stable and verify any
date before relying on it.

| Framework | Type of obligation | Fit |
| --- | --- | --- |
| **EU AI Act** — general-purpose models, Art. 53 | Technical documentation | Strong. In force since August 2025 |
| **EU AI Act** — high-risk systems, Art. 11 | Technical documentation | Substantial, with gaps identified. Applies from December 2027 |
| **ISO/IEC 42001** | Management system | Strong — the audit trail is direct evidence that a process was followed. The certification customers most often ask for |
| **NIST AI RMF** | Management system | Strong. Voluntary, but widely referenced in US procurement |
| **SR 26-2, PRA SS1/23, OSFI E-23** | Model risk management | Strong — a governed model inventory is what a registry provides natively |
| **FDA PCCP, UNECE R156** | Change control | Strong — a pre-declared change envelope assessed against measured version differences |
| **China CAC** | Algorithm registration | Limited. The output is a submission to a state body, and is not planned |
| **US state disclosure, EU Art. 50** | End-user disclosure | Not applicable. A runtime and interface concern |

The EU AI Act is being addressed first: it is the most prescriptive of the frameworks, an
obligation is already in force, and supporting it exercises the widest range of what the
registry holds.

## Open core

Lineage is open core. The boundary runs between holding a fact and shaping it into a specific
regulator's document: **the record is free, the filing is the product.**

Recording classifications, detecting when one has gone out of date, the audit trail and its
integrity guarantees, retention and legal hold, modification review, and model risk records
are all part of the free, Apache-2.0 product. Every regulatory fact an install holds is
queryable from that install, and always will be.

A commercial tier adds evidence export — the framework profiles and the generation of bundles
from them, on a schedule, signed with a managed key — along with identity and access control,
and reporting across multiple installations.

A profile is a mapping from stored facts to one framework's document structure, and it needs
nothing the public API does not already expose. Writing your own against `/v1` is a supported
path, not a workaround: the free product is tested to keep that possible.

The full boundary, including the capabilities committed never to be gated, is published in the
repository so that it is predictable in advance.

## Next

- [The audit trail](/guides/audit-trail/) — what is recorded, and how to query it
- [The lineage graph](/guides/lineage-graph/) — recording and traversing provenance
- [Comparing versions](/guides/version-diff/) — fingerprint levels and diff verdicts
- [Security model](/operate/security-model/) — the actor header and perimeter requirements
