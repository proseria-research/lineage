---
title: Deployments
description: Record where a version is served so impact analysis can reach live endpoints.
sidebar:
  order: 8
---

A deployment records that a version is — or was — being served somewhere. It is what turns
downstream traversal from an academic exercise into an incident tool: a bad dataset walks
through the versions that used it and lands on the endpoints serving them.

| Field | Meaning |
| --- | --- |
| `environment` | Required. `prod-eu-1`, `staging`, `edge-store-412` |
| `endpointUri` | Where it answers — `https://…`, `grpc://…` |
| `status` | `ACTIVE` or `INACTIVE` |
| `externalRef` | Back-pointer to whatever actually runs it |

## Record one

```bash
curl -XPOST localhost:8081/v1/models/fraud-detector/versions/1.4.0/deployments \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: deployer@example.com' \
  -d '{
    "environment": "prod-eu-1",
    "endpointUri": "https://infer.eu.example.com/v2/models/fraud-detector",
    "status": "ACTIVE",
    "externalRef": "kserve://ns/risk/inferenceservices/fraud-detector"
  }'
```

Creating a deployment also creates the `deployed_as` lineage edge from the version, so it
shows up in downstream traversals immediately.

`externalRef` is the field that makes this actionable. Put the identifier your platform uses
in it — the InferenceService, the Modal app, the Baseten deployment id — so that a downstream
query hands an operator something they can act on rather than a URL they have to go and
look up.

## List and update

```bash
curl -s localhost:8081/v1/models/fraud-detector/versions/1.4.0/deployments
```

Mark one inactive when it is drained rather than deleting it — the historical record of where
a version ran is usually the interesting part:

```bash
curl -XPATCH .../deployments/01JQ9B... \
  -H 'Content-Type: application/json' -H 'X-Lineage-Actor: deployer@example.com' \
  -d '{"status":"INACTIVE"}'
```

```bash
curl -XDELETE .../deployments/01JQ9B... -H 'X-Lineage-Actor: deployer@example.com'
```

## Who writes them

Whatever performs the rollout — a CD pipeline step, a controller reconciling
`InferenceService` objects, or the deployment job itself. The registry does not discover
deployments; it records what you tell it.

A reasonable pattern is one call at the end of a successful rollout:

```bash
lineage_deployment() {
  curl -sf -XPOST "$LINEAGE/v1/models/$1/versions/$2/deployments" \
    -H 'Content-Type: application/json' -H "X-Lineage-Actor: $CI_ACTOR" \
    -d "{\"environment\":\"$3\",\"endpointUri\":\"$4\",\"status\":\"ACTIVE\",\"externalRef\":\"$5\"}"
}
```

## Using them

```bash
# Everything serving off this version right now
curl -s ".../versions/1.4.0/lineage?direction=downstream&depth=1&relations=deployed_as"

# Every endpoint that inherited a suspect dataset, however indirectly
curl -s ".../versions/1.4.0/lineage?direction=downstream&depth=5&relations=derived_from,deployed_as"
```

## Next

- [The lineage graph](/guides/lineage-graph/)
- [Resolving a model](/delivery/resolve/)
- [KServe](/delivery/kserve/)
