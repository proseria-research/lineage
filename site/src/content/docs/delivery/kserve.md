---
title: KServe
description: Reference models by stage with lineage:// so a promotion takes effect without touching the InferenceService.
sidebar:
  order: 4
---

There are two ways to serve a Lineage model on KServe. The second one is the reason this
integration exists.

## Option 1 — resolve and paste the URI

Resolve the model in your pipeline and put the native `storageUri` into the
`InferenceService`:

```bash
uri=$(curl -s "$LINEAGE/v1/models/fraud-detector/resolve?stage=production" \
      | jq -r '.artifacts[] | select(.kind=="MODEL") | .storageUri' | head -1)
```

```yaml
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata: { name: fraud-detector }
spec:
  predictor:
    model:
      storageUri: s3://models/fraud-detector/1.4.0/model.onnx
      modelFormat: { name: onnx }
```

This works with stock KServe and nothing else installed. The cost is that every promotion
needs a manifest change and a redeploy.

## Option 2 — `lineage://`

Reference the **stage** instead of the version. The URI stays the same forever; what it
resolves to changes when you promote.

```yaml
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata: { name: fraud-detector }
spec:
  predictor:
    model:
      storageUri: lineage://fraud-detector/production
      modelFormat: { name: onnx }
```

Promote a new version in Lineage and the next pod pull picks it up. No manifest change, no
`kubectl apply`, no drift between what the registry says is in production and what the
cluster is running.

### Install the scheme

A `ClusterStorageContainer` registers `lineage://` cluster-wide. Install it once.

```yaml
apiVersion: serving.kserve.io/v1alpha1
kind: ClusterStorageContainer
metadata: { name: lineage }
spec:
  container:
    name: storage-initializer
    image: ghcr.io/proseria-research/lineage-init:<tag>
    env:
      - { name: LINEAGE_ENDPOINT, value: "http://lineage-model-api:8081" }
      - { name: LINEAGE_ACTOR, value: "kserve" }
    resources:
      requests: { cpu: 100m, memory: 100Mi }
  supportedUriFormats:
    - prefix: lineage://
```

### The URI grammar

```
lineage://<model>[/<stage>][@<version>][#<artifact>]
```

| URI | Resolves to |
| --- | --- |
| `lineage://fraud-detector` | The production version, all `MODEL` artifacts |
| `lineage://fraud-detector/staging` | The staging version |
| `lineage://fraud-detector@1.4.0` | That exact version — a deliberate pin |
| `lineage://fraud-detector/production#model.onnx` | One named artifact |

### What the initializer does

`lineage-init` runs as the init container. Given a `lineage://` URI and a destination
directory, it resolves the model against the Model API and downloads **every `MODEL`
artifact** of the version — by signed URL where available, otherwise through the
`/content` broker.

Every `MODEL` artifact, deliberately: sharded weights, config, and tokenizer are separate
artifacts of one version, and pulling only the first leaves an unusable model directory. A
`#artifact` fragment narrows it to one file when that is what you want.

It can be run by hand, which is the fastest way to debug a pull:

```bash
lineage-init lineage://fraud-detector/production /mnt/models
```

| Variable | Default | Purpose |
| --- | --- | --- |
| `LINEAGE_ENDPOINT` | `http://lineage-model-api:8081` | Model API base URL |
| `LINEAGE_ACTOR` | — | Audit identity recorded on the resolve |

## Record the deployment

Close the loop so downstream traversal reaches the endpoint:

```bash
curl -XPOST "$LINEAGE/v1/models/fraud-detector/versions/1.4.0/deployments" \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: cd@example.com' \
  -d '{
    "environment": "prod-eu-1",
    "endpointUri": "https://infer.eu.example.com/v2/models/fraud-detector",
    "status": "ACTIVE",
    "externalRef": "kserve://ns/risk/inferenceservices/fraud-detector"
  }'
```

See [Deployments](/guides/deployments/).

## Rollout behaviour

Promotion changes the registry, not the cluster. Existing pods keep serving the version they
pulled at startup; new pods pull the new one.

To make a promotion take effect immediately, restart the deployment after promoting:

```bash
kubectl rollout restart inferenceservice/fraud-detector
```

Doing it in that order — promote, then restart — means a pod that starts for any other reason
in between still gets the correct version.

## Next

- [Custom runtimes](/delivery/custom-runtimes/) — Modal, Baseten, anything else
- [Resolving a model](/delivery/resolve/)
- [Deployments](/guides/deployments/)
