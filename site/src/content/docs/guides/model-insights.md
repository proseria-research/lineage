---
title: Model insights
description: Record what a version is made of — parameters, dtype, quantization, memory footprints, evaluations — with the provenance of every fact.
sidebar:
  order: 9
---

Insights are optional composition facts about a version: what framework produced it, how many
parameters it has, what dtype dominates, how much memory it needs, and how it scored.

Every field records **how the value was obtained**. A number without its provenance is not
actionable, so Lineage refuses to store one.

| Source | Meaning |
| --- | --- |
| `declared` | Asserted by the publisher. Attributed, not verified |
| `derived` | Computed by a producer from the model files |
| `measured` | Reported by a runtime or harness, valid within its stated basis |

## Writing facts

`PATCH` merges — the default write. Each call stamps its attribution onto exactly the fields
it touched, so two producers can contribute to one version without overwriting each other.

```bash
curl -XPATCH localhost:8081/v1/models/fraud-detector/versions/1.4.0/insight \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: insight-bot@example.com' \
  -d '{
    "schemaVersion": "1",
    "source": "derived",
    "reporter": "lineage-inspect",
    "reporterVersion": "0.4.1",
    "facts": {
      "framework": { "name": "onnxruntime", "version": "1.17" },
      "producer": { "name": "xgboost", "version": "2.0.3" },
      "paramCountTotal": 7241000,
      "paramCountMethod": "from_tensors",
      "tensorCount": 412,
      "dtypeDominant": "fp32",
      "diskBytes": 431717888,
      "weightsBytes": 428900000
    }
  }'
```

`PUT` replaces the whole document. Use it only when one producer owns every fact.

An unrecognised `schemaVersion` is rejected with `400` rather than partially applied — a
producer built against a contract the server does not know is a bug, not something to
half-accept.

`reporter` is the tool writing the record; `producer` is the library that wrote the model
files. They are different questions and both are worth knowing.

## Reading

```bash
curl -s localhost:8081/v1/models/fraud-detector/versions/1.4.0/insight
```

Each field comes back with its source and the reporter that supplied it, so you can tell a
publisher's claim from a measurement.

## Memory footprints

A footprint is what the model needs to run under a named scenario. Scenarios are upserted by
name, because "how much memory does it need" has no single answer.

```bash
curl -XPUT .../versions/1.4.0/footprints/serve-a10g-batch8 \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: capacity-bot@example.com' \
  -d '{
    "deviceClass": "a10g",
    "batch": 8,
    "seqLen": 2048,
    "weightsBytes": 428900000,
    "kvCacheBytes": 2147483648,
    "activationBytes": 536870912,
    "runtimeOverheadBytes": 1073741824,
    "totalBytes": 4186795520,
    "source": "measured",
    "basis": { "runtime": "vllm", "runtimeVersion": "0.7.2", "kvDtype": "fp16", "pagedAttention": true }
  }'
```

`basis` is **required when `source` is `estimated`**. An estimate without its assumptions
cannot be checked and cannot be compared to anything, so it is not accepted.

```bash
curl -s .../versions/1.4.0/footprints
```

## Evaluations

Append-only results. One row per metric per suite per split.

```bash
curl -XPOST .../versions/1.4.0/evaluations \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: eval-harness@example.com' \
  -d '{
    "suite": "fraud-holdout",
    "metric": "auc",
    "split": "2026-q1-holdout",
    "value": 0.947,
    "higherIsBetter": true,
    "nSamples": 1840000,
    "harnessName": "internal-evalkit",
    "harnessVersion": "3.2.0",
    "source": "measured",
    "runAt": 1785300000
  }'
```

`evidenceArtifactId` can point at an artifact holding the verbatim report, so a number always
has something behind it.

Gate promotions on these rather than on a person remembering:

```bash
auc=$(curl -s .../versions/1.4.0/evaluations \
      | jq '[.items[] | select(.metric=="auc")][0].value')
awk -v a="$auc" 'BEGIN { exit (a >= 0.94 ? 0 : 1) }' \
  && curl -XPOST .../versions/1.4.0:transition -d '{"to":"production","reason":"auc '"$auc"'"}'
```

## Insight in a resolution

Schedulers need one call, not three. Ask for the compact block:

```bash
curl -s "localhost:8081/v1/models/fraud-detector/resolve?stage=production&include=insight"
```

```json
{
  "insight": {
    "paramCount": 7241000,
    "dtype": "fp32",
    "diskBytes": 431717888,
    "minDeviceMemoryBytes": 4186795520,
    "minDeviceMemoryScenario": "serve-a10g-batch8",
    "source": "measured"
  }
}
```

`minDeviceMemoryBytes` always comes with the scenario it belongs to. A footprint without its
basis would let a scheduler pick a device class on a number that was never true for the
configuration it is about to run.

## Next

- [Comparing versions](/guides/version-diff/)
- [Resolving a model](/delivery/resolve/)
- [The lineage graph](/guides/lineage-graph/)
