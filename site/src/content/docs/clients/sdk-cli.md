---
title: SDK and CLI
description: Reference for the Python SDK and the lineage-cli command-line client — methods, commands, flags, environment variables and behaviour.
sidebar:
  order: 1
---

Both clients are thin layers over the [Model API](/api/conventions/). Neither authenticates:
authentication belongs to whatever sits in front of Lineage. Both send the actor as the
`X-Lineage-Actor` header. That name is fixed in the clients; if the server uses a different
`LINEAGE_ACTOR_HEADER`, your front door must set it.

## Python SDK

Package `lineage-sdk`, import name `lineage`. Python 3.10+, standard library only (`urllib`), so
it works in minimal training and serving images.

```bash
pip install ./sdk/python
```

```python
from lineage import Client

lin = Client("http://localhost:8081", actor="training-ci")

lin.publish(
    model="fraud-detector",
    version="1.4.0",
    artifacts=[lin.model_file("dist/model.onnx", format=("onnx", "1.16"))],
    lineage=[("trained_on", "s3://datasets/fraud-q3")],
    labels={"track": "canary"},
)
lin.transition("fraud-detector", "1.4.0", to="staging")
lin.transition("fraud-detector", "1.4.0", to="production", reason="passed shadow eval")

res = lin.resolve("fraud-detector", stage="production")
paths = lin.download("fraud-detector", stage="production", dest="./model")
```

`base_url` is the Model API origin, without `/v1`. Request paths are built from
`lineage/_openapi.py`, generated from the server's OpenAPI document by `make sdk`.

### Methods

The client exposes these signatures:

```python
Client(base_url, *, actor=None, timeout=60.0)
model_file(path, *, name=None, format=None, media_type=None)
artifact_uri(uri, *, name, kind="MODEL", format=None, storage_backend=None)
publish(*, model, version, artifacts=(), lineage=(), description=None, labels=None,
        auto_capture=True, idempotency_key=None)
transition(model, version, *, to, reason=None)
resolve(model, *, stage=None, version=None)
download(model, *, stage=None, version=None, dest, artifact=None, kind="MODEL")
lineage(model, version, *, direction="upstream", depth=3)
```

| Method | Returns | Does |
| --- | --- | --- |
| `Client` | | Sends `actor` as `X-Lineage-Actor` on every Model API request |
| `model_file` | `Artifact` | Local `MODEL` file to upload |
| `artifact_uri` | `Artifact` | Existing object to register by reference |
| `publish` | `dict` | See [publish](#publish) |
| `transition` | `dict` | `POST :transition`; returns the version |
| `resolve` | `Resolution` | See [resolve and download](#resolve-and-download) |
| `download` | `list[Path]` | See [resolve and download](#resolve-and-download) |
| `lineage` | `dict` | Graph traversal; `direction` is `upstream`, `downstream` or `both` |

`model_file`'s `name` defaults to the basename; `media_type` is guessed from the extension, else
`application/octet-stream`. In `resolve`, `stage` and `version` are exclusive (`ValueError`);
passing neither resolves `production`.

`format` is a `(name, version)` tuple; `version` may be `None`. For a `DOC` file, or to set
`service_account`, construct `lineage.Artifact(name=..., path=... | uri=..., kind=...,
model_format=..., media_type=..., storage_backend=..., service_account=...)` directly; exactly
one of `path` or `uri` is required.

### publish

`publish` runs four steps:

1. Creates the model with `Idempotency-Key: model:<model>`; an existing model is fine.
2. Creates the version with `Idempotency-Key: version:<model>@<version>` (or `idempotency_key`),
   registering all `uri` artifacts inline. `author` is set to the client's `actor`, if any.
3. Uploads each `path` artifact: initiate, then a signed `PUT`, multipart parts, or a
   stream-through `PUT` depending on the ticket, then finalize with the file's SHA-256.
4. Adds each `lineage` edge. A target containing `://` is a URI; anything else is a version name
   of the same model.

Returns `{"version": {...}, "artifacts": [...]}` with inline and uploaded artifacts combined.
The steps are separate requests: if an upload fails, the version exists in `draft` with the
artifacts that succeeded.

With `auto_capture=True`, `publish` also adds:

- A `produced_by` edge to `LINEAGE_RUN_URI` or `LINEAGE_RUN_ID` (prefixed `run://` if it has no
  scheme). Otherwise, if `git rev-parse HEAD` succeeds in the working directory, a `produced_by`
  edge to `git://<sha>`.
- A `derived_from` edge per entry in `LINEAGE_DERIVED_FROM`, comma-separated.

### resolve and download

`resolve` returns a `Resolution`; `download` resolves and writes files.

`Resolution` fields: `model`, `version`, `stage`, `digest`, `model_format`, `artifacts` (list of
dicts as on the wire), `raw` (the full response). `storage_uri` and `signed_url` are properties
of the first artifact.

`download` resolves once, then fetches every artifact of `kind` (default `MODEL`; pass
`kind=None` for all). `artifact="model.onnx"` selects one by name regardless of kind. Each file
comes from `signedUrl` when present, else from the `/content` endpoint. When the artifact has a
`digest`, the file's SHA-256 is checked against it and the file is deleted on mismatch. Artifact
names containing `/`, `\`, or equal to `.` or `..` are refused.

### Errors

Every non-2xx response raises `APIError` with `status`, `code`, `detail` and `details` from the
problem body. Network failures raise `APIError` with status `0` and code `network_error`.

The SDK raises two errors itself: `409 failed_precondition` when `download` selects no
artifacts, and `422 unprocessable` for a downloaded digest mismatch or an unsafe artifact name.

## CLI

`lineage-cli` is a Go client in `cmd/lineage-cli`, standard library only.

```bash
make cli    # builds bin/lineage-cli
```

Set `LINEAGE_SERVER` to the Model API origin (default `http://localhost:8081`). Set
`LINEAGE_ACTOR` to send it as `X-Lineage-Actor` on API requests (unset by default).

### Commands

Each command maps to one or a few Model API requests.

| Command | Required | Optional | Request |
| --- | --- | --- | --- |
| `model list` | | | `GET /v1/models` (first page) |
| `version publish` | `-m` `-n` `-a FILE` | `--format NAME:VERSION` | Create model and version, upload one file, finalize |
| `version promote` | `-m` `-n` `--to STAGE` | | `POST .../{version}:transition` |
| `resolve MODEL` | | `--stage` or `--version` | `GET .../resolve` |
| `pull MODEL` | `--dest DIR` | `--stage` `--version` `--kind` `--artifact NAME` | Resolve, then download matching artifacts |
| `lineage MODEL@VERSION` | | `--direction` | `GET .../lineage?direction=...` |

`-m` is the model and `-n` the version. `resolve` with neither `--stage` nor `--version` resolves
`production`. `pull` defaults to `--stage production` and `--kind MODEL`. `lineage` defaults to
`--direction upstream` and uses the server default depth of 3.

Flags accept one or two dashes. For `resolve`, `pull` and `lineage`, flags may come before or
after the operand.

```bash
export LINEAGE_ACTOR=ci@example.com
lineage-cli version publish -m fraud-detector -n 1.4.0 -a dist/model.onnx --format onnx:1.16
lineage-cli version promote -m fraud-detector -n 1.4.0 --to staging
lineage-cli resolve fraud-detector --stage staging | jq -r .version
lineage-cli pull fraud-detector --stage staging --dest ./model
lineage-cli lineage fraud-detector@1.4.0 --direction both
```

Behaviour:

- Output is the response JSON on one line, except `pull`, which prints each written path.
- `version publish` uploads one `MODEL` artifact named after the file's basename, using the same
  idempotency keys as the SDK (`model:<m>`, `version:<m>@<v>`). A `409` on model create is
  ignored; any other non-2xx exits.
- `version promote` has no reason flag.
- `pull` fetches every artifact of `--kind` (`--kind ""` for all), or only `--artifact`. It
  refuses unsafe names but does not verify digests; the SDK's `download` does.
- Errors print `lineage-cli: <detail>` and exit `1`. Bad usage prints the usage line and exits
  `2`.

Next: [Publishing](/api/publishing/) · [Resolve and fetch](/api/resolve/) · [API conventions](/api/conventions/)
