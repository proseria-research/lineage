---
title: Resolving a model
description: The one call serving systems make — ask for a stage, get native storage URIs, signed URLs, digests, sizes, and the model format.
sidebar:
  order: 1
---

Resolution is the pull path. A consumer asks for a model at a stage and gets back everything
needed to fetch it, without knowing anything about how the registry is laid out.

```bash
curl -s "localhost:8081/v1/models/fraud-detector/resolve?stage=production"
```

```json
{
  "model": "fraud-detector",
  "versionId": "01JQ7Y...",
  "version": "1.4.0",
  "stage": "production",
  "digest": "sha256:9f2c4a10...",
  "modelFormat": { "name": "onnx", "version": "1.16" },
  "artifacts": [
    {
      "name": "model.onnx",
      "kind": "MODEL",
      "storageUri": "s3://models/fraud-detector/1.4.0/model.onnx",
      "signedUrl": "https://s3.eu-west-1.amazonaws.com/...&X-Amz-Expires=900",
      "signedUrlExpiresAt": 1785312900,
      "sizeBytes": 431717888,
      "digest": "sha256:9f2c4a10...",
      "mediaType": "application/octet-stream",
      "serviceAccount": "fraud-detector-reader"
    }
  ],
  "resolvedAt": 1785312000
}
```

## Selectors

| Query | Returns |
| --- | --- |
| `?stage=production` | Whatever is in production right now |
| `?stage=staging` | The current candidate |
| `?version=1.4.0` | That exact version, whatever stage it is in |
| *(neither)* | The production version — the default |

Ask by **stage** in anything long-lived. Ask by **version** when you are pinning
deliberately: a reproducibility run, a bisect, a comparison.

A stage with nothing in it returns `409 failed_precondition`, not an empty success. "Nothing
is in production" is a condition your caller has to handle, not a normal response.

## Two ways to fetch

Every artifact comes back with both, and consumers pick:

- **`storageUri`** — the native URI: `s3://`, `gs://`, `oci://`, `hf://`. Use it when the
  runtime already speaks object storage and has its own credentials. Nothing passes through
  the registry.
- **`signedUrl`** — a freshly minted, time-limited HTTPS URL. Use it when the consumer only
  speaks HTTP or has no storage credentials of its own.

Signed URLs are **minted per response and never cached**. `signedUrlExpiresAt` tells you when
it stops working. Do not store one; call resolve again.

`serviceAccount` is a hint passed through from the artifact — the credential your platform is
expected to use for the native URI.

## Every MODEL artifact matters

A version's weights are often sharded, and the config and tokenizer are separate artifacts.
Pulling only the first entry leaves an unusable model directory.

Take **every artifact with `kind: "MODEL"`**. `DOC` artifacts — model cards, licences — stay
out unless you asked for them by name.

## Conditional requests

Resolutions carry an `ETag`. A poller that has not missed anything gets a `304`:

```bash
curl -s -D- -o/dev/null \
  -H 'If-None-Match: "01JQ7Y...-4"' \
  "localhost:8081/v1/models/fraud-detector/resolve?stage=production"
```

```
HTTP/1.1 304 Not Modified
```

This is the cheap way to watch for promotions: poll with `If-None-Match` and only do work
when the response is a `200`. See [Caching and invalidation](/delivery/caching/).

## Composition facts in one call

A scheduler picking a device class needs the size before it places the pod. Add
`include=insight`:

```bash
curl -s "localhost:8081/v1/models/fraud-detector/resolve?stage=production&include=insight"
```

```json
{
  "insight": {
    "paramCount": 7241000,
    "dtype": "fp32",
    "diskBytes": 431717888,
    "minDeviceMemoryBytes": 4186795520,
    "minDeviceMemoryScenario": "serve-a10g-batch8",
    "source": "measured"
  }
}
```

## What a consumer should do

1. `resolve` with a stage selector.
2. Take every `MODEL` artifact.
3. Fetch by `storageUri` if it has credentials, otherwise by `signedUrl`.
4. Verify each file against its `digest`.
5. Cache locally keyed by `digest` — content is write-once, so a matching digest means
   nothing to do.

## Next

- [Fetching artifact content](/delivery/artifact-content/) — when you want the registry to broker
- [Caching and invalidation](/delivery/caching/)
- [KServe](/delivery/kserve/) — skip all of this with `lineage://`
