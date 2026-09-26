---
title: Conventions
description: Base URL, contract, errors, status codes, pagination, filtering, idempotency and the actor header for the Model API.
sidebar:
  order: 5
---

Rules shared by every Model API endpoint.

## Base URL and shape

Every request follows these rules:

- Port: `:8081` (`LINEAGE_MODEL_API_ADDR`). The Admin UI on `:8080` is not part of the API.
- Prefix: `/v1`, e.g. `http://localhost:8081/v1/models`.
- Bodies: `application/json`. Unknown fields are rejected with `400 invalid_argument`.
- Addressing: by name, e.g. `/v1/models/{model}/versions/{version}/artifacts/{artifact}`.
- Actions: a `:verb` suffix on the resource, e.g. `…/versions/1.4.0:transition`,
  `…/artifacts:initiateUpload`.
- Timestamps: integer epoch-millis (`createdAt`, `updatedAt`, `lockedAt`, …).
- IDs: 26-character, time-sortable ULID-style strings, returned in every body.
- Auth: none. Your ingress, gateway or mesh enforces it.

## The contract

`GET /v1/openapi.json` serves the OpenAPI 3.1 document (`info.version: v1`,
`Cache-Control: public, max-age=3600`). It is the source of truth; the Python SDK and CLI are
generated from it. `GET /v1/insight-schema.json` serves the insight document's JSON Schema.

`internal/contracttest` runs in CI on SQLite and Postgres. Against a live server, over HTTP only,
it asserts two guarantees:

- Read completeness: every recorded fact a compliance report needs is readable through `/v1`. No
  report needs the database, the Admin UI or the ops port.
- Actor header: a configured header name is honoured and its value lands verbatim in the audit
  trail.

## Errors

Errors are `application/problem+json` (RFC 9457):

```json
{
  "type": "https://lineage.dev/errors/failed_precondition",
  "title": "failed_precondition",
  "status": 409,
  "code": "failed_precondition",
  "detail": "illegal stage transition draft→production",
  "details": {"allowedTargets": ["staging", "archived"]}
}
```

Branch on `code` and `details.reason`, not on `detail` text. `title` repeats `code`;
`details` appears only when there is context.

| `code` | Status | Typical cause |
| --- | --- | --- |
| `invalid_argument` | 400 | Bad name, bad JSON or unknown field, missing required field, unknown action, `cp.*` filter on SQLite |
| `not_found` | 404 | Unknown model, version, artifact or upload ticket |
| `already_exists` | 409 | Duplicate model, version or artifact name |
| `failed_precondition` | 409 | Illegal transition, locked version, blocked delete, no version matches a selector, changed immutable artifact field, expired upload ticket |
| `unprocessable` | 422 | Digest or size mismatch on finalize, no uploaded object |
| `internal` | 500 | Anything unexpected, including storage errors |

`details.reason` separates refusals that share `failed_precondition`:

| `reason` | Refused | Remedy |
| --- | --- | --- |
| `version_locked` | Artifact register, upload, finalize or delete on a locked version (`lockedAt`) | Publish a new version |
| `legal_hold` | Delete of a held subject (`heldSince`, `heldBy`, `heldSubject`) | Release the hold |
| `retention_floor` | Delete inside the retention floor (`floorDays`, `ageDays`) | Wait |
| `already_held` / `not_held` | `:hold` on a held subject / `:release` on an unheld one | None needed |

A path or method that matches no route gets Go's plain-text `404` or `405`, not a problem body.

## Success status codes

Successful calls return these statuses:

- `200`: reads, `PATCH`, `:transition`, `:archive`, `:hold`, `:release`.
- `201`: creates: model, version, artifact register, `finalizeUpload`, deployment, lineage edge.
- `202`: `initiateUpload`. It returns a ticket, not the artifact.
- `204`: `DELETE`, `uploadContent`.
- `206`, `302`, `304`: artifact content and `resolve`; see [Resolve](/api/resolve/).

## Pagination

Paged collections (`/v1/models`, `…/versions`, `…/audit`, `/v1/audit`, `/v1/reviews`,
`/v1/change-plans/conformance`) return:

```json
{"items": [ … ], "nextPageToken": "MTc5MDAwMDAwMDAwMHwwMUs1Wjh…"}
```

`pageSize` defaults to `50`; values above `500` are clamped to `500`. Pass the previous page's
`nextPageToken`, an opaque cursor, as `pageToken`. `nextPageToken` is an empty string on the last
page.

Order is newest first (models and versions by `createdAt`, audit events by `at`, then `id`,
descending); the cursor is stable across concurrent inserts.
`orderBy` is accepted but currently has no effect. Artifact lists return `{"items": […]}`
with no paging.

## Filtering

List endpoints accept these query parameters:

| Param | Endpoints | Behaviour |
| --- | --- | --- |
| `q` | models | Substring match on `name` |
| `state` | models | `ACTIVE` or `ARCHIVED` |
| `stage` | versions | Exact stage |
| `label.<key>=<value>` | models, versions | Label equality; several keys are ANDed |
| `cp.<key>=<value>` | models, versions | `customProperties` equality; Postgres only, else `400` |
| `asOf` | audit feeds | Epoch-millis; only events at or before it. Non-positive or non-integer is `400` |

`/v1/models` also filters on classification and model-risk fields; see
[Compliance](/governance/compliance/).

```bash
curl 'localhost:8081/v1/models?q=fraud&label.team=risk&pageSize=20'
```

## Idempotency keys

`POST /v1/models` and `POST /v1/models/{model}/versions` accept an `Idempotency-Key` header.
The first `2xx` response for a key is stored and replayed (status, headers, body) on any
later request with that key.

- Scope: one keyspace for the whole process, shared by both endpoints and all models.
- Matching: by key only. The request body is not compared.
- Stored: `2xx` responses only. An error is not stored, so a retry runs again.
- Lifetime: 24 hours, in memory. Keys are lost on restart and not shared between replicas.
- Concurrency: two in-flight requests with one key both execute; the second gets
  `409 already_exists`.

Derive keys from the intent and include the resource, e.g.
`publish-fraud-detector-1.4.0`. A bare `1.4.0` reused for another model replays the first
model's response and creates nothing.

## Actor header

Lineage records who made a change from a header set by your front door. It makes no
authorization decision from it.

- Name: `LINEAGE_ACTOR_HEADER` (Helm `actorHeader`), default `X-Lineage-Actor`. Startup fails
  on an invalid name.
- Value: trusted and recorded verbatim as `actor` on audit events and as `heldBy` on holds.
- Absent: the request succeeds; `actor` is omitted from its audit event.
- Stability: renaming the header, or changing what the front door puts in it, is a breaking
  change for clients, proxies and reports.

Strip the header from untrusted traffic before your gateway sets it.

```bash
curl -X PATCH localhost:8081/v1/models/fraud-detector \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: alice@example.com' \
  -d '{"owner":"risk-ml"}'
```

Next: [Publishing](/api/publishing/) · [Audit](/governance/audit/) · [Configuration](/operate/configuration/)
