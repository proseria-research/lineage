---
title: Labels and search
description: Use labels and custom properties to make a registry navigable, and know which filters are Postgres-only.
sidebar:
  order: 5
---

Models and versions both carry `labels` (a flat `string → string` map) and
`customProperties` (arbitrary JSON). They serve different jobs.

| | `labels` | `customProperties` |
| --- | --- | --- |
| Shape | Flat strings | Any JSON |
| For | Filtering and grouping | Carrying detail |
| Indexed | Yes | JSONB on Postgres; stored but unindexed on SQLite |
| Keep it | Small and consistent | As big as it needs to be |

## Designing a label set

Labels only help if the same keys mean the same thing across models. Agree on a set and
write it down.

```json
{
  "tier": "critical",
  "domain": "payments",
  "framework": "xgboost",
  "pii": "none",
  "retrain": "2026-q1"
}
```

Good keys answer a question somebody actually asks:

| Key | Question it answers |
| --- | --- |
| `tier` | What breaks if this is wrong? |
| `owner-team` | Who do I page? |
| `pii` | Does this need a data review? |
| `framework` | Which runtime image serves it? |

Avoid encoding things the registry already knows. `stage`, `version`, and `digest` are
first-class fields; duplicating them into labels guarantees they will disagree eventually.

## Search

List endpoints take a substring search on the name plus cursor pagination:

```bash
curl -s "localhost:8081/v1/models?q=fraud&pageSize=50"
curl -s "localhost:8081/v1/models?state=ARCHIVED"
curl -s "localhost:8081/v1/models/fraud-detector/versions?orderBy=createdAt%20desc"
```

| Parameter | Applies to | Notes |
| --- | --- | --- |
| `q` | Models, versions | Case-insensitive substring on the name |
| `state` | Models | `ACTIVE` or `ARCHIVED` |
| `pageSize` | All lists | Default 50, maximum 500 |
| `pageToken` | All lists | Cursor from `nextPageToken` |
| `orderBy` | All lists | Sort key and direction |

## Label filtering

Filtering by label content is pushed down into the database on Postgres, where `labels` and
`customProperties` are JSONB columns with containment queries. On SQLite the values are
stored and returned faithfully but are not indexed for containment, so complex label queries
are a Postgres capability.

Core registry behaviour is identical on both engines. Advanced querying is where they
diverge, deliberately — SQLite is the zero-dependency tier, Postgres is the full-power one.
See [Choosing a metadata store](/operate/metadata-store/).

If your workflow depends on querying labels at scale, run Postgres.

## Paginating properly

```bash
token=""
while :; do
  page=$(curl -s "localhost:8081/v1/models?pageSize=200&pageToken=$token")
  echo "$page" | jq -r '.items[].name'
  token=$(echo "$page" | jq -r '.nextPageToken // empty')
  [ -z "$token" ] && break
done
```

Cursors are opaque. Do not construct them, do not assume they stay valid forever, and do not
try to skip ahead by offset.

## Next

- [Registering models](/guides/registering-models/)
- [API conventions](/clients/api-conventions/)
- [Choosing a metadata store](/operate/metadata-store/)
