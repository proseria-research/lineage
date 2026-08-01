---
title: The audit trail
description: Every state transition is recorded, attributed, and queryable — per model or across the whole registry.
sidebar:
  order: 6
---

Every mutating operation writes an audit event in the same transaction as the change. If the
change committed, the event exists. If the event does not exist, the change did not happen.

## What an event looks like

```json
{
  "id": "01JQ8Z...",
  "at": 1785312000,
  "actor": "release-bot@example.com",
  "action": "version.transition",
  "subjectType": "model_version",
  "subjectId": "01JQ7Y...",
  "summary": "fraud-detector 1.4.0: staging → production",
  "data": {
    "from": "staging",
    "to": "production",
    "reason": "shadow eval green for 48h",
    "demoted": "1.3.0"
  }
}
```

| Field | Meaning |
| --- | --- |
| `actor` | From the trusted identity header — see below |
| `action` | What happened, as a dotted verb |
| `subjectType` / `subjectId` | The entity that changed |
| `summary` | A human-readable line |
| `data` | Structured detail specific to the action |

## Reading it

Per model — the usual starting point when something looks wrong:

```bash
curl -s "localhost:8081/v1/models/fraud-detector/audit?pageSize=50"
```

Across the whole registry — for a compliance export or a security review:

```bash
curl -s "localhost:8081/v1/audit?pageSize=200"
```

Both are cursor-paginated. Follow `nextPageToken` until it is empty.

```bash
curl -s "localhost:8081/v1/models/fraud-detector/audit?pageSize=200" \
  | jq -r '.items[] | select(.action=="version.transition")
           | [.at, .actor, .summary, .data.reason] | @tsv'
```

## Where the actor comes from

Lineage does not authenticate anybody. Your ingress, gateway, or mesh does that, and then
passes an identity along in a header — `X-Lineage-Actor` by default, configurable with
`LINEAGE_ACTOR_HEADER`.

Lineage records whatever that header says.

:::caution[Strip it at the edge]
The actor header is trusted, which means the perimeter must **overwrite** it on every inbound
request. If a client can set it themselves, your audit trail records whatever they felt like
claiming. See [Security model](/operate/security-model/).
:::

Requests without the header are recorded with an empty actor. That is legal — it just makes
the trail less useful, so set it everywhere, including in CI.

## It is append-only

There is no API to edit or delete an audit event. Deleting a model cascades to its entities,
and the global feed keeps the record of what happened.

For long-term retention beyond the registry's own database, export the global feed on a
schedule and ship it wherever your other audit data lives.

## What gets recorded

Every create, update, delete, transition, upload finalize, lineage edge change, and
deployment change. The action names follow `subject.verb`:

| Action | Written when |
| --- | --- |
| `model.create`, `model.update`, `model.archive`, `model.delete` | Model lifecycle |
| `version.publish`, `version.update`, `version.delete` | Version lifecycle |
| `version.transition` | A stage change, including the demoted incumbent |
| `artifact.create`, `artifact.update`, `artifact.delete` | Artifact metadata |
| `lineage.add`, `lineage.delete` | Provenance edges |
| `deployment.create`, `deployment.update`, `deployment.delete` | Deployment records |

## Next

- [Stages and promotion](/guides/stages-and-promotion/)
- [Security model](/operate/security-model/)
- [Observability](/operate/observability/)
