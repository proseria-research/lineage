---
title: What Lineage is
description: Lineage is a self-hostable model registry — the system of record for what models exist, where their artifacts live, what stage each version is in, and how serving systems fetch them.
sidebar:
  order: 1
---

Lineage is the metadata and governance layer between experimentation and production. It
knows what models and versions exist, where their artifact bytes live, what lifecycle stage
each version is in, who owns it, and how consumers fetch it.

It is one Go binary. It stores metadata in your database and pointers to artifacts in your
object store. It never holds the weights itself.

## What it does

| Area | What you get |
| --- | --- |
| **Registry** | Models, versions, and artifacts with digests, sizes, and model formats |
| **Governance** | Four lifecycle stages, a singleton `production` invariant, and an append-only audit trail |
| **Provenance** | Typed lineage edges — `derived_from`, `trained_on`, `produced_by`, `deployed_as` |
| **Delivery** | One `resolve` call returns a native storage URI, a signed URL, and a digest |
| **Insights** | Recorded composition facts, an architecture fingerprint, and version diff |
| **Operations** | Health probes, Prometheus metrics, structured logs, and a Helm chart |

## Two surfaces, two ports

One process, one Deployment, and the port selects the surface.

| Port | Surface | Audience |
| --- | --- | --- |
| `:8081` | **Model API** (`/v1`) — publish, resolve, fetch | Machines: CI, training jobs, serving systems |
| `:8080` | **Admin console** — dashboards, detail views, promotion | Humans |
| `:9090` | **Ops** — `/healthz`, `/readyz`, `/metrics` | Your platform |

Publishing is a Model API operation, not an admin one. The console is for people looking at
and steering the registry; it is never the only way to do something.

## What it is not

- **Not an authentication system.** Your ingress, gateway, or mesh owns authN and authZ.
  Lineage trusts an already-authenticated request and records the actor header for audit
  attribution. See [Security model](/operate/security-model/).
- **Not an artifact store.** Bytes live in your filesystem or object store and travel
  directly between storage and consumer.
- **Not a training orchestrator or experiment tracker.** It records what your pipeline
  produced; it does not run it.
- **Not a public model hub.** Hugging Face Hub is excellent at public sharing and discovery.
  Lineage is deliberately the private, self-hosted system of record.

## When to reach for it

Reach for Lineage when more than one team ships models, when you need to answer "which model
is serving production right now, and what produced it" without asking someone, and when you
want that answer to live in infrastructure you control.

If you have one model and one person, a bucket and a naming convention will do. Come back
when the naming convention starts lying to you.

## How it compares

| Capability | Lineage | Kubeflow Model Registry | Hugging Face Hub |
| --- | --- | --- | --- |
| Footprint | Single Go binary + Helm chart | Kubernetes / Kubeflow, MLMD-based | SaaS |
| Metadata store | SQLite or Postgres — your database | MLMD on MySQL | Managed, Git-backed |
| Artifact storage | S3 / GCS / Azure / filesystem | External URI references | HF-hosted |
| Provenance | Typed graph: ancestry + impact | MLMD lineage | Informal `base_model` tag |
| Serving delivery | `resolve` + `lineage://` initializer | KServe | Inference Endpoints |
| API contract | REST + OpenAPI 3.1 | REST + Python client | REST + `huggingface_hub` |
| Authentication | Delegated to infrastructure | Cluster identity | Built-in accounts |

Lineage targets **capability parity** with Kubeflow Model Registry, not wire compatibility.
The data model and API are our own.

## Next

- [Quickstart](/start/quickstart/) — a running registry in about a minute
- [Core concepts](/start/concepts/) — the six things the data model contains
- [Register your first model](/start/first-model/) — publish, upload, promote, resolve
