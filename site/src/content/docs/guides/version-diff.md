---
title: Comparing versions
description: Diff two versions and get a verdict — identical, reweighted, recast, rescaled, or rearchitected — with an honest account of what the facts could not settle.
sidebar:
  order: 10
---

"What changed between 1.3.0 and 1.4.0" usually gets answered by reading a commit message.
Diff answers it from the models themselves.

## Diff two versions

Within one model:

```bash
curl -s "localhost:8081/v1/models/fraud-detector/diff?from=1.3.0&to=1.4.0"
```

Across models — comparing a quantized variant to its parent, for instance:

```bash
curl -s "localhost:8081/v1/diff?from=fraud-detector@1.4.0&to=fraud-detector-lite@0.1.0"
```

## The verdict

| Verdict | Means |
| --- | --- |
| `identical` | Same architecture, same weights |
| `reweighted` | Same architecture, different weights — a retrain |
| `recast` | Same weights, different dtype — a quantization or cast |
| `rescaled` | Same architecture family, different size |
| `rearchitected` | The structure itself changed |
| `unknown` | The recorded facts do not settle it |

```json
{
  "from": { "model": "fraud-detector", "version": "1.3.0" },
  "to":   { "model": "fraud-detector", "version": "1.4.0" },
  "verdict": "reweighted",
  "hashes": {
    "architecture": { "from": "sha256:11ab…", "to": "sha256:11ab…", "equal": true },
    "weights":      { "from": "sha256:77cd…", "to": "sha256:90ef…", "equal": false }
  }
}
```

## When it cannot decide

The interesting part of the response is what it admits.

```json
{
  "verdict": "unknown",
  "candidates": ["reweighted", "recast"],
  "missing": ["dtype-normalised weights hash"]
}
```

- **`candidates`** — the facts narrowed the answer without settling it. These are the verdicts
  still standing.
- **`missing`** — the hash levels the verdict needed and nobody submitted.

`missing` is a to-do list for your producers. Record those hashes on future publishes and the
same comparison resolves next time.

## The architecture fingerprint

The fingerprint is a hash over a canonical form of the model's structure, independent of
weight values. Two versions with the same fingerprint have the same architecture, whatever
their weights say.

Fingerprints have a cost tier — computing per-tensor digests over a large model is not free —
so producers choose how deep to go. Deeper hashing settles more verdicts; the diff tells you
when the depth you chose was not enough.

A fingerprint is only as good as the producer that computed it. Diff never claims more
confidence than the recorded facts support, which is why `unknown` is a first-class answer
rather than a fallback to a guess.

## Using it

**In review.** Attach the diff to the promotion request. `reweighted` on a version described
as "config change only" is a conversation worth having before it reaches production.

**In incident response.** `identical` between a suspect version and its predecessor rules out
the model as the cause immediately.

**Across a fleet.** Diff a fine-tune against its base to confirm what actually changed, rather
than trusting a `base_model` string somebody typed.

## Next

- [Model insights](/guides/model-insights/) — recording the facts diff reads
- [The lineage graph](/guides/lineage-graph/)
