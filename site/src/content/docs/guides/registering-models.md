---
title: Registering models
description: Create, describe, update, archive, and delete models — and the naming conventions that keep consumers working.
sidebar:
  order: 1
---

A model is the named thing your organisation ships. Versions hang off it, consumers reference
it, and its name appears in every URL that addresses it.

## Create

```bash
curl -XPOST localhost:8081/v1/models \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: you@example.com' \
  -d '{
    "name": "fraud-detector",
    "description": "Card-not-present transaction scoring",
    "owner": "risk-platform",
    "labels": { "tier": "critical", "domain": "payments" },
    "customProperties": { "runbook": "https://wiki/runbooks/fraud-detector" }
  }'
```

Only `name` is required.

| Field | Notes |
| --- | --- |
| `name` | Unique, path-safe, immutable in practice — consumers depend on it |
| `description` | Free text |
| `owner` | Team or person accountable for the model |
| `labels` | Flat `string → string` map, meant for filtering |
| `customProperties` | Arbitrary JSON, meant for carrying your own metadata |

## Naming

Names must be path-safe: no slashes, no spaces, no characters that would need escaping in a
URL. Use the name of the thing, not of a particular attempt at it.

| Good | Avoid |
| --- | --- |
| `fraud-detector` | `fraud-detector-v3` — that is a version |
| `speech-to-text-en` | `johns-model-final-FINAL` |
| `recommender-homepage` | `Recommender Homepage` |

Version numbers, dates, and experiment identifiers belong on the version, not the model.

## Read

```bash
curl -s localhost:8081/v1/models/fraud-detector
```

List, with cursor pagination and a substring search on the name:

```bash
curl -s "localhost:8081/v1/models?q=fraud&pageSize=50&orderBy=createdAt%20desc"
```

| Parameter | Purpose |
| --- | --- |
| `q` | Case-insensitive substring match on the name |
| `state` | `ACTIVE` or `ARCHIVED` |
| `pageSize` | Default 50, maximum 500 |
| `pageToken` | Cursor from the previous response's `nextPageToken` |
| `orderBy` | Sort key and direction |

Responses look like `{ "items": [...], "nextPageToken": "..." }`. Keep following
`nextPageToken` until it comes back empty. See
[API conventions](/clients/api-conventions/).

## Update

`PATCH` merges the fields you send and leaves the rest alone.

```bash
curl -XPATCH localhost:8081/v1/models/fraud-detector \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: you@example.com' \
  -d '{"owner":"risk-platform-oncall","labels":{"tier":"critical","pii":"none"}}'
```

`labels` and `customProperties` are replaced as whole objects, not merged key by key. Read,
modify, and send the full map if you want to add one key.

## Archive

Archiving is a soft, reversible state change. The model stops appearing in default listings;
nothing is deleted.

```bash
curl -XPOST localhost:8081/v1/models/fraud-detector:archive \
  -H 'X-Lineage-Actor: you@example.com'
```

Bring it back by patching the state:

```bash
curl -XPATCH localhost:8081/v1/models/fraud-detector \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: you@example.com' \
  -d '{"state":"ACTIVE"}'
```

Archive is the right move for a model nobody should use any more. It keeps the record intact
and keeps existing lineage edges pointing at something real.

## Delete

```bash
curl -XDELETE localhost:8081/v1/models/fraud-detector \
  -H 'X-Lineage-Actor: you@example.com'
```

:::caution[This cascades]
Deleting a model deletes its versions, artifacts metadata, lineage edges, and deployments.
Artifact **bytes** are only removed if garbage collection is enabled; by default the registry
retains them. See [Garbage collection](/operate/garbage-collection/).

Prefer archiving. Delete when a model was created in error or when a retention policy
requires the record gone.
:::

## Next

- [Publishing versions](/guides/publishing-versions/)
- [Labels and search](/guides/labels-and-search/)
- [The audit trail](/guides/audit-trail/)
