---
title: Publishing versions
description: Publish a model version with inline artifacts, name it well, attach metadata, and make the call safe to retry.
sidebar:
  order: 2
---

A version is one concrete release of a model. Publishing creates it in `draft` with whatever
artifacts you pass inline.

## Publish

```bash
curl -XPOST localhost:8081/v1/models/fraud-detector/versions \
  -H 'Content-Type: application/json' \
  -H 'X-Lineage-Actor: ci@example.com' \
  -H 'Idempotency-Key: publish-fraud-detector-1.4.0' \
  -d '{
    "name": "1.4.0",
    "description": "Quarterly retrain, ONNX export",
    "author": "risk-platform",
    "labels": { "retrain": "2026-q1", "framework": "xgboost" },
    "artifacts": [
      {
        "name": "model.onnx",
        "kind": "MODEL",
        "uri": "s3://models/fraud-detector/1.4.0/model.onnx",
        "digest": "sha256:9f2c4a10...",
        "sizeBytes": 431717888,
        "modelFormat": { "name": "onnx", "version": "1.16" }
      },
      {
        "name": "model-card.md",
        "kind": "DOC",
        "uri": "s3://models/fraud-detector/1.4.0/model-card.md",
        "mediaType": "text/markdown"
      }
    ]
  }'
```

The response is a `PublishResult` — the version and every artifact created with it:

```json
{
  "version": {
    "id": "01JQ...",
    "model": "fraud-detector",
    "name": "1.4.0",
    "stage": "draft",
    "author": "risk-platform",
    "createdAt": 1785312000
  },
  "artifacts": [ { "id": "01JQ...", "name": "model.onnx", "digest": "sha256:9f2c4a10..." } ]
}
```

Publishing is one transaction. If any artifact is rejected, no version is created.

## Inline artifacts vs. upload

Inline artifacts are **registered by reference**: you are telling Lineage where bytes already
live. If the backend can reach the URI, it fills in `digest` and `sizeBytes` for you via a
`Stat`.

If the bytes are on the machine running the publish, use the upload flow instead — publish the
version first, then initiate and finalize. See
[Artifacts and uploads](/guides/artifacts-and-uploads/).

## Naming versions

Version names are unique per model and path-safe. Beyond that, Lineage does not care what
scheme you use — it never sorts by name to decide what is newest, because that is what stages
are for.

| Scheme | Works well when |
| --- | --- |
| Semver — `1.4.0` | A human decides what changed |
| Date — `2026-04-01` | A scheduled retrain produces them |
| Run id — `run-8b21c4de` | A pipeline is the only publisher |

Pick one per model and keep it. Mixed schemes make the version list unreadable.

## Metadata that earns its place

```json
{
  "labels": { "retrain": "2026-q1", "framework": "xgboost", "approved": "true" },
  "customProperties": {
    "training": { "epochs": 40, "seed": 7 },
    "eval": { "auc": 0.947, "dataset": "holdout-2026-q1" }
  }
}
```

- **`labels`** are flat strings and exist to be filtered on. Keep the key set small and
  consistent across a model's versions.
- **`customProperties`** is arbitrary JSON for things you want carried but not queried. On
  Postgres it is JSONB and can be filtered; on SQLite it is stored and returned faithfully but
  not indexed.

Structured facts about *composition* — parameter counts, dtype, quantization — belong in
[insights](/guides/model-insights/) instead, where they get provenance and take part in diff.

## Make it retry-safe

Publishing from CI means publishing from something that will occasionally run twice. Send an
`Idempotency-Key`:

```bash
-H 'Idempotency-Key: publish-fraud-detector-1.4.0'
```

A replayed request returns the original response instead of `409 already_exists`.

:::caution[Namespace your keys]
The idempotency store is a single global keyspace. A bare version name like `1.4.0` will
collide across models and replay the wrong response. Include the resource in the key.
:::

## Update metadata

```bash
curl -XPATCH localhost:8081/v1/models/fraud-detector/versions/1.4.0 \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: you@example.com' \
  -d '{"description":"Quarterly retrain; fixes the currency-code bug"}'
```

Metadata is mutable. Content is not — you cannot repoint an artifact at different bytes.

## Delete

```bash
curl -XDELETE localhost:8081/v1/models/fraud-detector/versions/1.3.0 \
  -H 'X-Lineage-Actor: you@example.com'
```

A version in `production` cannot be deleted. Move it out of production first — that guard
exists so a delete can never leave a model with nothing serving.

## Next

- [Artifacts and uploads](/guides/artifacts-and-uploads/)
- [Stages and promotion](/guides/stages-and-promotion/)
- [Idempotency and retries](/guides/idempotency/)
