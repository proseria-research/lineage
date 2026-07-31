package main

import (
	"encoding/json"

	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// The demo dataset: a small but realistic registry — several teams, versions at every stage,
// artifacts both uploaded and registered by reference (s3/gs/hf/oci), lineage edges, and
// deployment records. Everything is created through the Model API, so the audit feed and the
// stage history come out real rather than fabricated.

type seedModel struct {
	core.CreateModelInput
	Archived bool // apply :archive after its versions exist
	Versions []version
}

type version struct {
	Name        string
	Description string
	Author      string
	Labels      map[string]string
	Artifacts   []core.ArtifactInput // registered by reference at publish time (§05.5)
	Uploads     []blob               // real bytes pushed through the upload handshake (§05.6)
	Lineage     []core.LineageInput
	Path        []domain.Stage // stage moves applied in order; empty leaves it in draft
	Deployments []core.DeploymentInput
}

// blob is a synthetic artifact payload. Bytes are deterministic, so re-seeding produces the
// same digests.
type blob struct {
	Name        string
	Kind        domain.ArtifactKind
	MediaType   string
	ModelFormat *domain.ModelFormat
	Size        int    // total bytes
	Filler      string // repeated to reach Size; varies the digest per version
}

func (b blob) bytes() []byte {
	out := make([]byte, 0, b.Size)
	out = append(out, ("LINEAGE-SEED " + b.Name + " " + b.Filler + "\n")...)
	for len(out) < b.Size {
		out = append(out, b.Filler...)
	}
	return out[:b.Size]
}

// Lineage edge constructors (§07): `to` carries exactly one of a sibling version or an
// external URI, so each relation gets its own helper.
func derivedFrom(v string) core.LineageInput  { return edge(domain.RelDerivedFrom, v, "") }
func trainedOn(uri string) core.LineageInput  { return edge(domain.RelTrainedOn, "", uri) }
func producedBy(uri string) core.LineageInput { return edge(domain.RelProducedBy, "", uri) }

func edge(rel domain.LineageRelation, version, uri string) core.LineageInput {
	e := core.LineageInput{Relation: rel}
	e.To.Version, e.To.URI = version, uri
	return e
}

func onnx(filler string) blob {
	return blob{
		Name: "model.onnx", Kind: domain.KindModel, MediaType: "application/octet-stream",
		ModelFormat: &domain.ModelFormat{Name: "onnx", Version: "1.16"},
		Size:        64 << 10, Filler: filler,
	}
}

func props(s string) json.RawMessage { return json.RawMessage(s) }

var (
	staged     = []domain.Stage{domain.StageStaging}
	promoted   = []domain.Stage{domain.StageStaging, domain.StageProduction}
	retired    = []domain.Stage{domain.StageStaging, domain.StageArchived}
	prodEnv    = "production"
	stagingEnv = "staging"
)

var dataset = []seedModel{
	{
		CreateModelInput: core.CreateModelInput{
			Name:        "fraud-detector",
			Description: "Real-time card-fraud scoring on the payments authorization path.",
			Owner:       "risk-ml@acme.example",
			Labels:      map[string]string{"team": "risk", "tier": "critical", "domain": "payments"},
			// Custom properties are free-form per-install metadata (§02.3, filterable with cp.* on Postgres).
			CustomProperties: props(`{"reviewBoard":"model-risk","pciScope":true,"slaMs":25}`),
		},
		Versions: []version{
			{
				Name: "1.0.0", Author: "ana@acme.example",
				Description: "Baseline gradient-boosted scorer trained on Q4 transactions.",
				Labels:      map[string]string{"framework": "xgboost", "release": "ga"},
				Uploads:     []blob{onnx("q4-baseline-")},
				Lineage: []core.LineageInput{
					trainedOn("s3://acme-datasets/fraud/transactions-2025-11.parquet"),
					producedBy("https://airflow.acme.example/dags/fraud-train/runs/8821"),
				},
				Path: promoted, // demoted to archived when 1.1.0 takes production (§02.4)
			},
			{
				Name: "1.1.0", Author: "ana@acme.example",
				Description: "Adds merchant-velocity features; +3.1pp recall at fixed FPR.",
				Labels:      map[string]string{"framework": "xgboost", "release": "ga"},
				Uploads:     []blob{onnx("velocity-features-")},
				Artifacts: []core.ArtifactInput{{
					Kind: domain.KindDoc, Name: "model-card.md", MediaType: "text/markdown",
					URI: "s3://acme-models/fraud-detector/1.1.0/model-card.md",
				}},
				Lineage: []core.LineageInput{
					derivedFrom("1.0.0"),
					trainedOn("s3://acme-datasets/fraud/transactions-2026-04.parquet"),
					producedBy("https://airflow.acme.example/dags/fraud-train/runs/9140"),
				},
				Path: promoted,
				Deployments: []core.DeploymentInput{{
					Environment: prodEnv,
					EndpointURI: "https://serving.acme.example/v1/models/fraud-detector:predict",
					Status:      domain.DeployActive,
					ExternalRef: "kserve/payments/fraud-detector",
				}},
			},
			{
				Name: "1.2.0-rc1", Author: "dev@acme.example",
				Description: "Release candidate: graph-embedding features, under shadow eval.",
				Labels:      map[string]string{"framework": "xgboost", "release": "rc"},
				Uploads:     []blob{onnx("graph-embeddings-")},
				Lineage:     []core.LineageInput{derivedFrom("1.1.0")},
				Path:        staged,
				Deployments: []core.DeploymentInput{{
					Environment: stagingEnv,
					EndpointURI: "https://staging.serving.acme.example/v1/models/fraud-detector:predict",
					Status:      domain.DeployActive,
					ExternalRef: "kserve/staging/fraud-detector-rc",
				}},
			},
		},
	},
	{
		CreateModelInput: core.CreateModelInput{
			Name:        "churn-predictor",
			Description: "Monthly subscriber churn propensity for lifecycle campaigns.",
			Owner:       "growth-ml@acme.example",
			Labels:      map[string]string{"team": "growth", "tier": "standard"},
		},
		Versions: []version{
			{
				Name: "0.9.0", Author: "kim@acme.example",
				Description: "First production candidate; logistic baseline.",
				Artifacts: []core.ArtifactInput{{
					Name: "model.pkl", URI: "s3://acme-models/churn-predictor/0.9.0/model.pkl",
					MediaType: "application/octet-stream", SizeBytes: 4_812_544,
					Digest:      "sha256:2f0b7d29d0a2f8d0d3b2b8f9c0a1d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5",
					ModelFormat: &domain.ModelFormat{Name: "sklearn", Version: "1.5"},
				}},
				Lineage: []core.LineageInput{trainedOn("gs://acme-datasets/churn/features-2026-02.parquet")},
				Path:    promoted,
			},
			{
				Name: "1.0.0", Author: "kim@acme.example",
				Description: "GA: gradient boosting with tenure and support-ticket features.",
				Labels:      map[string]string{"release": "ga"},
				Artifacts: []core.ArtifactInput{
					{
						Name: "model.pkl", URI: "s3://acme-models/churn-predictor/1.0.0/model.pkl",
						MediaType: "application/octet-stream", SizeBytes: 6_291_456,
						Digest:      "sha256:9c1a5e77b4f2a3d81e0c6b5a4938271605f4e3d2c1b0a99887766554433221100",
						ModelFormat: &domain.ModelFormat{Name: "sklearn", Version: "1.5"},
					},
					{
						Kind: domain.KindDoc, Name: "metrics.json", MediaType: "application/json",
						URI: "s3://acme-models/churn-predictor/1.0.0/metrics.json",
					},
				},
				Lineage: []core.LineageInput{
					derivedFrom("0.9.0"),
					trainedOn("gs://acme-datasets/churn/features-2026-05.parquet"),
				},
				Path: promoted,
				Deployments: []core.DeploymentInput{{
					Environment: prodEnv,
					EndpointURI: "https://serving.acme.example/v1/models/churn-predictor:predict",
					Status:      domain.DeployActive,
				}},
			},
		},
	},
	{
		CreateModelInput: core.CreateModelInput{
			Name:        "sentiment-classifier",
			Description: "Support-ticket sentiment tagging (Hugging Face weights, by reference).",
			Owner:       "nlp@acme.example",
			Labels:      map[string]string{"team": "nlp", "tier": "standard"},
		},
		Versions: []version{
			{
				Name: "2.1.0", Author: "lee@acme.example",
				Description: "DistilBERT SST-2 fine-tune served straight from the HF hub.",
				Artifacts: []core.ArtifactInput{{
					Name: "weights", URI: "hf://distilbert-base-uncased-finetuned-sst-2-english",
					ModelFormat: &domain.ModelFormat{Name: "transformers", Version: "4.44"},
				}},
				Path: promoted,
				Deployments: []core.DeploymentInput{{
					Environment: prodEnv,
					EndpointURI: "https://serving.acme.example/v1/models/sentiment-classifier:predict",
					Status:      domain.DeployActive,
				}},
			},
			{
				Name: "2.2.0-rc1", Author: "lee@acme.example",
				Description: "Domain-adapted checkpoint; awaiting eval sign-off (stays in draft).",
				Artifacts: []core.ArtifactInput{{
					Name: "weights", URI: "hf://acme/sentiment-support-tickets",
					ModelFormat: &domain.ModelFormat{Name: "transformers", Version: "4.44"},
				}},
				Lineage: []core.LineageInput{derivedFrom("2.1.0")},
			},
		},
	},
	{
		CreateModelInput: core.CreateModelInput{
			Name:        "demand-forecast",
			Description: "Weekly SKU-level demand forecasting for supply planning.",
			Owner:       "supply-ml@acme.example",
			Labels:      map[string]string{"team": "supply", "tier": "standard"},
		},
		Versions: []version{{
			Name: "0.4.0", Author: "raj@acme.example",
			Description: "Experimental hierarchical model; not yet evaluated.",
			Artifacts: []core.ArtifactInput{{
				Name: "model.xgb", URI: "gs://acme-models/demand-forecast/0.4.0/model.xgb",
				MediaType:   "application/octet-stream",
				ModelFormat: &domain.ModelFormat{Name: "xgboost", Version: "2.0"},
			}},
			Lineage: []core.LineageInput{trainedOn("gs://acme-datasets/supply/orders-2026-06.parquet")},
		}},
	},
	{
		CreateModelInput: core.CreateModelInput{
			Name:        "legacy-recommender",
			Description: "Retired homepage recommender, kept for audit and reproducibility.",
			Owner:       "platform-ml@acme.example",
			Labels:      map[string]string{"team": "platform", "tier": "deprecated"},
		},
		Archived: true,
		Versions: []version{{
			Name: "1.9.3", Author: "platform-ml@acme.example",
			Description: "Final version before the service was decommissioned.",
			Artifacts: []core.ArtifactInput{{
				Name: "model", URI: "oci://ghcr.io/acme/models/recommender:1.9.3",
				ModelFormat: &domain.ModelFormat{Name: "tensorflow", Version: "2.15"},
			}},
			Path: retired,
		}},
	},
}
