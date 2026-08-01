---
title: Idempotency and retries
description: Make writes safe to repeat with Idempotency-Key, and know which operations are already idempotent.
sidebar:
  order: 11
---

Anything publishing from CI will eventually publish twice — a retried job, a flaky network, a
runner that died after the request left but before the response arrived. Writes need to be
safe to repeat.

## Idempotency-Key

Send the header on any `POST` that creates something:

```bash
curl -XPOST localhost:8081/v1/models/fraud-detector/versions \
  -H 'Content-Type: application/json' \
  -H 'X-Lineage-Actor: ci@example.com' \
  -H 'Idempotency-Key: publish-fraud-detector-1.4.0' \
  -d '{"name":"1.4.0"}'
```

The first call does the work and records the response against the key. A later call with the
same key **replays that response** instead of failing with `409 already_exists`.

The replay is byte-for-byte the original response, including the identifiers. A retried
pipeline sees exactly what the first attempt saw.

## Choosing keys

:::caution[Namespace them]
The idempotency store is a single global keyspace. A bare version name like `1.4.0` collides
across models and replays the wrong response — leaving you with a success status and no
version created.
:::

Include the resource:

| Good | Bad |
| --- | --- |
| `publish-fraud-detector-1.4.0` | `1.4.0` |
| `model-create-fraud-detector` | `create` |
| `artifact-fraud-detector-1.4.0-model.onnx` | `model.onnx` |

Derive the key from the *intent*, not from the attempt. A key containing the CI run number
makes every retry a new operation, which is the opposite of what you want.

## What is already idempotent

| Operation | Behaviour on repeat |
| --- | --- |
| `PATCH` on any entity | Naturally idempotent — same body, same end state |
| `PUT` footprint scenario | Upsert by scenario name |
| `POST :transition` to the current stage | Rejected as an illegal transition, not silently applied |
| `POST :finalizeUpload` | Guarded by the write-once artifact constraint |
| `DELETE` | Second call returns `404 not_found` |

`POST` to a collection is where you need the key.

## Retry policy

Retry on `429`, `502`, `503`, `504`, and on connection failures. Use exponential backoff with
jitter.

Do **not** retry on `4xx` other than `429` — those describe a request that will fail the same
way forever.

| Status | Code | Retry? |
| --- | --- | --- |
| 400 | `invalid_argument` | No — fix the request |
| 404 | `not_found` | No |
| 409 | `already_exists` | No — you already succeeded; use a key next time |
| 409 | `failed_precondition` | No — the state does not permit it |
| 413 | `payload_too_large` | No |
| 422 | `unprocessable` | No |
| 429 | `rate_limited` | Yes, with backoff |
| 5xx | `internal` | Yes, with backoff |

## A publish that survives a flaky runner

```bash
publish() {
  local key="publish-$1-$2"
  for attempt in 1 2 3 4 5; do
    if curl -sf -XPOST "$LINEAGE/v1/models/$1/versions" \
        -H 'Content-Type: application/json' \
        -H "X-Lineage-Actor: $CI_ACTOR" \
        -H "Idempotency-Key: $key" \
        -d "{\"name\":\"$2\"}"; then
      return 0
    fi
    sleep $((attempt * attempt))
  done
  return 1
}
```

## Next

- [API conventions](/clients/api-conventions/)
- [Publishing versions](/guides/publishing-versions/)
- [Python SDK](/clients/python-sdk/)
