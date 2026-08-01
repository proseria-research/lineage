package main

import (
	"crypto/sha256"
	"encoding/hex"
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
	// Insight submissions, applied in order (§11.6.1). More than one entry seeds the
	// multi-producer case the merge exists for: a header scanner reports what it can read
	// cheaply, then the SDK adds what only a publish-time walk knows. Leaving this empty
	// seeds a version nobody has reported on, which the console renders as such.
	Insight     []core.InsightWrite
	Footprints  []seedFootprint
	Evaluations []core.EvaluationInput
}

// seedFootprint is one memory scenario; the scenario name is the upsert key (§11.3.3).
type seedFootprint struct {
	Scenario string
	core.FootprintInput
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
func derivedFrom(v string) core.LineageInput { return edge(domain.RelDerivedFrom, v, "") }

// derivedFromWith records *how* the version was derived alongside the edge (§11.3.6) — the
// hashes can show the shape is unchanged, but only a declaration says "this is a quantize".
func derivedFromWith(v, properties string) core.LineageInput {
	e := edge(domain.RelDerivedFrom, v, "")
	e.Properties = json.RawMessage(properties)
	return e
}

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

// ---- Insight helpers (§11) ----

// facts marshals a fact map for an InsightWrite payload.
func facts(kv map[string]any) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(kv))
	for k, v := range kv {
		b, err := json.Marshal(v)
		if err != nil {
			panic("seed: unmarshalable fact " + k + ": " + err.Error())
		}
		out[k] = b
	}
	return out
}

func hashes(topology, shape, dtype, weights string) map[string]string {
	h := map[string]string{}
	for k, v := range map[string]string{"topology": topology, "shape": shape, "dtype": dtype, "weights": weights} {
		if v != "" {
			h[k] = "sha256:" + v
		}
	}
	return h
}

var yes, no = true, false

// eval is a reported quality result. Direction is explicit because the sign of a delta
// cannot be inferred from a metric name (§11.3.4).
func eval(suite, metric, split string, value float64, higherIsBetter *bool) core.EvaluationInput {
	return core.EvaluationInput{
		Suite: suite, Metric: metric, Split: split, Value: value, HigherIsBetter: higherIsBetter,
		HarnessName: "lm-eval", HarnessVersion: "0.4.2", NSamples: ptr(int64(1821)),
		Source: domain.SourceMeasured,
	}
}

func ptr[T any](v T) *T { return &v }

// distilbertTensors is a trimmed but realistic tensor list for the sentiment model, enough
// for the per-tensor diff to show a partial weight change (§11.4.2).
var distilbertTensors = []string{
	"classifier.weight",
	"distilbert.embeddings.word_embeddings.weight",
	"distilbert.transformer.layer.0.attention.q_lin.weight",
	"distilbert.transformer.layer.0.attention.v_lin.weight",
	"distilbert.transformer.layer.0.ffn.lin1.weight",
	"distilbert.transformer.layer.1.attention.q_lin.weight",
	"distilbert.transformer.layer.1.attention.v_lin.weight",
	"distilbert.transformer.layer.1.ffn.lin1.weight",
}

// archDoc builds a canonical arch doc whose per-tensor digests are deterministic. Tensors
// named in `changed` get a different digest, so a diff between two docs reports exactly
// those as changed — the signature of a merged adapter rather than a full fine-tune.
func archDoc(dtype string, changed ...string) json.RawMessage {
	dirty := map[string]bool{}
	for _, c := range changed {
		dirty[c] = true
	}
	doc := domain.ArchDoc{}
	for _, name := range distilbertTensors {
		salt := "base"
		if dirty[name] {
			salt = "tuned"
		}
		sum := sha256.Sum256([]byte(name + "|" + dtype + "|" + salt))
		doc.Tensors = append(doc.Tensors, domain.ArchTensor{
			Name: name, Dtype: dtype, Digest: "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	b, _ := json.Marshal(doc)
	return b
}

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
				Insight: []core.InsightWrite{{
					SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
					Reporter: "lineage-scanner", ReporterVersion: "0.3.1",
					Facts: facts(map[string]any{
						"hashes":        hashes("fraud-topo-v1", "fraud-shape-v1", "fp32-dtypes", ""),
						"framework":     map[string]string{"name": "xgboost", "version": "2.0"},
						"dtypeDominant": "fp32",
						"coverage": map[string]string{
							"paramCountTotal": domain.CoverageNotAttempted,
							"weightsBytes":    domain.CoverageNotAttempted,
						},
					}),
				}},
				Evaluations: []core.EvaluationInput{eval("holdout-2026-04", "recall_at_fpr_001", "test", 0.712, &yes)},
			},
			{
				Name: "1.2.0-rc1", Author: "dev@acme.example",
				Description: "Release candidate: graph-embedding features, under shadow eval.",
				Labels:      map[string]string{"framework": "xgboost", "release": "rc"},
				Uploads:     []blob{onnx("graph-embeddings-")},
				Lineage:     []core.LineageInput{derivedFrom("1.1.0")},
				Path:        staged,
				// A header-only scan: three hashes, no weights hash. A diff against 1.1.0
				// still separates a shape change from an unchanged shape, but cannot tell an
				// untouched republish from a retrain — and reports that (§11.4.3).
				Insight: []core.InsightWrite{{
					SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
					Reporter: "lineage-scanner", ReporterVersion: "0.3.1",
					Facts: facts(map[string]any{
						// Same tree structure, wider feature space: `rescaled`, and decidable
						// from header hashes alone — no weights hash needed (§11.4.1).
						"hashes":        hashes("fraud-topo-v1", "fraud-shape-v2", "fp32-dtypes", ""),
						"framework":     map[string]string{"name": "xgboost", "version": "2.0"},
						"dtypeDominant": "fp32",
						"coverage": map[string]string{
							"paramCountTotal": domain.CoverageNotAttempted,
							"weightsBytes":    domain.CoverageNotAttempted,
						},
					}),
				}},
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
				// A pickled sklearn model exposes nothing safely, so the only honest record
				// is a declaration plus a coverage map saying why the rest is absent. The
				// console shows "not reported" here, and a diff against 0.9.0 returns
				// verdict `unknown` rather than inventing one (§11.2, §11.5).
				Insight: []core.InsightWrite{{
					SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDeclared,
					Reporter: "release-checklist",
					Facts: facts(map[string]any{
						"framework": map[string]string{"name": "scikit-learn", "version": "1.5"},
						"diskBytes": 6_291_456,
						"coverage": map[string]string{
							"paramCountTotal": domain.CoverageUnavailable,
							"tensorCount":     domain.CoverageUnavailable,
							"hashes":          domain.CoverageUnavailable,
						},
					}),
				}},
				Evaluations: []core.EvaluationInput{
					eval("holdout-2026-05", "auc", "test", 0.8412, &yes),
					eval("holdout-2026-05", "logloss", "test", 0.3187, &no),
				},
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
				// Two producers, disjoint fields: the scanner reads the file header, the SDK
				// reports what only a publish-time module walk knows (§11.6.1).
				Insight: []core.InsightWrite{
					{
						SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
						Reporter: "lineage-scanner", ReporterVersion: "0.3.1",
						Facts: facts(map[string]any{
							"hashes":        hashes("d15t1lb3rt-topo", "d15t1lb3rt-shape", "fp32-dtypes", ""),
							"framework":     map[string]string{"name": "pytorch", "version": "2.4.1"},
							"producer":      map[string]string{"name": "transformers", "version": "4.44"},
							"tensorCount":   101,
							"dtypeDominant": "fp32",
							"diskBytes":     267_967_963,
							"coverage":      map[string]string{"quantMethod": domain.CoverageUnavailable},
						}),
						Layers: &[]domain.LayerBlock{
							{Path: "distilbert.embeddings.word_embeddings", OpType: "Embedding", ShapeSignature: "[30522,768]", Dtype: "fp32", ParamCount: ptr(int64(23_440_896))},
							{Path: "distilbert.transformer.layer.*.attention.q_lin", OpType: "Linear", RepeatCount: 6, ShapeSignature: "[768,768]", Dtype: "fp32", ParamCount: ptr(int64(590_592))},
							{Path: "distilbert.transformer.layer.*.ffn.lin1", OpType: "Linear", RepeatCount: 6, ShapeSignature: "[768,3072]", Dtype: "fp32", ParamCount: ptr(int64(2_362_368))},
							{Path: "classifier", OpType: "Linear", ShapeSignature: "[768,2]", Dtype: "fp32", ParamCount: ptr(int64(1_538))},
						},
					},
					{
						SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
						Reporter: "lineage-sdk", ReporterVersion: "1.0.0",
						Facts: facts(map[string]any{
							"paramCountTotal":  66_955_010,
							"paramCountMethod": "from_tensors",
							"weightsBytes":     267_820_040,
							"hashes":           map[string]string{"weights": "sha256:base-weights"},
							"archDoc":          archDoc("fp32"),
							"coverage":         map[string]string{"paramCountTotal": domain.CoverageFilled},
						}),
					},
				},
				Footprints: []seedFootprint{{
					Scenario: "bs1-seq128",
					FootprintInput: core.FootprintInput{
						DeviceClass: "cpu", Batch: ptr(int64(1)), SeqLen: ptr(int64(128)),
						WeightsBytes: ptr(int64(267_820_040)), TotalBytes: ptr(int64(412_090_368)),
						Source: domain.FootprintMeasured,
					},
				}},
				Evaluations: []core.EvaluationInput{eval("sst2", "acc", "validation", 0.9106, &yes)},
			},
			{
				Name: "2.2.0-rc1", Author: "lee@acme.example",
				Description: "Domain-adapted checkpoint; awaiting eval sign-off (stays in draft).",
				Artifacts: []core.ArtifactInput{{
					Name: "weights", URI: "hf://acme/sentiment-support-tickets",
					ModelFormat: &domain.ModelFormat{Name: "transformers", Version: "4.44"},
				}},
				Lineage: []core.LineageInput{derivedFrom("2.1.0")},
				// Same architecture, same precision, different weights — but only the
				// attention projections and the head moved. That partial pattern is a merged
				// adapter, which a checkpoint-level digest could never show (§11.4.2).
				Insight: []core.InsightWrite{{
					SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
					Reporter: "lineage-sdk", ReporterVersion: "1.0.0",
					Facts: facts(map[string]any{
						"hashes":           hashes("d15t1lb3rt-topo", "d15t1lb3rt-shape", "fp32-dtypes", "tuned-weights"),
						"framework":        map[string]string{"name": "pytorch", "version": "2.4.1"},
						"paramCountTotal":  66_955_010,
						"paramCountMethod": "from_tensors",
						"tensorCount":      101,
						"dtypeDominant":    "fp32",
						"diskBytes":        267_967_963,
						"archDoc": archDoc("fp32",
							"classifier.weight",
							"distilbert.transformer.layer.0.attention.q_lin.weight",
							"distilbert.transformer.layer.0.attention.v_lin.weight",
							"distilbert.transformer.layer.1.attention.q_lin.weight",
							"distilbert.transformer.layer.1.attention.v_lin.weight",
						),
					}),
				}},
				Evaluations: []core.EvaluationInput{eval("sst2", "acc", "validation", 0.9231, &yes)},
			},
			{
				Name: "2.2.0-int8", Author: "lee@acme.example",
				Description: "Post-training quantization of the rc1 checkpoint for CPU serving.",
				Artifacts: []core.ArtifactInput{{
					Name: "weights", URI: "hf://acme/sentiment-support-tickets-int8",
					ModelFormat: &domain.ModelFormat{Name: "onnx", Version: "1.16"},
				}},
				// The edge records *how* it was derived; hashes prove the shape is unchanged
				// but cannot prove why the weights moved (§11.3.6, §11.4.4).
				Lineage: []core.LineageInput{
					derivedFromWith("2.2.0-rc1", `{"method":"quantize","from_dtype":"fp32","tool":"onnxruntime"}`),
				},
				Insight: []core.InsightWrite{{
					SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
					Reporter: "lineage-sdk", ReporterVersion: "1.0.0",
					Facts: facts(map[string]any{
						// Same topology and shape, different dtype: the recast row of §11.4.1.
						"hashes":           hashes("d15t1lb3rt-topo", "d15t1lb3rt-shape", "int8-dtypes", "int8-weights"),
						"framework":        map[string]string{"name": "onnxruntime", "version": "1.19"},
						"paramCountTotal":  66_955_010,
						"paramCountMethod": "from_tensors",
						"tensorCount":      101,
						"dtypeDominant":    "int8",
						"quantMethod":      "ptq-static",
						"quantScope":       map[string]any{"excluded": []string{"classifier"}},
						"diskBytes":        67_512_320,
						"archDoc":          archDoc("int8"),
					}),
				}},
				Footprints: []seedFootprint{
					{
						Scenario: "bs1-seq128",
						FootprintInput: core.FootprintInput{
							DeviceClass: "cpu", Batch: ptr(int64(1)), SeqLen: ptr(int64(128)),
							WeightsBytes: ptr(int64(67_108_864)), TotalBytes: ptr(int64(148_897_792)),
							Source: domain.FootprintMeasured,
						},
					},
					{
						// An estimate must carry the assumptions behind it, or the number
						// cannot be interpreted by anyone else (§11.3.3).
						Scenario: "bs32-seq512",
						FootprintInput: core.FootprintInput{
							DeviceClass: "cpu", Batch: ptr(int64(32)), SeqLen: ptr(int64(512)),
							TotalBytes: ptr(int64(1_073_741_824)), Source: domain.FootprintEstimated,
							Basis: props(`{"kvDtype":"int8","activationModel":"peak-per-layer","runtime":"onnxruntime 1.19"}`),
						},
					},
				},
				// The tradeoff: a quarter of the disk, half a point of accuracy.
				Evaluations: []core.EvaluationInput{eval("sst2", "acc", "validation", 0.9178, &yes)},
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
		Versions: []version{
			{
				Name: "0.4.0", Author: "raj@acme.example",
				Description: "Experimental hierarchical model; not yet evaluated.",
				Artifacts: []core.ArtifactInput{{
					Name: "model.xgb", URI: "gs://acme-models/demand-forecast/0.4.0/model.xgb",
					MediaType:   "application/octet-stream",
					ModelFormat: &domain.ModelFormat{Name: "xgboost", Version: "2.0"},
				}},
				Lineage: []core.LineageInput{trainedOn("gs://acme-datasets/supply/orders-2026-06.parquet")},
				Insight: []core.InsightWrite{{
					SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
					Reporter: "lineage-scanner", ReporterVersion: "0.3.1",
					Facts: facts(map[string]any{
						"hashes":        hashes("demand-topo-v1", "demand-shape-v1", "fp32-dtypes", ""),
						"framework":     map[string]string{"name": "xgboost", "version": "2.0"},
						"dtypeDominant": "fp32",
						"coverage":      map[string]string{"weightsBytes": domain.CoverageNotAttempted},
					}),
				}},
			},
			{
				// A scheduled weekly retrain: identical structure, so the header hashes all
				// match. Without a weights hash the diff cannot tell whether the retrain
				// actually moved anything — it narrows to two candidates and names what is
				// missing rather than picking one (§11.4.3).
				Name: "0.4.1", Author: "raj@acme.example",
				Description: "Weekly retrain on July orders; same structure as 0.4.0.",
				Artifacts: []core.ArtifactInput{{
					Name: "model.xgb", URI: "gs://acme-models/demand-forecast/0.4.1/model.xgb",
					MediaType:   "application/octet-stream",
					ModelFormat: &domain.ModelFormat{Name: "xgboost", Version: "2.0"},
				}},
				Lineage: []core.LineageInput{
					derivedFrom("0.4.0"),
					trainedOn("gs://acme-datasets/supply/orders-2026-07.parquet"),
				},
				Insight: []core.InsightWrite{{
					SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
					Reporter: "lineage-scanner", ReporterVersion: "0.3.1",
					Facts: facts(map[string]any{
						"hashes":        hashes("demand-topo-v1", "demand-shape-v1", "fp32-dtypes", ""),
						"framework":     map[string]string{"name": "xgboost", "version": "2.0"},
						"dtypeDominant": "fp32",
						"coverage":      map[string]string{"weightsBytes": domain.CoverageNotAttempted},
					}),
				}},
			},
		},
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
