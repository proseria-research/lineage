---
title: Comparison
description: How Lineage differs from MLflow, Kubeflow Model Registry, cloud-managed registries, OCI artifact registries, tracking platforms and Hugging Face Hub, and what it does not do.
sidebar:
  order: 4
---

This page compares Lineage with other ways teams run a model registry. Claims about other
products link to their documentation or issue trackers and reflect those sources as of
September 2026.

## At a glance

| | Lineage | MLflow | Kubeflow Model Registry | Cloud-managed¹ | OCI registries² |
| --- | --- | --- | --- | --- | --- |
| Self-hosted | Yes, one binary | Yes | Yes, on Kubernetes | No | Yes |
| Artifact bytes | Signed URL from your storage | Often through the tracking server | External URI | Provider storage | In the registry |
| Lifecycle | Fixed stages, one `production` | Aliases (stages deprecated) | Model/version state | Varies by product | Tags |
| Audit trail | Every change, sealed | Not built in | — | Cloud audit logs | Registry logs |
| Serving hand-off | `resolve` + KServe `lineage://` | `models:/` URI | KServe integration | Provider serving | Pull by tag or digest |
| Authentication | Your infrastructure | Optional built-in | Cluster identity | Cloud IAM | Built in |

¹ SageMaker, Vertex AI, Azure ML, Databricks Unity Catalog. ² KitOps, Harbor, JFrog
Artifactory.

## MLflow Model Registry

MLflow combines experiment tracking with a registry and is the most widely self-hosted option.

| | MLflow | Lineage |
| --- | --- | --- |
| Artifact path | Uploads and downloads often go through the tracking server. Large models have hit timeouts, memory growth and size limits ([#22665](https://github.com/mlflow/mlflow/issues/22665), [#13447](https://github.com/mlflow/mlflow/issues/13447), [#5083](https://github.com/mlflow/mlflow/issues/5083)) | Clients read and write storage directly through signed URLs. Stream-through exists only for the filesystem backend |
| Lifecycle | Stages were deprecated in 2.9 in favour of free-form aliases ([docs](https://mlflow.org/docs/latest/ml/model-registry/workflow/)) | Four fixed stages. Promoting to `production` archives the previous version in the same transaction |
| Change history | No built-in audit log | An append-only audit event on every change, sealed for tamper evidence |
| Experiment tracking | Yes | No. Keep MLflow for tracking and publish finished versions to Lineage |

**Choose MLflow** if you want tracking and registry in one tool and your models are small.
**Choose Lineage** if you need a fixed promotion model, an audit trail, or large-artifact
delivery that does not pass through a server.

## Kubeflow Model Registry

Kubeflow Model Registry is the closest match in scope, and the benchmark Lineage aims to
match in capability.

| | Kubeflow Model Registry | Lineage |
| --- | --- | --- |
| Maturity | Alpha with limited support ([docs](https://www.kubeflow.org/docs/components/hub/overview/)). Now part of Kubeflow Hub | All planned milestones shipped |
| Footprint | Runs inside a Kubernetes/Kubeflow install | One binary. Runs on a laptop with SQLite or on Kubernetes with Helm |
| KServe | Custom storage initializer. It has failed against MinIO-registered models ([#1532](https://github.com/kubeflow/model-registry/issues/1532)) | `lineage://model/stage` initializer, resolved at pull time |
| Metadata export | Not available ([#7](https://github.com/kubeflow/model-registry/issues/7)) | No export API. The metadata is in your own SQLite or Postgres |
| Governance | Model and version state | Stages, audit, retention and legal hold, risk classification, change control plans |

Lineage does not implement Kubeflow's API. Moving from Kubeflow means re-publishing
through the Lineage [Model API](/api/publishing/).

## Cloud-managed registries

SageMaker, Vertex AI, Azure ML and Unity Catalog come with their cloud platforms and use
that platform's IAM and serving.

| Limitation | Source |
| --- | --- |
| SageMaker Model Collections are not supported in VPC mode | [AWS](https://docs.aws.amazon.com/sagemaker/latest/dg/modelcollections-limitations.html) |
| Vertex AI models are region-scoped | [Cake](https://www.cake.ai/blog/google-vertex-alternatives-portability-compliance-and-control) |
| Azure ML registries do not support MLflow model management across workspaces | [Microsoft](https://learn.microsoft.com/en-us/azure/machine-learning/how-to-manage-models-mlflow?view=azureml-api-2) |
| Unity Catalog migration drops comments and requires a signature on every model | [Databricks](https://docs.databricks.com/aws/en/machine-learning/manage-model-lifecycle/migrate-to-uc) |

**Choose a cloud registry** if you run on one cloud and use its serving stack.
**Choose Lineage** if you serve from more than one place, need to self-host, or want the
registry to outlive a platform choice.

## OCI and artifact registries

KitOps, Harbor and JFrog Artifactory store models as OCI artifacts or binaries. They are good
at moving bytes and caching them close to nodes.

They record little about a model beyond the artifact. Lineage stores lineage, evaluations,
stages and approvals, which these tools do not. The two work together: Lineage's `oci`
storage driver keeps artifact bytes in an OCI registry. See [Storage](/operate/storage/).

## Tracking platforms

Weights & Biases, Comet, ZenML and ClearML include a registry as part of a larger platform.
To use the registry you adopt the platform. ZenML's registry, for example, requires its
experiment tracker ([docs](https://docs.zenml.io/stacks/stack-components/model-registries)).
W&B self-hosting is enterprise-tier.

Neptune's hosted service shut down on 6 March 2026 and customer data was deleted
([Neptune](https://support.neptune.ai/en/articles/13925412-faq)). With Lineage, metadata
stays in your database and bytes in your storage.

## Hugging Face Hub

Hugging Face Hub is for sharing and discovering models, much of it in public. Lineage is a
private system of record for the versions you ship. A common pattern is to vet a model from
the Hub and then publish the approved version to Lineage with a `derived_from` edge to its
Hub URI.

## What Lineage does not do

| Not included | Use instead |
| --- | --- |
| Authentication and authorization | Your ingress, gateway or mesh. See [Introduction](/start/introduction/#authentication) |
| Experiment tracking | MLflow, W&B or similar |
| Model serving | KServe, Modal, Baseten or similar. They pull from Lineage |
| GCS or Azure Blob drivers | An S3-compatible endpoint |
| Multi-tenancy | One install per tenant |
| Public model hosting | Hugging Face Hub |
