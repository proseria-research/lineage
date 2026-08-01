---
title: CLI
description: lineage-cli — publish, promote, resolve, pull, and inspect lineage from a shell script.
sidebar:
  order: 2
---

A small Go binary for the things you do from a terminal or a CI step.

```bash
make cli          # builds ./bin/lineage-cli
```

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `LINEAGE_SERVER` | `http://localhost:8081` | Model API base URL |
| `LINEAGE_ACTOR` | — | Audit identity, sent as `X-Lineage-Actor` |

```bash
export LINEAGE_SERVER=https://models.internal
export LINEAGE_ACTOR=ci@example.com
```

## Commands

```
lineage-cli model list
lineage-cli version publish -m MODEL -n VERSION -a FILE
lineage-cli version promote -m MODEL -n VERSION --to STAGE
lineage-cli resolve MODEL [--stage STAGE]
lineage-cli pull MODEL --dest DIR [--kind KIND] [--artifact NAME]
lineage-cli lineage MODEL@VERSION
```

Every command prints JSON to stdout, so `jq` is the natural companion.

### List models

```bash
lineage-cli model list | jq -r '.items[].name'
```

### Publish

```bash
lineage-cli version publish -m fraud-detector -n 1.4.0 -a model.onnx
```

This does the whole flow: ensures the model exists, creates the version, computes the SHA-256
digest locally, requests an upload ticket, transfers the bytes in whichever mode the ticket
specifies — including multipart for large files — and finalizes.

Idempotency keys are namespaced by resource, so a retried publish replays rather than
colliding across models.

### Promote

```bash
lineage-cli version promote -m fraud-detector -n 1.4.0 --to staging
lineage-cli version promote -m fraud-detector -n 1.4.0 --to production
```

One stage at a time. Promoting to production archives the incumbent in the same transaction.

### Resolve

```bash
lineage-cli resolve fraud-detector --stage production
```

```bash
# Just the native URI of the first MODEL artifact
lineage-cli resolve fraud-detector \
  | jq -r '.artifacts[] | select(.kind=="MODEL") | .storageUri' | head -1
```

Flags parse either side of the operand — `resolve MODEL --stage x` and
`resolve --stage x MODEL` both work.

### Pull

```bash
lineage-cli pull fraud-detector --dest ./model
```

Downloads **every `MODEL` artifact** of the resolved version, mirroring what the `lineage://`
storage-initializer does. Sharded weights, config, and tokenizer are separate artifacts of
one version, so pulling only the first would leave an unusable directory.

```bash
lineage-cli pull fraud-detector --dest ./model --kind DOC        # model cards, licences
lineage-cli pull fraud-detector --dest ./model --artifact model.onnx
```

`DOC` artifacts stay out unless you ask for them by kind or by name.

Server-supplied artifact names are validated before being joined into a local path, so a bad
or compromised registry cannot write outside the destination directory.

### Lineage

```bash
lineage-cli lineage fraud-detector@1.4.0
```

## In a pipeline

```bash
#!/usr/bin/env bash
set -euo pipefail

export LINEAGE_SERVER=https://models.internal
export LINEAGE_ACTOR="ci@${CI_PROJECT}"

version="${BUILD_VERSION}"

lineage-cli version publish -m fraud-detector -n "$version" -a dist/model.onnx
lineage-cli version promote -m fraud-detector -n "$version" --to staging

if ./run-eval.sh "$version"; then
  lineage-cli version promote -m fraud-detector -n "$version" --to production
fi
```

## When to use the SDK instead

The CLI covers publish, promote, resolve, pull, and lineage. For everything else — insights,
evaluations, footprints, deployments, audit queries, diff — use the
[Python SDK](/clients/python-sdk/) or call the API directly.

## Next

- [Python SDK](/clients/python-sdk/)
- [API conventions](/clients/api-conventions/)
