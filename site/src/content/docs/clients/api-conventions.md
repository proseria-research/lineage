---
title: API conventions
description: The rules every /v1 endpoint follows — resources, actions, pagination, errors, identity, and the OpenAPI contract.
sidebar:
  order: 3
---

REST over `/v1`, with a hand-authored OpenAPI 3.1 document as the source of truth. The SDK
and CLI are generated from it.

```bash
curl -s localhost:8081/v1/openapi.json
```

## Resources and actions

Normal CRUD uses ordinary paths and verbs:

```
GET    /v1/models
POST   /v1/models
GET    /v1/models/{model}
PATCH  /v1/models/{model}
DELETE /v1/models/{model}
```

Operations that are not CRUD use a `:verb` suffix on the resource:

```
POST /v1/models/{model}:archive
POST /v1/models/{model}/versions/{version}:transition
POST /v1/models/{model}/versions/{version}/artifacts:initiateUpload
POST /v1/models/{model}/versions/{version}/artifacts:finalizeUpload
```

The suffix says plainly that this is an action on a resource rather than a sub-resource, so
nobody has to guess whether `POST /versions/{v}/transition` creates something called a
transition.

## Addressing

Paths address things by **name**, because names are what humans and pipelines already have:

```
/v1/models/fraud-detector/versions/1.4.0/artifacts/model.onnx
```

Every entity also carries a ULID `id`. Lineage edges and audit rows point at IDs. Both work
where an identifier is expected.

Names are path-safe by validation — no slashes, no spaces, nothing needing escaping.

## Pagination

Every list endpoint is cursor-paginated.

| Parameter | Meaning |
| --- | --- |
| `pageSize` | Default 50, maximum 500 |
| `pageToken` | Cursor from the previous response |
| `orderBy` | Sort key and direction |
| `q` | Case-insensitive substring match on the name |

```json
{ "items": [ … ], "nextPageToken": "eyJ…" }
```

Follow `nextPageToken` until it comes back empty or absent. Cursors are opaque — do not
construct them, parse them, or assume they stay valid indefinitely.

## Errors

RFC 9457 problem documents, served as `application/problem+json`.

```json
{
  "type": "https://lineage.dev/errors/failed_precondition",
  "title": "illegal stage transition",
  "status": 409,
  "code": "failed_precondition",
  "detail": "cannot move from draft to production",
  "instance": "/v1/models/fraud-detector/versions/1.4.0:transition",
  "details": { "from": "draft", "to": "production", "allowed": ["staging", "archived"] }
}
```

**Branch on `code`, not on `status`.** Codes are stable; two different codes share `409`.

| Code | Status | Means |
| --- | --- | --- |
| `invalid_argument` | 400 | The request is malformed |
| `not_found` | 404 | No such resource |
| `already_exists` | 409 | It is already there — send an `Idempotency-Key` |
| `failed_precondition` | 409 | The current state does not permit this |
| `payload_too_large` | 413 | Too big |
| `unprocessable` | 422 | Well-formed but semantically wrong — a digest mismatch, say |
| `rate_limited` | 429 | Back off and retry |
| `internal` | 500 | Our fault |

`details` carries structured, actionable context. On a failed transition it tells you which
stages you could have moved to.

## Identity

Every mutating request should carry the actor header:

```
X-Lineage-Actor: you@example.com
```

Configurable with `LINEAGE_ACTOR_HEADER`. Lineage does not authenticate — it records what the
header says, and your perimeter is responsible for setting it truthfully. See
[Security model](/operate/security-model/).

## Idempotency

```
Idempotency-Key: publish-fraud-detector-1.4.0
```

Honoured on creating `POST`s. A replay returns the original response byte for byte. The
keyspace is global, so namespace your keys by resource. See
[Idempotency and retries](/guides/idempotency/).

## Conditional requests

`GET /resolve` and artifact `/content` return `ETag` and honour `If-None-Match` with a `304`.
On content requests the `ETag` is the artifact digest. See
[Caching and invalidation](/delivery/caching/).

## Timestamps

All timestamps are **Unix seconds as integers** — `createdAt`, `updatedAt`, `resolvedAt`,
`at`, `expiresAt`, `signedUrlExpiresAt`. No strings, no timezones, no ambiguity.

## Field naming

`camelCase` throughout, in both requests and responses. Enum values are lowercase for stages
(`draft`, `staging`, `production`, `archived`) and uppercase for states and kinds (`ACTIVE`,
`ARCHIVED`, `MODEL`, `DOC`) — matching the distinction between a lifecycle position and a
type tag.

## PATCH semantics

`PATCH` merges the top-level fields you send. Map-valued fields — `labels`,
`customProperties` — are **replaced whole**, not merged key by key. Read, modify, and send the
full map to add a single key.

The exception is `PATCH /insight`, which is a genuine field-level merge with per-field
attribution, so two producers can contribute to one version without overwriting each other.
See [Model insights](/guides/model-insights/).

## Versioning

The API is `/v1`. Additive changes — new fields, new endpoints, new enum values — ship within
it. Anything breaking would be `/v2`.

Write clients that ignore unknown fields.

## Next

- [Python SDK](/clients/python-sdk/)
- [CLI](/clients/cli/)
- [Idempotency and retries](/guides/idempotency/)
