---
title: Quickstart
description: Run Lineage locally, publish a model version, promote it to production, and resolve it — with no dependencies beyond Go.
sidebar:
  order: 2
---

This gets you a working registry with SQLite and local filesystem storage. Nothing else to
install, nothing to configure.

## Run it

```bash
git clone https://github.com/proseria-research/lineage
cd lineage
make run          # or: go run ./cmd/lineage
```

Three listeners come up:

| Port | Surface |
| --- | --- |
| `:8081` | Model API (`/v1`) — publish, resolve, fetch |
| `:8080` | Admin console |
| `:9090` | Ops — `/healthz`, `/readyz`, `/metrics` |

Check it is alive:

```bash
curl -s localhost:9090/healthz
```

## Publish a model

Create the model, then publish a version with an artifact registered by reference.

```bash
curl -XPOST localhost:8081/v1/models \
  -H 'Content-Type: application/json' \
  -H 'X-Lineage-Actor: you@example.com' \
  -d '{"name":"fraud-detector","owner":"risk-platform"}'
```

```bash
curl -XPOST localhost:8081/v1/models/fraud-detector/versions \
  -H 'Content-Type: application/json' \
  -H 'X-Lineage-Actor: you@example.com' \
  -d '{
    "name": "1.4.0",
    "artifacts": [{
      "name": "model.onnx",
      "uri": "s3://models/fraud-detector/1.4.0/model.onnx",
      "modelFormat": { "name": "onnx", "version": "1.16" }
    }]
  }'
```

The version starts in `draft`.

## Promote it

Versions move one stage at a time. Promoting to `production` archives whatever was there,
in the same transaction.

```bash
curl -XPOST localhost:8081/v1/models/fraud-detector/versions/1.4.0:transition \
  -H 'Content-Type: application/json' \
  -H 'X-Lineage-Actor: you@example.com' \
  -d '{"to":"staging"}'

curl -XPOST localhost:8081/v1/models/fraud-detector/versions/1.4.0:transition \
  -H 'Content-Type: application/json' \
  -H 'X-Lineage-Actor: you@example.com' \
  -d '{"to":"production","reason":"passed shadow eval"}'
```

## Resolve it

This is the call your serving system makes. It asks for a stage, not a version number.

```bash
curl -s "localhost:8081/v1/models/fraud-detector/resolve?stage=production"
```

```json
{
  "model": "fraud-detector",
  "version": "1.4.0",
  "stage": "production",
  "digest": "sha256:9f2c4a10...",
  "modelFormat": { "name": "onnx", "version": "1.16" },
  "artifacts": [
    {
      "name": "model.onnx",
      "kind": "MODEL",
      "storageUri": "s3://models/fraud-detector/1.4.0/model.onnx",
      "signedUrl": "https://...",
      "digest": "sha256:9f2c4a10...",
      "sizeBytes": 431717888
    }
  ],
  "resolvedAt": 1785312000
}
```

Promote a different version and the same call returns the new one. Nothing redeploys.

## Load sample data

With the registry running, seed a demo dataset — five models across every stage, uploaded and
by-reference artifacts, lineage edges, deployments, and a real audit trail:

```bash
make seed                       # or: go run ./cmd/lineage-seed
```

Seeding is additive and never deletes. A registry with a retention floor refuses deletion by
design, and a fixture loader is not a reason to reach around that — so starting over means a
fresh registry: stop it, `make reset`, run again.

The seeder talks to the Model API like any other client, so `LINEAGE_ENDPOINT` (default
`http://localhost:8081`) can point it at a port-forward or a remote install.

## Look at it

Open the console at [localhost:8080](http://localhost:8080) and the API contract at
[localhost:8081/v1/openapi.json](http://localhost:8081/v1/openapi.json).

## Next

- [Register your first model](/start/first-model/) — the same flow with a real file upload
- [Core concepts](/start/concepts/) — what the entities mean
- [Deploy with Helm](/deploy/helm/) — the same registry, in a cluster
