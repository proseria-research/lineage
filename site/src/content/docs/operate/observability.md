---
title: Observability
description: Health probes, Prometheus metrics, structured logs, OTLP traces, and the SLO rules that turn them into alerts.
sidebar:
  order: 5
---

Everything operational lives on the ops port, `:9090` by default. Keep it inside the cluster.

## Probes

| Endpoint | Answers |
| --- | --- |
| `/healthz` | Is the process alive? |
| `/readyz` | Can it reach its dependencies — database, storage, cache? |

`/readyz` is a real readiness check, not an alias for liveness. It fails when the metadata
store, the storage backend, or the cache is unreachable, which keeps a pod that cannot serve
out of the load balancer instead of returning errors from it.

Wire `/healthz` to `livenessProbe` and `/readyz` to `readinessProbe`. The Helm chart does.

## Metrics

`/metrics` in Prometheus text format, from a dependency-free registry built into the binary.

**Request metrics (RED), with templated route labels:**

| Metric | Type |
| --- | --- |
| `lineage_http_requests_total` | counter — by route, method, status |
| `lineage_http_request_duration_seconds` | histogram — by route |

Routes are templated (`/v1/models/{model}/resolve`), so cardinality stays bounded no matter
how many models you have.

**Domain metrics:**

| Metric | Meaning |
| --- | --- |
| `lineage_resolve_cache_total` | Resolve cache hits and misses |
| `lineage_versions_published_total` | Publishes |
| `lineage_stage_transitions_total` | Transitions, by target stage |
| `lineage_singleton_demotions_total` | Incumbents archived by a promotion |
| `lineage_upload_finalize_seconds` | Upload finalize latency |
| `lineage_digest_mismatch_total` | Uploads rejected for a bad digest |
| `lineage_signed_urls_total` | Signed URLs minted |

**Gauges:**

`lineage_models`, `lineage_versions`, `lineage_versions_by_stage`, `lineage_artifacts`,
`lineage_deployments`, `lineage_db_connections_open` / `_in_use` / `_idle`, `lineage_up`.

`lineage_versions_by_stage{stage="production"}` is the one to put on a wall. It should equal
your model count.

## Logs

Structured JSON access logs on stdout. Every line carries a request id and, when present, the
W3C `traceparent`, so a log line joins to a trace without correlation guesswork.

```json
{
  "level": "info",
  "msg": "request",
  "method": "GET",
  "route": "/v1/models/{model}/resolve",
  "status": 200,
  "duration_ms": 3.1,
  "request_id": "01JQ8Z...",
  "traceparent": "00-4bf92f...-00f067...-01",
  "actor": "kserve"
}
```

## Traces

Set an OTLP/HTTP collector endpoint and spans start flowing. Leave it empty and tracing is
off — request ids and log correlation still work.

```yaml
observability:
  otlpEndpoint: "otel-collector.observability:4318"
  serviceName: lineage
  traceSampleRatio: 1.0    # parent-based head sampling; lower it on hot installs
```

Accepts `host:port` for plaintext or a full `http(s)://` URL.

## Prometheus operator

```yaml
observability:
  metrics: true
  serviceMonitor:
    enabled: true
    labels: { release: kube-prometheus-stack }
    interval: 30s
```

The `labels` matter — the operator only picks up a `ServiceMonitor` that matches its
selector.

## SLO alerts

The chart can ship a `PrometheusRule` with error-budget burn rules over the RED metrics.

```yaml
observability:
  prometheusRule:
    enabled: true
    labels: { release: kube-prometheus-stack }
    slo:
      resolveAvailability: 99.9        # percent of resolves that must not 5xx
      resolveLatencyP99Seconds: 0.05   # cache-hit p99 target
      publishSuccessRate: 99.5         # percent of publishes that must succeed
    window: 30m
    for: 10m
```

These thresholds are starting targets, not universal truths. Watch your own numbers for a
couple of weeks and tune them — an SLO nobody believes gets muted, and a muted alert is worse
than no alert.

## What to watch

| Symptom | Look at |
| --- | --- |
| Resolves slow | `lineage_resolve_cache_total` hit ratio; is Redis shared across replicas? |
| Uploads failing | `lineage_digest_mismatch_total`, `lineage_upload_finalize_seconds` |
| Pods flapping | `/readyz` — usually the database or the bucket, not the process |
| Nothing in production | `lineage_versions_by_stage{stage="production"}` |
| Connection exhaustion | `lineage_db_connections_in_use` against your pool size |

## Next

- [Production checklist](/deploy/production-checklist/)
- [Deploy with Helm](/deploy/helm/)
- [Caching and invalidation](/delivery/caching/)
