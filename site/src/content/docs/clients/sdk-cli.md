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

| Method | Returns | Does |
| --- | --- | --- |
| `Client(base_url, *, actor=None, timeout=60.0)` | | `actor` is sent as `X-Lineage-Actor` on every Model API request |
| `model_file(path, *, name=None, format=None, media_type=None)` | `Artifact` | Local `MODEL` file to upload. `name` defaults to the basename; `media_type` is guessed from the extension, else `application/octet-stream` |
| `artifact_uri(uri, *, name, kind="MODEL", format=None, storage_backend=None)` | `Artifact` | Existing object to register by reference |
| `publish(*, model, version, artifacts=(), lineage=(), description=None, labels=None, auto_capture=True, idempotency_key=None)` | `dict` | See below |
| `transition(model, version, *, to, reason=None)` | `dict` | `POST :transition`; returns the version |
| `resolve(model, *, stage=None, version=None)` | `Resolution` | `stage` and `version` are exclusive (`ValueError`). Neither resolves `production` |
| `download(model, *, stage=None, version=None, dest, artifact=None, kind="MODEL")` | `list[Path]` | See below |
| `lineage(model, version, *, direction="upstream", depth=3)` | `dict` | Graph traversal: `direction` is `upstream`, `downstream` or `both` |

`format` is a `(name, version)` tuple; `version` may be `None`. For a `DOC` file, or to set
`service_account`, construct `lineage.Artifact(name=..., path=... | uri=..., kind=...,
model_format=..., media_type=..., storage_backend=..., service_account=...)` directly; exactly
one of `path` or `uri` is required.

### publish

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

| Source | Edge |
| --- | --- |
| `LINEAGE_RUN_URI` or `LINEAGE_RUN_ID` (prefixed `run://` if it has no scheme) | `produced_by` |
| Otherwise `git rev-parse HEAD` in the working directory, if it succeeds | `produced_by` `git://<sha>` |
| `LINEAGE_DERIVED_FROM`, comma-separated | `derived_from` per entry |

### resolve and download

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

| Raised by the SDK itself | `status` | `code` |
| --- | --- | --- |
| `download` selects no artifacts | 409 | `failed_precondition` |
| Downloaded digest mismatch, unsafe artifact name | 422 | `unprocessable` |

## CLI

`lineage-cli` is a Go client in `cmd/lineage-cli`, standard library only.

```bash
go build -o bin/lineage-cli ./cmd/lineage-cli
```

:::caution
`make cli` currently writes the CLI to `bin/lineage`, overwriting the server binary that
`make build` produces. Build with the command above instead.
:::

| Variable | Default | Effect |
| --- | --- | --- |
| `LINEAGE_SERVER` | `http://localhost:8081` | Model API origin |
| `LINEAGE_ACTOR` | unset | Sent as `X-Lineage-Actor` on API requests |

### Commands

| Command | Flags | Request |
| --- | --- | --- |
| `model list` | | `GET /v1/models` (first page) |
| `version publish` | `-m MODEL` `-n VERSION` `-a FILE` (required), `--format NAME:VERSION` | Create model, create version, upload one file, finalize |
| `version promote` | `-m MODEL` `-n VERSION` `--to STAGE` (required) | `POST .../{version}:transition` |
| `resolve MODEL` | `--stage STAGE` or `--version VERSION` | `GET .../resolve`; neither resolves `production` |
| `pull MODEL` | `--dest DIR` (required), `--stage` (default `production`), `--version`, `--kind` (default `MODEL`), `--artifact NAME` | Resolve, then download matching artifacts |
| `lineage MODEL@VERSION` | `--direction` (default `upstream`) | `GET .../lineage?direction=...` at server default depth 3 |

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
