---
title: Observability and security
description: Health probes, Prometheus metrics, traces and logs on the ops port, and the security model you have to enforce around Lineage.
sidebar:
  order: 4
---

## Ops listener

The ops surface listens on `LINEAGE_METRICS_ADDR` (default `:9090`) and serves three routes.
It is unauthenticated; keep it inside the cluster.

| Route | Returns | Checks |
| --- | --- | --- |
| `GET /healthz` | Always `200`, JSON | Only that the process answers |
| `GET /readyz` | `200 ready`, or `503` with the error text | Metadata store and storage backend |
| `GET /metrics` | Prometheus text format `0.0.4` | Nothing |

`/readyz` checks the metadata store by listing one model, and storage with a `Stat` of
`readyz-probe/.keep` on the active backend; "not found" counts as healthy.

`/healthz` also reports the governance settings the process is running under:

```json
{
  "status": "ok",
  "retention": { "minAuditAgeDays": 3650, "minArchivedVersionDays": 3650 },
  "auditAttestation": { "enabled": true, "sealIntervalSeconds": 60, "sealGraceSeconds": 5 }
}
```

`/readyz` does not check Redis. Redis is pinged once at startup, and an unreachable Redis
stops the process from starting.

:::caution[oci and /readyz]
The readiness probe passes the bare path `readyz-probe/.keep` to the backend. The `oci`
driver accepts only `oci://` URIs, so with `LINEAGE_STORAGE_DRIVER=oci` the storage check
returns an error and `/readyz` answers `503`.
:::

## Metrics

Lineage exports its own metrics on `/metrics`. All metrics come from a built-in registry; there is no Go runtime or process collector.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `lineage_http_requests_total` | counter | `surface`, `route`, `method`, `status` | HTTP requests (RED) |
| `lineage_http_request_duration_seconds` | histogram | `surface`, `route`, `method` | HTTP request duration (RED) |
| `lineage_resolve_cache_total` | counter | `result`: `hit`, `miss` | Resolution cache lookups |
| `lineage_signed_urls_total` | counter | | Signed download URLs minted |
| `lineage_versions_published_total` | counter | | Versions published |
| `lineage_stage_transitions_total` | counter | `to` | Stage transitions by target stage |
| `lineage_singleton_demotions_total` | counter | | Incumbents demoted by a promotion into a singleton stage |
| `lineage_upload_finalize_seconds` | histogram | | Upload finalize (verify and record) duration |
| `lineage_digest_mismatch_total` | counter | | Finalize rejections for a digest mismatch |
| `lineage_up` | gauge | | Always `1` |
| `lineage_models`, `lineage_versions`, `lineage_artifacts`, `lineage_deployments` | gauge | | Registry totals |
| `lineage_versions_by_stage` | gauge | `stage` | Versions per stage |
| `lineage_db_connections_open`, `_in_use`, `_idle` | gauge | | `database/sql` pool |

- `surface` is `model-api` or `admin-ui`; the ops port is not instrumented.
- `route` is the matched route template, such as `/v1/models/{model}/resolve`, or
  `unmatched`, so cardinality does not grow with the number of models.
- Gauges are refreshed at scrape. Each scrape queries the metadata store to refresh the
  domain gauges.
- The pool gauges are absent with the `memory` engine.

### Prometheus operator

With the Prometheus operator, the chart can create the scrape target and alert rules:

```yaml
observability:
  serviceMonitor:
    enabled: true
    labels: { release: kube-prometheus-stack }   # must match your Prometheus selector
    interval: 30s
  prometheusRule:
    enabled: true
    labels: { release: kube-prometheus-stack }
```

The `PrometheusRule` defines `LineageResolveAvailability`, `LineageResolveLatencyP99`,
`LineagePublishSuccessRate`, `LineageDigestMismatches`, `LineageProductionSingletonViolated`
and `LineageDown`. Thresholds are under `observability.prometheusRule.slo`, with `window`
(default `30m`) and `for` (default `10m`).

## Tracing

OpenTelemetry over OTLP/HTTP, off unless `LINEAGE_OTLP_ENDPOINT` (or
`OTEL_EXPORTER_OTLP_ENDPOINT`) is set. `host:port` is sent plaintext; use an `https://` URL
for TLS. Sampling is parent-based with ratio `LINEAGE_TRACE_SAMPLE_RATIO`. Inbound W3C
`traceparent` and `baggage` are continued.

Spans and their attributes:

- `<METHOD> <route>` (server): `http.request.method`, `http.route`,
  `http.response.status_code`, `lineage.surface`.
- `core.Resolve`: `lineage.model`, `lineage.version`, `lineage.cache`.
- `core.PublishVersion`: `lineage.model`, `lineage.version`, `lineage.artifacts`.
- `core.Transition`: `lineage.model`, `lineage.version`, `lineage.stage.from`,
  `lineage.stage.to`, `lineage.demoted`.
- `core.FinalizeUpload`: `lineage.model`, `lineage.version`, `lineage.upload.mode`,
  `lineage.size_bytes`.
- `store.<Method>`: one span per metadata store call.

A `5xx` response marks the server span as an error. Pending spans are flushed on shutdown.
If the exporter cannot be created, Lineage logs `tracing disabled:` and keeps serving.

## Logging

Lineage writes two kinds of log, both to stderr.

- **Access log:** one JSON line per Model API and Admin UI request.
- **Operational log:** plain text lines prefixed `□`, for startup settings, GC, sealing and
  shutdown.

```json
{"time":"…","level":"INFO","msg":"request","surface":"model-api","method":"GET",
 "route":"/v1/models/{model}/resolve","path":"/v1/models/fraud/resolve","status":200,
 "latencyMs":3,"requestId":"4bf92f…","actor":"ci@acme","traceId":"4bf92f…"}
```

`requestId` is the inbound `X-Request-Id`, else the trace ID, else a random ID, and is
echoed back in the `X-Request-Id` response header. Query strings, bodies and credentials are
not logged.

## Security model

Lineage performs no authentication or authorization on any port. It trusts every request it
receives and records the value of `LINEAGE_ACTOR_HEADER` (default `X-Lineage-Actor`) as the
actor on audit events. Your ingress, gateway or mesh decides who may call it.

### What each port exposes

Each port needs its own protection, because each exposes different reads and writes.

| Port | Callers | Reads | Writes | Protect with |
| --- | --- | --- | --- | --- |
| `:8081` Model API | Pipelines, inference systems | Everything, including signed download URLs | All registry writes | Machine identity (mTLS, tokens) |
| `:8080` Admin UI | People | Everything shown in the console | Governance actions from the console | SSO proxy |
| `:9090` ops | Prometheus | Health, governance settings, metrics | None | Cluster network only |

- Model API callers go through a gateway or ingress that authenticates, authorizes and
  overwrites the actor header. Its writes are publish, upload, promote, delete, holds,
  reviews and validations.
- Admin UI users go through an SSO proxy that overwrites the actor header. Its writes are
  stage transitions, holds and releases, reviews, validations, classifications and change
  plans.
- Anything else in the cluster reaches nothing; NetworkPolicy enforces that.

Anyone who can call resolve receives signed URLs that grant 15 minutes of direct read access
to the bytes, independent of any storage policy on the caller.

### The actor header

The actor header is only as trustworthy as the proxy that sets it.

- Set it at the edge from the authenticated principal, replacing any value the client sent.
  A header passed through from the client makes the audit trail say whatever the client
  claims.
- The value is recorded verbatim and appears in access logs.
- `lineage-cli`, `lineage-seed` and `lineage-init` always send `X-Lineage-Actor`. If you rename
  the header, their values are not recorded unless your proxy maps them.

### Network

The chart's NetworkPolicy (`networkPolicy.enabled`) admits `:8081` and `:9090` from any
source and `:8080` from `networkPolicy.adminUiFrom`. An empty `adminUiFrom` admits everyone.
It does not restrict egress. Tighten it with your own policy if in-cluster workloads must not
publish directly.

### Secrets

Each credential has its own chart value that names a Secret:

- Postgres DSN: `database.postgres.dsnSecret`.
- S3 keys: `storage.s3.credentialsSecret`, or none with IRSA.
- OCI credentials: `storage.oci.credentialsSecret`.
- Redis password: `cache.redis.passwordSecret`.

Credentials are never returned by the API. Keep them out of `extraEnv`, which renders as plain
values in the Deployment.

### Container

The image is distroless and static. It runs as UID `65532` with a read-only root filesystem,
all capabilities dropped, `RuntimeDefault` seccomp and no privilege escalation.

Next: [Deploy](/operate/deploy/) · [Audit](/governance/audit/) · [Configuration](/operate/configuration/)
