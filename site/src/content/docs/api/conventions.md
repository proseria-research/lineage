---
title: Conventions
description: Base URL, contract, errors, status codes, pagination, filtering, idempotency and the actor header for the Model API.
sidebar:
  order: 5
---

Rules shared by every Model API endpoint.

## Base URL and shape

| Item | Value |
| --- | --- |
| Port | `:8081` (`LINEAGE_MODEL_API_ADDR`); the Admin UI on `:8080` is not part of the API |
| Prefix | `/v1`, e.g. `http://localhost:8081/v1/models` |
| Bodies | `application/json`; unknown fields are rejected with `400 invalid_argument` |
| Addressing | By name: `/v1/models/{model}/versions/{version}/artifacts/{artifact}` |
| Actions | `:verb` suffix on the resource, e.g. `…/versions/1.4.0:transition`, `…/artifacts:initiateUpload` |
| Timestamps | Integer epoch-millis (`createdAt`, `updatedAt`, `lockedAt`, …) |
| IDs | 26-character, time-sortable ULID-style strings, returned in every body |
| Auth | None; enforced by your ingress, gateway or mesh |

## The contract

`GET /v1/openapi.json` serves the OpenAPI 3.1 document (`info.version: v1`,
`Cache-Control: public, max-age=3600`). It is the source of truth; the Python SDK and CLI are
generated from it. `GET /v1/insight-schema.json` serves the insight document's JSON Schema.

`internal/contracttest` runs in CI on SQLite and Postgres and asserts two guarantees against a
live server over HTTP only:

| Guarantee | Meaning |
| --- | --- |
| Read completeness | Every recorded fact a compliance report needs is readable through `/v1`; no report needs the database, the Admin UI or the ops port |
| Actor header | A configured header name is honoured and its value lands verbatim in the audit trail |

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

| Status | Used by |
| --- | --- |
| `200` | Reads, `PATCH`, `:transition`, `:archive`, `:hold`, `:release` |
| `201` | Creates: model, version, artifact register, `finalizeUpload`, deployment, lineage edge |
| `202` | `initiateUpload` (returns a ticket, not the artifact) |
| `204` | `DELETE`, `uploadContent` |
| `206` / `302` / `304` | Artifact content and `resolve`; see [Resolve](/api/resolve/) |

## Pagination

Paged collections (`/v1/models`, `…/versions`, `…/audit`, `/v1/audit`, `/v1/reviews`,
`/v1/change-plans/conformance`) return:

```json
{"items": [ … ], "nextPageToken": "MTc5MDAwMDAwMDAwMHwwMUs1Wjh…"}
```

| Param | Behaviour |
| --- | --- |
| `pageSize` | Default `50`; values above `500` are clamped to `500` |
| `pageToken` | Opaque cursor from the previous page |
| `nextPageToken` | Empty string on the last page |

Order is newest first (models and versions by `createdAt`, audit events by `at`, then `id`,
descending); the cursor is stable across concurrent inserts.
`orderBy` is accepted but currently has no effect. Artifact lists return `{"items": […]}`
with no paging.

## Filtering

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

| Property | Behaviour |
| --- | --- |
| Scope | One keyspace for the whole process, shared by both endpoints and all models |
| Matching | Key only; the request body is not compared |
| What is stored | `2xx` responses only; an error is not stored, so a retry runs again |
| Lifetime | 24 hours, in memory; lost on restart and not shared between replicas |
| Concurrency | Two in-flight requests with one key both execute; the second gets `409 already_exists` |

Derive keys from the intent and include the resource, e.g.
`publish-fraud-detector-1.4.0`. A bare `1.4.0` reused for another model replays the first
model's response and creates nothing.

## Actor header

Lineage records who made a change from a header set by your front door. It makes no
authorization decision from it.

| Term | Contract |
| --- | --- |
| Name | `LINEAGE_ACTOR_HEADER` (Helm `actorHeader`), default `X-Lineage-Actor`; startup fails on an invalid name |
| Value | Trusted and recorded verbatim as `actor` on audit events and as `heldBy` on holds |
| Absent | The request succeeds; `actor` is omitted from its audit event |
| Stability | Renaming the header, or changing what the front door puts in it, is a breaking change for clients, proxies and reports |

Strip the header from untrusted traffic before your gateway sets it.

```bash
curl -X PATCH localhost:8081/v1/models/fraud-detector \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: alice@example.com' \
  -d '{"owner":"risk-ml"}'
```

Next: [Publishing](/api/publishing/) · [Audit](/governance/audit/) · [Configuration](/operate/configuration/)
