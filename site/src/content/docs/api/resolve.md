---
title: Resolve and fetch
description: Turn a model and a stage or version into native storage URIs, signed URLs and digests, and fetch the bytes.
sidebar:
  order: 3
---

Consumers make one call to learn what to load, then download from storage directly.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/v1/models/{model}/resolve` | Select a version; return its artifacts as native and signed refs |
| `GET` | `/v1/models/{model}/versions/{version}/artifacts/{artifact}/content` | Broker the bytes: `302` to a signed URL, or stream |

## Selectors

A selector picks which version to resolve.

| Query | Selects |
| --- | --- |
| `version=1.4.0` | That exact version, any stage |
| `label.<key>=<value>` | Newest version with that label (not in the OpenAPI document) |
| `stage=staging` | Newest-created version in that stage |
| none | `stage=production` |

Precedence is `version`, then `label.*`, then `stage`. Add `include=insight` for a compact
composition block (`paramCount`, `dtype`, `diskBytes`, `minDeviceMemoryBytes`,
`minDeviceMemoryScenario`, `source`), omitted when nothing was recorded.

| Outcome | Status |
| --- | --- |
| Match | `200 Resolution` |
| `If-None-Match` matches | `304` |
| Unknown model, or unknown `version` | `404 not_found` |
| No version in the stage, or no label match | `409 failed_precondition`, detail `no version matches selector` |

## Response

A resolution names the version and lists each artifact with its native and signed locations.

```bash
curl 'localhost:8081/v1/models/fraud-detector/resolve?stage=production' \
  -H 'X-Lineage-Actor: kserve'
```

```json
{
  "model": "fraud-detector",
  "versionId": "01K5Z8Q2M4…",
  "version": "1.4.0",
  "stage": "production",
  "digest": "sha256:9f2c…",
  "modelFormat": {"name": "onnx", "version": "1.16"},
  "artifacts": [
    {
      "name": "model.onnx",
      "kind": "MODEL",
      "storageUri": "s3://models/fraud-detector/1.4.0/model.onnx",
      "signedUrl": "https://models.s3.amazonaws.com/fraud-detector/1.4.0/model.onnx?X-Amz-Algorithm=AWS4-HMAC-SHA256&…",
      "signedUrlExpiresAt": 1790008100000,
      "sizeBytes": 48213377,
      "digest": "sha256:9f2c…",
      "mediaType": "application/octet-stream"
    }
  ],
  "resolvedAt": 1790007200000
}
```

| Field | Meaning |
| --- | --- |
| `version`, `versionId`, `stage` | The selected version |
| `digest`, `modelFormat` | From the first `MODEL` artifact by name |
| `artifacts[]` | Every artifact of the version, `MODEL` and `DOC`, ordered by name |
| `artifacts[].storageUri` | The artifact's `uri`, verbatim |
| `artifacts[].signedUrl`, `signedUrlExpiresAt` | Present only when the backend can sign |
| `artifacts[].serviceAccount` | Copied from the artifact, for runtimes that pull with their own credentials |
| `ociImage` | Pullable `oci://registry/repo:tag` when all `MODEL` artifacts share one OCI manifest |
| `insight` | Only with `include=insight` |
| `resolvedAt` | Epoch-millis |

## Storage URIs and signed URLs

What you get in `storageUri` and `signedUrl` depends on the backend.

| Backend | `storageUri` | `signedUrl` |
| --- | --- | --- |
| `s3` | `s3://<bucket>/<key>` | SigV4 presigned `GET`, valid 15 minutes |
| `oci` | `oci://<registry>/<repo>:<tag>#<layer>` | The registry's absolute blob redirect, when it issues one; otherwise absent |
| `fs` | `file://<path>` | Absent |
| By reference, other schemes | As registered (`gs://`, `hf://`, …) | Absent unless the configured backend can sign that URI |

Signed URLs are minted per response and never cached. Do not store them; store bytes keyed by
`digest`. Without a `signedUrl`, use `storageUri` with your own credentials or the content
endpoint below.

## Content endpoint

`/content` fetches one artifact's bytes when you cannot or do not want to use the storage URI.

```bash
curl -L -o model.onnx \
  localhost:8081/v1/models/fraud-detector/versions/1.4.0/artifacts/model.onnx/content
```

| Case | Response |
| --- | --- |
| Backend can sign (default) | `302` to a fresh signed URL |
| Backend cannot sign, signing fails, or `?mode=stream` | `200` with the bytes through the Model API |
| Streamed, `If-None-Match` equals `"<digest>"` | `304` |
| Streamed from `fs` with `Range` | `206` |
| Unknown artifact | `404 not_found` |

Streamed responses set `ETag: "<digest>"` and `Content-Type` from `mediaType`. `fs` supports
`Range`; other backends stream the whole object with `Content-Length`.

## Caching

Poll cheaply with ETags; the server also caches selections.

`resolve` returns `ETag: "<digest>"` and `Cache-Control: private, no-cache`. Send it back in
`If-None-Match` to get `304` while the selection is unchanged.

```bash
curl -sD- -o /dev/null 'localhost:8081/v1/models/fraud-detector/resolve?stage=production' | grep -i '^etag'
curl -s -o /dev/null -w '%{http_code}\n' -H 'If-None-Match: "sha256:9f2c…"' \
  'localhost:8081/v1/models/fraud-detector/resolve?stage=production'   # 304
```

:::caution
The ETag is the first `MODEL` artifact's digest, not the version. A promotion to a version
with identical first `MODEL` bytes still returns `304`, and a resolution with no digest has
no ETag.
:::

Server side, the selection (without signed URLs) is cached per model and selector. Any
publish, transition, edit or delete on the model invalidates its entries; a 60-second TTL
backstops a missed event. Multi-replica installs need the Redis cache for invalidation to
reach every replica; see [Configuration](/operate/configuration/).

## KServe: `lineage://`

`lineage-init` (`cmd/lineage-init`, image built from `Dockerfile.init`) is a KServe
storage initializer. Reference a stage, and a promotion takes effect on the next pod start
with no manifest change.

```text
lineage://<model>[/<stage>][@<version>][#<artifact>]
```

- `lineage://fraud-detector`: every `MODEL` artifact of the production version.
- `lineage://fraud-detector/staging`: every `MODEL` artifact of the newest staging version.
- `lineage://fraud-detector@1.4.0`: every `MODEL` artifact of 1.4.0.
- `lineage://fraud-detector/production#model.onnx`: that one artifact.

Register the scheme once per cluster, then use it as `storageUri`:

```yaml
apiVersion: serving.kserve.io/v1alpha1
kind: ClusterStorageContainer
metadata:
  name: lineage
spec:
  container:
    name: storage-initializer
    image: ghcr.io/proseria-research/lineage-init:<tag>
    env:
      - {name: LINEAGE_ENDPOINT, value: "http://lineage-model-api:8081"}
      - {name: LINEAGE_ACTOR, value: "kserve"}
  supportedUriFormats:
    - prefix: lineage://
---
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: fraud-detector
spec:
  predictor:
    model:
      modelFormat: {name: onnx}
      storageUri: lineage://fraud-detector/production
```

The init container reads two env vars:

- `LINEAGE_ENDPOINT`: Model API base URL. Default `http://lineage-model-api:8081`.
- `LINEAGE_ACTOR`: sent as `X-Lineage-Actor` (fixed name, not `LINEAGE_ACTOR_HEADER`). No
  default.

It downloads via `signedUrl` when present, else the content endpoint; skips `DOC` artifacts
unless named in `#`; refuses artifact names containing path separators; and gives up after
10 minutes. It does not verify digests. Running pods keep what they pulled: restart the
`InferenceService` after promoting to roll out. Debug a pull by running it by hand:
`lineage-init lineage://fraud-detector/production /tmp/models`.

## Custom runtimes

Modal, Baseten, SageMaker or your own server follow the same steps:

1. `resolve` by stage, never by a hardcoded version.
2. Take every `kind: "MODEL"` artifact.
3. Download from `signedUrl`, from `storageUri` with runtime credentials, or from `/content`.
4. Verify each file's sha256 against `digest`.
5. Cache by digest; poll `resolve` with `If-None-Match` and reload only on `200`.

```python
import hashlib, json, pathlib, urllib.request

base = "http://localhost:8081"
req = urllib.request.Request(f"{base}/v1/models/fraud-detector/resolve?stage=production",
                             headers={"X-Lineage-Actor": "modal-worker"})
res = json.load(urllib.request.urlopen(req))
dest = pathlib.Path("model"); dest.mkdir(exist_ok=True)
for a in (a for a in res["artifacts"] if a["kind"] == "MODEL"):
    src = a.get("signedUrl") or f"{base}/v1/models/{res['model']}/versions/{res['version']}/artifacts/{a['name']}/content"
    path = dest / a["name"]
    urllib.request.urlretrieve(src, path)
    got = "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()
    if a.get("digest") and got != a["digest"]:
        raise SystemExit(f"digest mismatch: {a['name']}")
```

The [SDK and CLI](/clients/sdk-cli/) wrap this loop.

Next: [Promotion](/api/promotion/) · [Storage backends](/operate/storage/) · [SDK and CLI](/clients/sdk-cli/)
