---
title: Custom runtimes
description: Integrate Modal, Baseten, SageMaker, or your own serving code — resolve, fetch, verify, cache.
sidebar:
  order: 5
---

Anything that can make an HTTPS request can consume from Lineage. The integration is one
call and a download.

| System | Pattern |
| --- | --- |
| **KServe** (native) | Resolve, put `storageUri` in the `InferenceService` |
| **KServe** (`lineage://`) | Stage-referenced, resolved at pull time — see [KServe](/delivery/kserve/) |
| **Modal** | `resolve()` → `signedUrl` → download into a Volume at build or startup |
| **Baseten (Truss)** | `resolve()` → `signedUrl` during build or deploy |
| **SageMaker / Vertex** | Resolve, then point the endpoint at the native `storageUri` |
| **Anything else** | `signedUrl`, or the `/content` broker |

## The shape of every integration

1. `resolve` the model at a stage.
2. Take every artifact with `kind: "MODEL"`.
3. Download by `storageUri` if the runtime has storage credentials, else by `signedUrl`.
4. Verify each file against its `digest`.
5. Cache locally keyed by digest.

## Modal

Pull at container build time so the weights are baked into the image, or at startup into a
Volume when they change more often than the image does.

```python
import modal
from lineage import Client

image = modal.Image.debian_slim().pip_install("lineage-sdk")
volume = modal.Volume.from_name("models", create_if_missing=True)
app = modal.App("fraud-detector")


@app.function(image=image, volumes={"/models": volume})
def refresh():
    client = Client("http://lineage.internal:8081", actor="modal")
    client.download("fraud-detector", stage="production", dest="/models/fraud-detector")
    volume.commit()
```

Run `refresh` on a schedule, or trigger it from CI after a promotion. Because the download is
digest-verified and digest-cached, a run that finds nothing new is close to free.

## Baseten (Truss)

Resolve during the build step and write the files into the Truss data directory:

```python
# bin/fetch_model.py — run from the Truss build
import os
from lineage import Client

client = Client(os.environ["LINEAGE_ENDPOINT"], actor="truss-build")
resolution = client.resolve("fraud-detector", stage="production")
client.download("fraud-detector", version=resolution.version, dest="data/model")

print(f"baked {resolution.version} ({resolution.digest})")
```

Print the version and digest into the build log. When an endpoint misbehaves later, that line
is the fastest way to establish what it is actually running.

## Plain Python, no SDK

```python
import hashlib, urllib.request, json, pathlib

base = "http://lineage.internal:8081"
with urllib.request.urlopen(f"{base}/v1/models/fraud-detector/resolve?stage=production") as r:
    resolution = json.load(r)

dest = pathlib.Path("./model")
dest.mkdir(parents=True, exist_ok=True)

for artifact in resolution["artifacts"]:
    if artifact.get("kind") != "MODEL":
        continue
    target = dest / artifact["name"]
    urllib.request.urlretrieve(artifact["signedUrl"], target)

    digest = hashlib.sha256(target.read_bytes()).hexdigest()
    expected = artifact["digest"].removeprefix("sha256:")
    if digest != expected:
        raise SystemExit(f"digest mismatch on {artifact['name']}")
```

## Shell

```bash
resolve() {
  curl -sf "$LINEAGE/v1/models/$1/resolve?stage=${2:-production}"
}

pull() {
  resolve "$1" "$2" | jq -r '.artifacts[] | select(.kind=="MODEL") | [.name, .signedUrl] | @tsv' \
    | while IFS=$'\t' read -r name url; do
        curl -sfL "$url" -o "$3/$name"
      done
}
```

Or skip the loop entirely:

```bash
lineage-cli pull fraud-detector --dest ./model
```

## Watching for promotions

Poll `resolve` with the last `ETag` and act only on a `200`:

```bash
while :; do
  code=$(curl -s -o /tmp/res.json -w '%{http_code}' \
    -H "If-None-Match: $etag" \
    "$LINEAGE/v1/models/fraud-detector/resolve?stage=production")
  if [ "$code" = 200 ]; then
    etag=$(jq -r '.versionId' /tmp/res.json)   # or read the ETag response header
    reload_model /tmp/res.json
  fi
  sleep 30
done
```

A poller sitting on `304` costs essentially nothing. See
[Caching and invalidation](/delivery/caching/).

## Rules that keep integrations honest

- **Never hardcode a version.** That is what stages are for. Pin only when you mean it.
- **Never cache a signed URL.** They expire. Cache the bytes, keyed by digest.
- **Always verify the digest.** It is the only thing standing between you and a truncated
  download.
- **Always take every `MODEL` artifact.** One file is rarely a model.
- **Set an actor.** `X-Lineage-Actor` on the resolve tells the audit trail which system
  pulled what.

## Next

- [Resolving a model](/delivery/resolve/)
- [Python SDK](/clients/python-sdk/)
- [Deployments](/guides/deployments/)
