# 10 — SDK & CLI

> Status: **Implemented**. Client ergonomics over the Model API (`/v1`). The Python SDK
> provides the ergonomic producer/consumer layer; the Go CLI covers common terminal and CI
> flows. `sdk/generate.py` derives their shared operation manifest from the OpenAPI contract.
> APIs are `03`/`04`.

## 1. Principles

- **OpenAPI is the source.** A generated low-level client (from `/v1/openapi.json`) plus
  a thin hand-written ergonomic layer. The contract never drifts from the clients.
- **No auth in the client** (§00 axiom 4) — configure a base URL; infra handles authN.
  An optional actor value is passed as `X-Lineage-Actor` for audit.
- **Publish and consume are the two hot paths** — both must be one-liners.

```mermaid
flowchart LR
    sdk["Python SDK"] --> api["Model API /v1"]
    cli["Go CLI (lineage)"] --> api
    init["KServe lineage:// initializer"] --> api
    api --> core["core"]
```

## 2. Python SDK

```python
from lineage import Client
lin = Client("https://lineage.internal", actor="ci-bot")

# publish (uploads bytes via signed URL, or registers a uri) — §03/§05
v = lin.publish(
    model="fraud-detector", version="1.4.0",
    artifacts=[lin.model_file("model.onnx", format=("onnx","1.16"))],
    lineage=[("derived_from", "bert-base"), ("trained_on", "s3://datasets/q2")],
)

lin.transition("fraud-detector", "1.4.0", to="production")   # §03.7

# consume — §04
r = lin.resolve("fraud-detector", stage="production")
print(r.storage_uri, r.signed_url, r.digest, r.model_format)
paths = lin.download("fraud-detector", stage="production", dest="./model")  # Modal/Baseten
```

- `publish` picks upload vs register per artifact, handles multipart + digesting, records
  lineage edges (`07`), and returns the version with every artifact it created.
- `download` writes every `MODEL` artifact of the resolution and returns their paths — the
  same selection the `lineage://` initializer makes (`04.5`). Pass `artifact="name"` for one
  file, or `kind=None` to include `DOC` artifacts too.
- `resolve`/`download` wrap the resolution contract (`04.2`).
- **Auto-capture:** in a detectable training context, the SDK fills `produced_by`
  (git SHA / run id) and `derived_from` automatically (`07.4`).

## 3. Go CLI (`lineage`)

```bash
export LINEAGE_SERVER=https://lineage.internal
lineage model list
lineage version publish -m fraud-detector -n 1.4.0 \
        -a model.onnx --format onnx:1.16
lineage version promote -m fraud-detector -n 1.4.0 --to production   # → :transition
lineage resolve fraud-detector --stage production -o json
lineage pull fraud-detector --stage production --dest ./model        # mirrors lineage://
lineage lineage fraud-detector@1.4.0 --direction upstream            # §07
```

- Ships in the same repo/binary family (Go); talks Model API `/v1`.
- `pull` performs the same resolve→fetch the KServe initializer does (`04.5`), for local
  and CI use.

## 4. Build & Contract Check

```bash
make sdk-check                 # regenerate and validate the Python SDK manifest
make cli                       # builds bin/lineage-cli (the Go CLI)
```

`sdk/generate.py` emits `lineage/_openapi.py`: `API_VERSION` plus a `PATHS` table mapping
every `operationId` to its `(method, path template)`. The ergonomic layer builds **every**
URL through `PATHS`, so the manifest is load-bearing rather than advisory — an operation or
path that moves in the contract fails at generation time instead of surfacing as a runtime
404. Generation also fails if the contract drops an operation the SDK depends on.

### 4.1 Upload modes

`initiateUpload` returns one of three ticket shapes, and clients must handle all three
(§05.6):

| Ticket | Condition | Client action |
|---|---|---|
| `url` + `method` | signing backend, `< 64 MiB` | PUT the file to the signed URL |
| `multipart` + `parts` + `partSize` | signing backend, `≥ 64 MiB` | PUT each part, finalize with per-part ETags |
| `streamThrough` + `contentUrl` | non-signing backend (e.g. `fs`) | PUT the bytes to `contentUrl` |

`contentUrl` is **server-relative** and must be resolved against the Model API origin; the
other two are absolute. Both clients stream file bodies rather than buffering them.

### 4.2 Artifact selection

`download` (SDK), `pull` (CLI) and `lineage-init` (§04.5) make the **same** selection: every
`MODEL` artifact of the resolved version, `DOC` excluded. Taking only the first artifact
breaks sharded models; including `DOC` puts model cards in a serving directory.

```bash
lineage pull fraud-detector --dest ./model                      # all MODEL artifacts
lineage pull fraud-detector --dest ./model --artifact model.onnx  # one by name
lineage pull fraud-detector --dest ./model --version 1.4.0       # an exact version
lineage pull fraud-detector --dest ./model --kind ""              # everything
```

## 5. Versioning & Compat

- Clients are versioned with the API; generated code is regenerated per release.
- Backward-compatible `/v1` changes are additive; breaking changes ⇒ `/v2` (clients
  target a major).

## 6. See Also

| For | Doc |
|---|---|
| Publish / lifecycle / errors | `03` |
| Resolve / download / `lineage://` | `04` |
| Upload internals the SDK wraps | `05` |
| Lineage relations auto-captured | `07` |
