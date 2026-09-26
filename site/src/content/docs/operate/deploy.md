---
title: Deploy
description: Install Lineage with the Helm chart, build the container images, run it locally, and check a production install.
sidebar:
  order: 2
---

Lineage ships as one binary in one Deployment. The Helm chart at `deploy/helm/lineage`
(chart `0.1.0`, Kubernetes `>=1.23`) is the supported way to run it on a cluster.

## What the chart creates

The release is one Deployment behind three Services. A migration Job runs before install and
upgrade; Ingresses front the Model API and the console; a PVC is mounted when SQLite or `fs`
storage needs local disk.

| Object | Created when |
| --- | --- |
| Deployment, three Services, ServiceAccount | Always (ServiceAccount if `serviceAccount.create`) |
| PVC `<release>-data`, mounted at `/data` | `database.engine=sqlite` or `storage.driver=fs` |
| Migrate Job | `database.engine=postgres` and `migrations.auto=true` |
| Ingress per surface | `ingress.modelApi.enabled`, `ingress.adminUi.enabled` |
| NetworkPolicy | `networkPolicy.enabled` |
| HPA, PodDisruptionBudget | `autoscaling.enabled`, `podDisruptionBudget.enabled` |
| ServiceMonitor, PrometheusRule | `observability.metrics` and the respective `enabled` |

## Install

Two profiles ship with the chart.

```bash
# SQLite on a PVC, fs storage, one replica, no external dependencies
helm install lineage deploy/helm/lineage \
  -f deploy/helm/lineage/values-dev.yaml

# Postgres + S3, 2+ replicas, HPA, PDB, NetworkPolicy, ServiceMonitor
kubectl create secret generic lineage-db \
  --from-literal=dsn='postgres://lineage:…@db.internal:5432/lineage?sslmode=require'
helm install lineage deploy/helm/lineage \
  -f deploy/helm/lineage/values-prod.yaml \
  --set database.postgres.dsnSecret.name=lineage-db \
  --set ingress.modelApi.host=models.example.com \
  --set ingress.adminUi.host=console.example.com \
  --set publicModelApiUrl=https://models.example.com
```

The chart fails at render time when:

- `database.engine=sqlite` with `replicaCount > 1`, or with `autoscaling.enabled`;
- `database.engine=postgres` without `database.postgres.dsnSecret.name`;
- `storage.driver=oci` without `storage.oci.registry`;
- `cache.engine=redis` without `cache.redis.addr`.

## Values that matter

| Value | Default | Notes |
| --- | --- | --- |
| `image.repository` | `ghcr.io/proseria-research/lineage` | |
| `image.tag` | chart `appVersion` | Pin a published tag or digest. |
| `replicaCount` | `1` | Ignored when `autoscaling.enabled`. |
| `database.engine` | `sqlite` | `sqlite` or `postgres`. |
| `database.sqlite.path` | `/data/lineage.db` | On the PVC. |
| `database.sqlite.pvc.size` / `.storageClass` | `5Gi` / cluster default | Sizes the one PVC, which also holds `fs` artifacts. |
| `database.postgres.dsnSecret.name` / `.key` | empty / `dsn` | Secret holding the full DSN. |
| `cache.engine` | `memory` | `redis` for more than one replica. |
| `cache.redis.addr`, `.db`, `.passwordSecret` | empty, `0`, empty | |
| `storage.driver` | `fs` | `fs`, `s3`, or `oci`. |
| `storage.s3.*` | empty | `bucket`, `region`, `endpoint`, `pathStyle`, `credentialsSecret`. |
| `storage.oci.*` | empty | `registry`, `repository`, `plainHttp`, `credentialsSecret`. |
| `storage.gc.mode` | `retain` | See [Storage](/operate/storage/#garbage-collection). |
| `serviceAccount.annotations` | `{}` | IRSA or workload identity for storage credentials. |
| `actorHeader` | `X-Lineage-Actor` | Must match what your ingress sets. |
| `publicModelApiUrl` | empty | Model API URL shown in console snippets. Set it when you use an ingress. |
| `compliance.retention.*` | `3650` / `3650` | Retention floors in days. |
| `compliance.auditAttestation.*` | `true`, `60`, `5` | Audit sealing. |
| `migrations.auto` | `true` | Runs the migrate Job for Postgres. |
| `resources` | req `50m`/`96Mi`, limit `1`/`512Mi` | |
| `extraEnv` | `[]` | Extra env entries appended to the container, for variables the chart does not template. |

The chart renders the configuration values into environment variables; see
[Configuration](/operate/configuration/).
`LINEAGE_S3_SESSION_TOKEN` and `LINEAGE_DOCS_URL` have no chart value; set them through
`extraEnv`.

### Migrations

`lineage migrate` applies pending schema migrations and exits. With Postgres, the chart runs it
as a `pre-install,pre-upgrade` hook Job (`backoffLimit: 4`, `activeDeadlineSeconds: 600`), so
new pods start against a migrated schema. Every process also runs the migrator when it opens
the store, so SQLite needs no Job and a Postgres install with `migrations.auto=false` still
migrates on first pod start. Migrations are forward-only.

### Probes and rollout

| Probe | Endpoint | Delay / period |
| --- | --- | --- |
| Liveness | `GET /healthz` on `ops` | 3 s / 10 s |
| Readiness | `GET /readyz` on `ops` | 3 s / 10 s |

SQLite installs use the `Recreate` strategy so two pods never share the RWO volume. Postgres
installs use `RollingUpdate`.

### Pod security

Pods run as UID/GID `65532`, non-root, read-only root filesystem, no privilege escalation, all
capabilities dropped, `RuntimeDefault` seccomp. `/tmp` is an `emptyDir`.

### Ingress and NetworkPolicy

Each surface has its own Ingress (`host`, `className`, `annotations`, `tls`), routing `/` to
its Service. Authentication belongs on these Ingresses; see
[Observability and security](/operate/observability/#security-model).

With `networkPolicy.enabled`, the policy allows `:8081` and `:9090` from any source and `:8080`
from the peers in `networkPolicy.adminUiFrom`.

:::caution[Set adminUiFrom]
An empty `adminUiFrom` renders the Admin UI rule without a `from` clause, which Kubernetes
reads as "allow from everywhere". List your gateway's namespace or pod selector explicitly.
:::

## Container images

| Image | Dockerfile | Contents |
| --- | --- | --- |
| `ghcr.io/proseria-research/lineage` | `Dockerfile` | `lineage` with the embedded console |
| `ghcr.io/proseria-research/lineage-init` | `Dockerfile.init` | KServe storage-initializer for `lineage://` URIs |

Both are static, cgo-free Go builds (Go 1.25) on `gcr.io/distroless/static-debian12:nonroot`,
running as `nonroot`. There is no shell in either image. CI publishes `linux/amd64` and
`linux/arm64`, tagged `latest` (default branch), branch name, semver from `v*` tags, and short
SHA.

```bash
docker build --build-arg VERSION=1.2.3 -t lineage:1.2.3 .    # sets `lineage version`
docker build -f Dockerfile.init -t lineage-init:dev .
```

The registry image exposes `8080 8081 9090` and has `ENTRYPOINT ["/usr/local/bin/lineage"]`;
pass `migrate` or `version` as arguments.

## Local development

Requires Go and, for the console, Node with pnpm.

| Target | Does |
| --- | --- |
| `make run` | Builds the console and binary, runs with a 3650-day retention floor and sealing on. SQLite at `./lineage.db`, artifacts in `./data/artifacts`. |
| `make build` | `bin/lineage` with the embedded console. |
| `make seed` | Loads demo data into a running registry (`LINEAGE_ENDPOINT`). |
| `make reset` | Deletes `./lineage.db` and `./data`. Stop the registry first. |
| `make test` / `make test-console` | Unit tests, without and with the console build. |
| `make test-e2e` | Builds and launches the real binary and runs a publish-to-resolve flow. |
| `make docker` / `make docker-init` | Builds both images locally. |
| `make helm-lint` | Lints the chart against both profiles. |

With the retention floor from `make run`, deletes are refused; `make reset` is how you start
over. A plain `go build ./cmd/lineage` compiles a stub console and needs no Node.

## Production checklist

- [ ] `database.engine=postgres`, DSN in a Secret, `sslmode=require`.
- [ ] `cache.engine=redis` if more than one replica.
- [ ] `storage.driver` is `s3` or `oci`. `fs` uses the RWO PVC, which cannot follow pods across nodes.
- [ ] Storage credentials from IRSA or a Secret, never plain values.
- [ ] `image.tag` pinned.
- [ ] Both Ingresses authenticate, and each overwrites the actor header.
- [ ] `networkPolicy.enabled=true` with a non-empty `adminUiFrom`.
- [ ] `publicModelApiUrl` set to the Model API's external URL.
- [ ] Retention floors chosen deliberately; `/healthz` shows the values in force.
- [ ] `storage.gc.mode=sweep` only on a bucket or prefix Lineage owns alone.
- [ ] ServiceMonitor and PrometheusRule enabled, with labels your Prometheus selects.
- [ ] Metadata and artifact backups scheduled and a restore tested ([Storage](/operate/storage/#backup-and-restore)).

:::caution[In-process state with several replicas]
Upload sessions, `Idempotency-Key` records and the `memory` cache live in the pod that
created them. An upload's `initiateUpload`, `uploadContent` and `finalizeUpload` must reach the
same pod, or finalize returns `unknown or expired upload`; a retried `POST` with the same key
reaching another pod is not deduplicated. The chart does not configure session affinity.
:::

Next: [Configuration](/operate/configuration/) · [Storage](/operate/storage/) · [Observability and security](/operate/observability/)
