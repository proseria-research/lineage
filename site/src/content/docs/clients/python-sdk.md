---
title: Python SDK
description: Publish, promote, resolve, and download from Python — with uploads, digest verification, and automatic provenance capture.
sidebar:
  order: 1
---

The SDK is generated against the OpenAPI spec, with an ergonomic layer on top that collapses
the multi-step flows — publishing with uploads, downloading a whole version — into one call.

```bash
pip install lineage-sdk
```

```python
from lineage import Client

client = Client("http://localhost:8081", actor="you@example.com")
```

`base_url` is the Model API origin. `actor` is optional audit attribution, forwarded as the
trusted `X-Lineage-Actor` header. The SDK never authenticates — that is your infrastructure's
job.

## Publish

One call creates the model if it does not exist, publishes the version, uploads every local
file, and records lineage edges.

```python
from lineage import Client

client = Client("http://localhost:8081", actor="ci@example.com")

client.publish(
    model="fraud-detector",
    version="1.4.0",
    artifacts=[
        client.model_file("model.onnx", format=("onnx", "1.16")),
        client.model_file("tokenizer.json"),
        client.artifact_uri(
            "s3://models/fraud-detector/1.4.0/card.md",
            name="model-card.md",
            kind="DOC",
        ),
    ],
    lineage=[
        ("trained_on", "s3://data/transactions-2026-q1/"),
        ("derived_from", "fraud-detector@1.2.0"),
    ],
    description="Quarterly retrain",
    labels={"retrain": "2026-q1"},
    idempotency_key="publish-fraud-detector-1.4.0",
)
```

`model_file` uploads a local path. `artifact_uri` registers bytes that already exist
somewhere. An `Artifact` needs exactly one of the two.

Uploads pick the right mode automatically — signed direct, multipart for large files, or
stream-through when the backend cannot sign — and parts are read one at a time, so peak
memory is one part rather than the whole file.

### Automatic provenance

`auto_capture=True` (the default) adds edges the SDK can work out for itself:

| Edge | Source |
| --- | --- |
| `produced_by` | `LINEAGE_RUN_URI` or `LINEAGE_RUN_ID`, else `git://<HEAD sha>` |
| `derived_from` | `LINEAGE_DERIVED_FROM`, comma-separated |

Setting `LINEAGE_RUN_URI` in your pipeline means every publish records which run made it,
without anyone remembering to pass it. Pass `auto_capture=False` to turn it off.

## Promote

```python
client.transition("fraud-detector", "1.4.0", to="staging")
client.transition("fraud-detector", "1.4.0", to="production",
                  reason="shadow eval green for 48h")
```

## Resolve

```python
resolution = client.resolve("fraud-detector", stage="production")

resolution.version       # "1.4.0"
resolution.stage         # "production"
resolution.digest        # "sha256:9f2c4a10..."
resolution.model_format  # {"name": "onnx", "version": "1.16"}
resolution.artifacts     # the full list
resolution.storage_uri   # first artifact's native URI
resolution.signed_url    # first artifact's signed HTTPS URL
resolution.raw           # the untouched response
```

`stage` and `version` are mutually exclusive — passing both raises `ValueError`.

## Download

```python
files = client.download("fraud-detector", stage="production", dest="./model")
```

By default this fetches **every `MODEL` artifact** into `dest` and returns the paths written.
That default is deliberate: sharded weights, config, and tokenizer are separate artifacts of
one version, and taking only the first silently produces an unusable model directory.

```python
# Everything, including DOC artifacts
client.download("fraud-detector", stage="production", dest="./model", kind=None)

# One named artifact, whatever its kind
client.download("fraud-detector", stage="production", dest="./model", artifact="model.onnx")

# A pinned version
client.download("fraud-detector", version="1.4.0", dest="./model")
```

Each file's digest is verified when the resolution declares one. A mismatch deletes the
partial file and raises `APIError`.

Artifact names are validated before being joined into a local path, so a compromised registry
cannot make the client write outside `dest`.

## Lineage

```python
graph = client.lineage("fraud-detector", "1.4.0", direction="upstream", depth=3)

for node in graph["nodes"]:
    print(node["depth"], node.get("ref") or f"{node['model']}@{node['version']}")
```

`direction` is `upstream`, `downstream`, or `both`.

## Errors

```python
from lineage import APIError

try:
    client.transition("fraud-detector", "1.4.0", to="production")
except APIError as err:
    err.status   # 409
    err.code     # "failed_precondition"
    err.detail   # "cannot move from draft to production"
    err.details  # {"from": "draft", "to": "production", "allowed": [...]}
```

`err.code` is the stable thing to branch on. Statuses map from it, not the other way around.

## A CI publish step

```python
import os
from lineage import Client

client = Client(os.environ["LINEAGE_ENDPOINT"], actor=os.environ["CI_ACTOR"])
version = os.environ["BUILD_VERSION"]

client.publish(
    model="fraud-detector",
    version=version,
    artifacts=[client.model_file("dist/model.onnx", format=("onnx", "1.16"))],
    idempotency_key=f"publish-fraud-detector-{version}",
)
client.transition("fraud-detector", version, to="staging")
```

The idempotency key makes the step safe to retry. See
[Idempotency and retries](/guides/idempotency/).

## Regenerating

The OpenAPI document is the source of truth. After an API change:

```bash
make sdk         # regenerate from the spec
make sdk-check   # fail if the committed SDK is stale
```

## Next

- [CLI](/clients/cli/)
- [API conventions](/clients/api-conventions/)
- [Custom runtimes](/delivery/custom-runtimes/)
