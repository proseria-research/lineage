package domain

import "encoding/json"

// Model-insight entities (§11). The registry stores these facts; it never derives them —
// producers outside the registry (SDK, scanner, eval harness, load test) submit them and
// Lineage validates, attributes, audits, and queries (§11.1).

// ---- Enums (§11.2, §11.3.1) ----

// FactSource records how a value was obtained, so a reader can weigh it (§11.2).
type FactSource string

const (
	SourceDeclared FactSource = "declared" // asserted by the publisher; attributed, not verified
	SourceDerived  FactSource = "derived"  // computed by a producer from the model itself
	SourceMeasured FactSource = "measured" // reported by a runtime or harness, valid within its basis
)

func ValidFactSource(s FactSource) bool {
	return s == SourceDeclared || s == SourceDerived || s == SourceMeasured
}

// ParamCountMethod distinguishes how a parameter count was arrived at; the methods can
// disagree (tied embeddings, sharded checkpoints), so the record states which was used.
type ParamCountMethod string

const (
	ParamsFromTensors ParamCountMethod = "from_tensors"
	ParamsFromConfig  ParamCountMethod = "from_config"
	ParamsDeclared    ParamCountMethod = "declared"
)

func ValidParamCountMethod(m ParamCountMethod) bool {
	return m == ParamsFromTensors || m == ParamsFromConfig || m == ParamsDeclared
}

// Dtype is the dominant numeric precision of a version's weights.
type Dtype string

const (
	DtypeFP32  Dtype = "fp32"
	DtypeFP16  Dtype = "fp16"
	DtypeBF16  Dtype = "bf16"
	DtypeInt8  Dtype = "int8"
	DtypeInt4  Dtype = "int4"
	DtypeMixed Dtype = "mixed"
)

func ValidDtype(d Dtype) bool {
	switch d {
	case DtypeFP32, DtypeFP16, DtypeBF16, DtypeInt8, DtypeInt4, DtypeMixed:
		return true
	}
	return false
}

// FootprintSource narrows FactSource for footprints: an estimate or a measurement.
type FootprintSource string

const (
	FootprintEstimated FootprintSource = "estimated"
	FootprintMeasured  FootprintSource = "measured"
)

func ValidFootprintSource(s FootprintSource) bool {
	return s == FootprintEstimated || s == FootprintMeasured
}

// Coverage states, per field, why a producer did or did not supply a value (§11.2). It
// distinguishes "the format does not expose this" from "not attempted".
const (
	CoverageFilled       = "filled"
	CoverageUnavailable  = "unavailable_for_format"
	CoverageNotAttempted = "not_attempted"
)

// ---- Provenance (§11.2) ----

// FieldSource attributes one field of an insight to the write that set it. Provenance is
// per field, not per record, because independent producers each supply different facts.
type FieldSource struct {
	Source          FactSource `json:"source"`
	Reporter        string     `json:"reporter,omitempty"`
	ReporterVersion string     `json:"reporterVersion,omitempty"`
	At              int64      `json:"at"`
}

// ---- Entities (§11.3) ----

// Hashes is the four-level architecture fingerprint (§11.4). Separating the levels makes
// the diff verdict a table lookup rather than a heuristic. `Weights` is optional: a
// producer that only read file headers submits the first three (§11.4.3).
type Hashes struct {
	Topology string `json:"topology,omitempty"`
	Shape    string `json:"shape,omitempty"`
	Dtype    string `json:"dtype,omitempty"`
	Weights  string `json:"weights,omitempty"`
}

// NameVersion is a tool or library identified by name and version.
type NameVersion struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// VersionInsight is the 1:1 composition record for a model version (§11.3.1). Numeric
// facts are pointers so a merge can distinguish "absent" (leave alone) from an explicit
// zero, and an explicit null (clear) from both.
type VersionInsight struct {
	VersionID string `json:"versionId"`

	Framework *NameVersion `json:"framework,omitempty"` // e.g. pytorch / 2.4.1
	Producer  *NameVersion `json:"producer,omitempty"`  // library that wrote the files, e.g. transformers

	ParamCountTotal     *int64           `json:"paramCountTotal,omitempty"`
	ParamCountTrainable *int64           `json:"paramCountTrainable,omitempty"`
	ParamCountMethod    ParamCountMethod `json:"paramCountMethod,omitempty"`
	TensorCount         *int64           `json:"tensorCount,omitempty"`

	DtypeDominant Dtype           `json:"dtypeDominant,omitempty"`
	QuantMethod   string          `json:"quantMethod,omitempty"`
	QuantScope    json.RawMessage `json:"quantScope,omitempty"`

	DiskBytes    *int64 `json:"diskBytes,omitempty"`
	WeightsBytes *int64 `json:"weightsBytes,omitempty"`

	Hashes  Hashes          `json:"hashes"`
	ArchDoc json.RawMessage `json:"archDoc,omitempty"`

	Source          FactSource             `json:"source"`
	FieldSources    map[string]FieldSource `json:"fieldSources,omitempty"`
	ReporterName    string                 `json:"reporterName,omitempty"`
	ReporterVersion string                 `json:"reporterVersion,omitempty"`
	Coverage        map[string]string      `json:"coverage,omitempty"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`

	// Layers is hydrated only on ?include=layers; it lives in its own table (§11.3.2).
	Layers []*LayerBlock `json:"layers,omitempty"`
}

// LayerBlock is one distinct block of the architecture, collapsed over repeats (§11.3.2).
// An 80-layer decoder contributes one row per distinct sub-module with RepeatCount=80,
// not eighty copies. ParamCount and Bytes are per single instance.
type LayerBlock struct {
	VersionID      string `json:"-"`
	Ordinal        int64  `json:"ordinal"`
	Path           string `json:"path"` // e.g. model.layers.*.self_attn.q_proj
	OpType         string `json:"opType,omitempty"`
	RepeatCount    int64  `json:"repeatCount"`
	ShapeSignature string `json:"shapeSignature,omitempty"`
	Dtype          string `json:"dtype,omitempty"`
	ParamCount     *int64 `json:"paramCount,omitempty"`
	Bytes          *int64 `json:"bytes,omitempty"`
}

// Footprint is one memory scenario for a version (§11.3.3). In-memory size is a function
// of dtype, batch, sequence length, KV cache, and framework overhead, so each row carries
// the assumptions that produced it rather than pretending to a single number.
type Footprint struct {
	ID          string `json:"id"`
	VersionID   string `json:"-"`
	Scenario    string `json:"scenario"`
	DeviceClass string `json:"deviceClass,omitempty"`

	Batch  *int64 `json:"batch,omitempty"`
	SeqLen *int64 `json:"seqLen,omitempty"`

	WeightsBytes         *int64 `json:"weightsBytes,omitempty"`
	KVCacheBytes         *int64 `json:"kvCacheBytes,omitempty"`
	ActivationBytes      *int64 `json:"activationBytes,omitempty"`
	RuntimeOverheadBytes *int64 `json:"runtimeOverheadBytes,omitempty"`
	TotalBytes           *int64 `json:"totalBytes,omitempty"`

	Source FootprintSource `json:"source"`
	Basis  json.RawMessage `json:"basis,omitempty"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}

// Evaluation is one measured quality result (§11.3.4). Append-only: a re-run is a new row
// with its own RunAt and harness version, so regressions stay visible.
type Evaluation struct {
	ID        string `json:"id"`
	VersionID string `json:"-"`

	Suite  string  `json:"suite"`
	Metric string  `json:"metric"`
	Split  string  `json:"split,omitempty"`
	Value  float64 `json:"value"`
	// HigherIsBetter is required: the sign of a delta cannot be inferred from the name.
	HigherIsBetter bool   `json:"higherIsBetter"`
	NSamples       *int64 `json:"nSamples,omitempty"`

	HarnessName    string `json:"harnessName,omitempty"`
	HarnessVersion string `json:"harnessVersion,omitempty"`

	Params json.RawMessage `json:"params,omitempty"`
	// EvidenceArtifactID points at an artifact of kind METRICS holding the verbatim report.
	EvidenceArtifactID string `json:"evidenceArtifactId,omitempty"`

	Source    FactSource `json:"source"`
	RunAt     int64      `json:"runAt,omitempty"`
	CreatedAt int64      `json:"createdAt"`
}

// ---- Canonical arch doc (§11.4.2) ----

// ArchTensor is the one structure the registry reads out of the otherwise opaque
// `archDoc`: the per-tensor digest list backing the partial diff. Producers may carry any
// additional keys; unparseable docs simply yield no tensor-level diff.
type ArchTensor struct {
	Name   string  `json:"name"`
	Shape  []int64 `json:"shape,omitempty"`
	Dtype  string  `json:"dtype,omitempty"`
	Digest string  `json:"digest,omitempty"`
}

// ArchDoc is the canonical normalized graph summary that the hashes are computed over.
type ArchDoc struct {
	Tensors []ArchTensor `json:"tensors,omitempty"`
}
