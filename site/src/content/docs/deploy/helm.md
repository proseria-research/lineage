---
title: Deploy with Helm
description: The Helm chart is a first-class product surface — one install yields a working, secure registry, on SQLite or on Postgres and S3.
sidebar:
  order: 1
---

The chart is versioned and tested with the code. "One `helm install` yields a working, secure
registry" is an acceptance criterion, not an aspiration. There is no Istio dependency.

## Two profiles

**Development** — SQLite on a PVC, filesystem storage, no external dependencies:

```bash
helm install lineage deploy/helm/lineage \
  -f deploy/helm/lineage/values-dev.yaml
```

**Production** — external Postgres and S3, autoscaling, PodDisruptionBudget, NetworkPolicy,
ServiceMonitor:

```bash
helm install lineage deploy/helm/lineage \
  -f deploy/helm/lineage/values-prod.yaml \
  --set database.postgres.dsnSecret.name=lineage-db \
  --set ingress.modelApi.host=api.example.com
```

## Values that matter

### Database

```yaml
database:
  engine: sqlite            # sqlite | postgres
  sqlite:
    pvc: { size: 5Gi, storageClass: "" }
    path: /data/lineage.db
  postgres:
    dsnSecret: { name: lineage-db, key: dsn }
```

The DSN comes from a Secret so it never lands in a ConfigMap.

:::caution[SQLite is pinned to one replica]
SQLite is single-writer. The chart **rejects a multi-replica SQLite configuration at template
time** rather than letting you discover the problem in production. Set
`database.engine: postgres` before raising `replicaCount`.
:::

### Storage

```yaml
storage:
  driver: s3               # fs | s3
  fs:
    root: /data/artifacts
  s3:
    bucket: acme-models
    region: eu-west-1
    endpoint: ""           # override for MinIO / R2 / Ceph
    pathStyle: false       # true for MinIO / Ceph
    credentialsSecret:     # omit entirely to use IRSA / ECS / IMDS
      name: lineage-s3
      accessKeyKey: accessKey
      secretKeyKey: secretKey
  gc:
    mode: retain           # retain | sweep
    grace: 24h
    interval: 1h
    prefix: ""
```

### Cache

```yaml
cache:
  engine: redis            # memory | redis
  redis:
    addr: redis.internal:6379
    db: 0
    passwordSecret: { name: lineage-redis, key: password }
```

Any multi-replica install needs `redis` — see [Caching and invalidation](/delivery/caching/).

### Availability

```yaml
replicaCount: 3
autoscaling:
  enabled: true
  minReplicas: 3
  maxReplicas: 10
podDisruptionBudget:
  enabled: true
  minAvailable: 2
```

### Networking

```yaml
service:
  type: ClusterIP
ingress:
  modelApi:
    enabled: true
    host: api.example.com
  admin:
    enabled: true
    host: models.example.com
networkPolicy:
  enabled: true
```

The two surfaces get separate ingresses because they have different audiences and different
authentication. See [Security model](/operate/security-model/).

### Observability

```yaml
observability:
  metrics: true
  serviceMonitor:
    enabled: true
    labels: { release: kube-prometheus-stack }
  otlpEndpoint: "otel-collector.observability:4318"
  prometheusRule:
    enabled: true
    labels: { release: kube-prometheus-stack }
```

See [Observability](/operate/observability/).

### Migrations

```yaml
migrations:
  auto: true
```

For Postgres, a migrate Job runs `pre-install` and `pre-upgrade`. SQLite migrates in-process
at startup.

## Upgrading

```bash
helm upgrade lineage deploy/helm/lineage -f my-values.yaml
```

Migrations are forward-only — there are no down migrations. Take a backup before an upgrade
that crosses a schema version, and roll back by restoring it. See
[Backup and restore](/operate/backup-and-restore/).

## Validate before you install

```bash
helm lint deploy/helm/lineage -f deploy/helm/lineage/values-prod.yaml
helm template lineage deploy/helm/lineage -f my-values.yaml | less
```

`helm template` is where the SQLite multi-replica guard fires, along with the other
template-time checks. Run it in CI.

## The image

Distroless, non-root, statically linked. Nothing to shell into, which is the intent.

```yaml
image:
  repository: ghcr.io/proseria-research/lineage
  tag: ""                  # defaults to the chart's appVersion
  pullPolicy: IfNotPresent
```

## Next

- [Production checklist](/deploy/production-checklist/)
- [Configuration](/operate/configuration/)
- [Security model](/operate/security-model/)
