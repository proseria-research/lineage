---
title: Quickstart
description: Run Lineage locally, publish a version with an uploaded file, promote it to production, resolve it and download its bytes with curl.
sidebar:
  order: 2
---

This runs a registry with SQLite and local filesystem storage, then walks one version from
upload to production. You need Go 1.25+, `curl` and `jq`. `make run` also builds the admin
console, which needs `pnpm`.

## Run the registry

```bash
git clone git@github.com:proseria-research/lineage.git
cd lineage
make run
```

`make run` builds `bin/lineage` with the embedded console and starts it with a 3650-day
retention floor and audit attestation on, matching the Helm chart defaults. Without `pnpm`, use
`go run ./cmd/lineage`: the Model API is identical, the console is a placeholder, and the
retention floor is `0`.

The Model API is at `http://localhost:8081/v1`, the admin console at `http://localhost:8080`,
and the ops health check at `http://localhost:9090/healthz`.

State lands in `./lineage.db` and `./data/artifacts`. To start over, stop the process and run
`make reset`. Under the retention floor, deleting a model or version is refused.

The examples send `X-Lineage-Actor`, the identity header an ingress would normally set. Lineage
records it on every audit event.

```bash
export API=http://localhost:8081/v1
export ACTOR='X-Lineage-Actor: you@example.com'
```

## Create a model and a version

```bash
curl -s -XPOST $API/models -H "$ACTOR" -H 'Content-Type: application/json' \
  -d '{"name":"fraud-detector","owner":"risk-platform"}'

curl -s -XPOST $API/models/fraud-detector/versions -H "$ACTOR" -H 'Content-Type: application/json' \
  -d '{"name":"1.4.0","description":"Q3 retrain"}'
```

The second call returns `201` with the version and its (empty) artifact list. New versions start
in `draft`:

```json
{
  "version": {
    "id": "01K5ZC8Q4W7N3M2B9X6T1RHJDF",
    "modelId": "01K5ZC8P0A1B2C3D4E5F6G7H8J",
    "model": "fraud-detector",
    "name": "1.4.0",
    "description": "Q3 retrain",
    "stage": "draft",
    "createdAt": 1790150400000,
    "updatedAt": 1790150400000,
    "stageChangedAt": 1790150400000
  },
  "artifacts": []
}
```

## Upload an artifact

Uploads are three calls: initiate, send bytes, finalize. With the `fs` backend the ticket is
stream-through, so bytes go to the Model API. On `s3` the ticket carries a presigned `url`
instead; see [Publishing](/api/publishing/).

```bash
head -c 1048576 /dev/urandom > model.onnx
SIZE=$(wc -c < model.onnx | tr -d ' ')
DIGEST="sha256:$(shasum -a 256 model.onnx | cut -d' ' -f1)"

TICKET=$(curl -s -XPOST "$API/models/fraud-detector/versions/1.4.0/artifacts:initiateUpload" \
  -H "$ACTOR" -H 'Content-Type: application/json' \
  -d "{\"name\":\"model.onnx\",\"kind\":\"MODEL\",\"sizeBytes\":$SIZE,\"modelFormat\":{\"name\":\"onnx\",\"version\":\"1.16\"}}")
echo "$TICKET"
```

```json
{
  "uploadId": "01K5ZC9B2Y8R4T6V0W3X5Z7A9C",
  "streamThrough": true,
  "contentUrl": "/v1/models/fraud-detector/versions/1.4.0/artifacts:uploadContent?uploadId=01K5ZC9B2Y8R4T6V0W3X5Z7A9C",
  "expiresAt": 1790154000000
}
```

```bash
curl -s -XPUT "http://localhost:8081$(echo "$TICKET" | jq -r .contentUrl)" --data-binary @model.onnx

curl -s -XPOST "$API/models/fraud-detector/versions/1.4.0/artifacts:finalizeUpload" \
  -H "$ACTOR" -H 'Content-Type: application/json' \
  -d "{\"uploadId\":\"$(echo "$TICKET" | jq -r .uploadId)\",\"digest\":\"$DIGEST\"}"
```

Finalize verifies the digest and size, then returns the artifact (`201`). A mismatch is `422`.
The ticket expires after one hour.

## Promote

Stages move along a fixed graph; there is no direct `draft` to `production`.

```bash
curl -s -XPOST "$API/models/fraud-detector/versions/1.4.0:transition" \
  -H "$ACTOR" -H 'Content-Type: application/json' -d '{"to":"staging"}'

curl -s -XPOST "$API/models/fraud-detector/versions/1.4.0:transition" \
  -H "$ACTOR" -H 'Content-Type: application/json' \
  -d '{"to":"production","reason":"passed shadow eval"}'
```

Entering `staging` locked the artifact set: further uploads, registrations and artifact deletes
on `1.4.0` return `409` with `details.reason: "version_locked"`. Entering `production` archived
any version that was there before, in the same transaction.

## Resolve

This is the call a serving system makes.

```bash
curl -si "$API/models/fraud-detector/resolve?stage=production"
```

```json
{
  "model": "fraud-detector",
  "versionId": "01K5ZC8Q4W7N3M2B9X6T1RHJDF",
  "version": "1.4.0",
  "stage": "production",
  "digest": "sha256:5f2b8c0e…",
  "modelFormat": { "name": "onnx", "version": "1.16" },
  "artifacts": [
    {
      "name": "model.onnx",
      "kind": "MODEL",
      "storageUri": "file://fraud-detector/1.4.0/model.onnx",
      "sizeBytes": 1048576,
      "digest": "sha256:5f2b8c0e…"
    }
  ],
  "resolvedAt": 1790150700000
}
```

The response has `ETag: "<digest>"` and `Cache-Control: private, no-cache`. The `fs` backend
cannot sign, so there is no `signedUrl`; on `s3` or `oci` each artifact also carries `signedUrl`
and `signedUrlExpiresAt`. Omitting `stage` resolves `production`.

## Download

```bash
curl -sL -o downloaded.onnx \
  "$API/models/fraud-detector/versions/1.4.0/artifacts/model.onnx/content"
shasum -a 256 downloaded.onnx
```

On `fs` the bytes stream through Lineage (with `Range` support). On a signing backend the same
URL answers `302` to a signed URL, which `-L` follows.

## Check the audit trail

```bash
curl -s "$API/audit" | jq -r '.items[] | "\(.action)\t\(.actor)\t\(.summary)"'
```

The feed is newest first. Each step above wrote one event: `model.create`, `version.create`,
`artifact.upload` and two `version.stage_changed`.

Next: [Data model](/start/data-model/) · [SDK and CLI](/clients/sdk-cli/) · [Promotion](/api/promotion/)
