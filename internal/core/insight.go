package core

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/proseria-research/lineage/internal/domain"
)

// Model-insight writes and reads (§11.6). The registry validates, attributes, audits, and
// stores; it derives nothing from artifacts (§11.1).

// InsightSchemaVersion is the payload contract producers build against. It is versioned
// independently of the API so a producer can detect an older registry (§11.6.1).
const InsightSchemaVersion = "1"

// InsightWrite is the envelope for a fact submission. Everything under Facts is a
// mergeable field; the rest describes the write itself, which is how one attribution can
// be stamped onto every field the write touched.
type InsightWrite struct {
	SchemaVersion   string                     `json:"schemaVersion"`
	Source          domain.FactSource          `json:"source"`
	Reporter        string                     `json:"reporter"`
	ReporterVersion string                     `json:"reporterVersion"`
	Facts           map[string]json.RawMessage `json:"facts"`
	// Layers is a pointer so an absent key leaves the breakdown alone while an explicit
	// (even empty) array replaces it — the set has no meaningful per-element merge.
	Layers *[]domain.LayerBlock `json:"layers"`
}

// EvaluationInput is one reported quality result (append-only, §11.7).
type EvaluationInput struct {
	Suite              string            `json:"suite"`
	Metric             string            `json:"metric"`
	Split              string            `json:"split"`
	Value              float64           `json:"value"`
	HigherIsBetter     *bool             `json:"higherIsBetter"`
	NSamples           *int64            `json:"nSamples"`
	HarnessName        string            `json:"harnessName"`
	HarnessVersion     string            `json:"harnessVersion"`
	Params             json.RawMessage   `json:"params"`
	EvidenceArtifactID string            `json:"evidenceArtifactId"`
	Source             domain.FactSource `json:"source"`
	RunAt              int64             `json:"runAt"`
}

// FootprintInput is one memory scenario (upsert by scenario, §11.6.1).
type FootprintInput struct {
	DeviceClass          string                 `json:"deviceClass"`
	Batch                *int64                 `json:"batch"`
	SeqLen               *int64                 `json:"seqLen"`
	WeightsBytes         *int64                 `json:"weightsBytes"`
	KVCacheBytes         *int64                 `json:"kvCacheBytes"`
	ActivationBytes      *int64                 `json:"activationBytes"`
	RuntimeOverheadBytes *int64                 `json:"runtimeOverheadBytes"`
	TotalBytes           *int64                 `json:"totalBytes"`
	Source               domain.FootprintSource `json:"source"`
	Basis                json.RawMessage        `json:"basis"`
}

// ---- Reads ----

func (s *Service) GetInsight(ctx context.Context, model, version string, includeLayers bool) (*domain.VersionInsight, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	in, err := s.store.GetInsight(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	if includeLayers {
		if in.Layers, err = s.store.ListLayerBlocks(ctx, v.ID); err != nil {
			return nil, err
		}
	}
	return in, nil
}

func (s *Service) ListEvaluations(ctx context.Context, model, version string) ([]*domain.Evaluation, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	return s.store.ListEvaluations(ctx, v.ID)
}

func (s *Service) ListFootprints(ctx context.Context, model, version string) ([]*domain.Footprint, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	return s.store.ListFootprints(ctx, v.ID)
}

// ---- Writes ----

// WriteInsight applies a fact submission. When replace is false (PATCH, the default) the
// write merges field by field over whatever is already recorded, so independent producers
// coexist without coordinating; when true (PUT) it replaces the whole document, which is
// only correct where one producer owns every field (§11.6.1).
//
// force overrides the immutable-fingerprint guard (§11.7) and is itself audited.
func (s *Service) WriteInsight(ctx context.Context, actor, model, version string, w InsightWrite, replace, force bool) (*domain.VersionInsight, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if w.SchemaVersion == "" {
		return nil, domain.Invalid("schemaVersion is required; this registry speaks '" + InsightSchemaVersion + "'")
	}
	if w.SchemaVersion != InsightSchemaVersion {
		return nil, domain.Invalid("unsupported schemaVersion '" + w.SchemaVersion +
			"'; this registry speaks '" + InsightSchemaVersion + "'")
	}
	if !domain.ValidFactSource(w.Source) {
		return nil, domain.Invalid("source must be one of declared|derived|measured (§11.2)")
	}

	now := domain.NowMillis()
	existing, err := s.store.GetInsight(ctx, v.ID)
	if err != nil {
		if !isNotFound(err) {
			return nil, err
		}
		existing = nil
	}

	// Start from what is recorded (merge) or from nothing (replace).
	in := &domain.VersionInsight{VersionID: v.ID, CreatedAt: now, UpdatedAt: now}
	if existing != nil {
		in.CreatedAt = existing.CreatedAt
		if !replace {
			cp := *existing
			in = &cp
			in.Layers = nil
		}
	}
	if in.FieldSources == nil {
		in.FieldSources = map[string]domain.FieldSource{}
	}

	// Apply the submitted fields, rejecting anything the schema does not define. Unknown
	// keys are an error rather than a silent drop so a producer learns it is talking to an
	// older registry instead of believing a fact was recorded (§11.6.1).
	touched, err := applyFacts(in, w.Facts)
	if err != nil {
		return nil, err
	}

	// The fingerprint of a published version is immutable. A conflicting weights hash means
	// the artifact bytes changed underneath it — refuse rather than overwrite (§11.7).
	if existing != nil && !force && existing.Hashes.Weights != "" &&
		in.Hashes.Weights != "" && in.Hashes.Weights != existing.Hashes.Weights {
		return nil, domain.Precondition(
			"weights hash conflicts with the one already recorded for this version; "+
				"a version's content is immutable (§02.5). Retry with ?force=true to override.",
			map[string]any{"recorded": existing.Hashes.Weights, "submitted": in.Hashes.Weights})
	}

	attribution := domain.FieldSource{
		Source: w.Source, Reporter: w.Reporter, ReporterVersion: w.ReporterVersion, At: now,
	}
	for _, f := range touched {
		in.FieldSources[f] = attribution
	}
	in.Source, in.ReporterName, in.ReporterVersion = w.Source, w.Reporter, w.ReporterVersion
	in.UpdatedAt = now
	if len(in.FieldSources) == 0 {
		in.FieldSources = nil
	}

	// Layers are checked before anything is written, so a bad breakdown refuses the whole
	// submission rather than landing the facts without it.
	var blocks []*domain.LayerBlock
	if w.Layers != nil {
		if blocks, err = normalizeLayers(*w.Layers); err != nil {
			return nil, err
		}
		in.FieldSources = ensureMap(in.FieldSources)
		in.FieldSources["layers"] = attribution
	}

	action, summary := "insight.update", "updated insight for "+model+"@"+version
	if replace {
		action, summary = "insight.replace", "replaced insight for "+model+"@"+version
	}
	data, _ := json.Marshal(map[string]any{
		"fields": touched, "source": w.Source, "reporter": w.Reporter, "forced": force,
	})
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.UpsertInsight(ctx, in); err != nil {
			return err
		}
		if w.Layers != nil {
			if err := tx.ReplaceLayerBlocks(ctx, v.ID, blocks); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, actor, action, "model_version", v.ID, summary, data)
	}); err != nil {
		return nil, err
	}
	// Insight can ride along on a resolution (?include=insight, §11.6.3), so a write has to
	// drop that model's cached resolutions.
	s.events.Publish(domain.Event{Type: "insight.updated", Model: model, Version: version})
	return in, nil
}

func (s *Service) AddEvaluation(ctx context.Context, actor, model, version string, in EvaluationInput) (*domain.Evaluation, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if in.Suite == "" || in.Metric == "" {
		return nil, domain.Invalid("evaluation requires suite and metric")
	}
	// Required, not defaulted: guessing the direction from a metric name is how a
	// regression gets reported as an improvement (§11.3.4).
	if in.HigherIsBetter == nil {
		return nil, domain.Invalid("higherIsBetter is required; the sign of a delta cannot be inferred from the metric name")
	}
	if in.Source == "" {
		in.Source = domain.SourceMeasured
	}
	if !domain.ValidFactSource(in.Source) {
		return nil, domain.Invalid("source must be one of declared|derived|measured (§11.2)")
	}
	now := domain.NowMillis()
	if in.RunAt == 0 {
		in.RunAt = now
	}
	e := &domain.Evaluation{
		ID: domain.NewID(), VersionID: v.ID, Suite: in.Suite, Metric: in.Metric, Split: in.Split,
		Value: in.Value, HigherIsBetter: *in.HigherIsBetter, NSamples: in.NSamples,
		HarnessName: in.HarnessName, HarnessVersion: in.HarnessVersion, Params: in.Params,
		EvidenceArtifactID: in.EvidenceArtifactID, Source: in.Source, RunAt: in.RunAt, CreatedAt: now,
	}
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.CreateEvaluation(ctx, e); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "evaluation.create", "model_version", v.ID,
			"recorded "+in.Suite+"/"+in.Metric+" for "+model+"@"+version, nil)
	}); err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Service) PutFootprint(ctx context.Context, actor, model, version, scenario string, in FootprintInput) (*domain.Footprint, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if scenario == "" {
		return nil, domain.Invalid("footprint requires a scenario name")
	}
	if in.Source == "" {
		in.Source = domain.FootprintEstimated
	}
	if !domain.ValidFootprintSource(in.Source) {
		return nil, domain.Invalid("footprint source must be estimated or measured (§11.3.3)")
	}
	// An estimate without its assumptions is not reusable by anyone else (§11.3.3).
	if in.Source == domain.FootprintEstimated && len(in.Basis) == 0 {
		return nil, domain.Invalid("an estimated footprint requires a basis stating its assumptions (§11.3.3)")
	}
	now := domain.NowMillis()
	f := &domain.Footprint{
		ID: domain.NewID(), VersionID: v.ID, Scenario: scenario, DeviceClass: in.DeviceClass,
		Batch: in.Batch, SeqLen: in.SeqLen, WeightsBytes: in.WeightsBytes, KVCacheBytes: in.KVCacheBytes,
		ActivationBytes: in.ActivationBytes, RuntimeOverheadBytes: in.RuntimeOverheadBytes,
		TotalBytes: in.TotalBytes, Source: in.Source, Basis: in.Basis, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.UpsertFootprint(ctx, f); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "footprint.update", "model_version", v.ID,
			"recorded footprint '"+scenario+"' for "+model+"@"+version, nil)
	}); err != nil {
		return nil, err
	}
	return f, nil
}

// ---- Field merge (§11.6.1) ----

// factSetters is the closed set of mergeable fact fields. A raw JSON `null` clears the
// field; an absent key is untouched by construction, since only submitted keys are visited.
var factSetters = map[string]func(*domain.VersionInsight, json.RawMessage) error{
	"framework":           func(in *domain.VersionInsight, r json.RawMessage) error { return setNameVersion(&in.Framework, r) },
	"producer":            func(in *domain.VersionInsight, r json.RawMessage) error { return setNameVersion(&in.Producer, r) },
	"paramCountTotal":     func(in *domain.VersionInsight, r json.RawMessage) error { return setInt(&in.ParamCountTotal, r) },
	"paramCountTrainable": func(in *domain.VersionInsight, r json.RawMessage) error { return setInt(&in.ParamCountTrainable, r) },
	"tensorCount":         func(in *domain.VersionInsight, r json.RawMessage) error { return setInt(&in.TensorCount, r) },
	"diskBytes":           func(in *domain.VersionInsight, r json.RawMessage) error { return setInt(&in.DiskBytes, r) },
	"weightsBytes":        func(in *domain.VersionInsight, r json.RawMessage) error { return setInt(&in.WeightsBytes, r) },
	"quantMethod":         func(in *domain.VersionInsight, r json.RawMessage) error { return setString(&in.QuantMethod, r) },
	"quantScope":          func(in *domain.VersionInsight, r json.RawMessage) error { return setRaw(&in.QuantScope, r) },
	"archDoc":             func(in *domain.VersionInsight, r json.RawMessage) error { return setRaw(&in.ArchDoc, r) },
	"paramCountMethod": func(in *domain.VersionInsight, r json.RawMessage) error {
		var s string
		if err := setString(&s, r); err != nil {
			return err
		}
		if s != "" && !domain.ValidParamCountMethod(domain.ParamCountMethod(s)) {
			return domain.Invalid("paramCountMethod must be one of from_tensors|from_config|declared")
		}
		in.ParamCountMethod = domain.ParamCountMethod(s)
		return nil
	},
	"dtypeDominant": func(in *domain.VersionInsight, r json.RawMessage) error {
		var s string
		if err := setString(&s, r); err != nil {
			return err
		}
		if s != "" && !domain.ValidDtype(domain.Dtype(s)) {
			return domain.Invalid("dtypeDominant must be one of fp32|fp16|bf16|int8|int4|mixed")
		}
		in.DtypeDominant = domain.Dtype(s)
		return nil
	},
}

// InsightFactFields returns every mergeable fact field this registry accepts, sorted.
// The published JSON Schema is checked against it, so the contract producers build
// against cannot drift from what the code actually takes (§11.6.1).
func InsightFactFields() []string {
	out := make([]string, 0, len(factSetters)+2)
	for k := range factSetters {
		out = append(out, k)
	}
	out = append(out, "hashes", "coverage") // handled specially: they merge per sub-key
	sort.Strings(out)
	return out
}

// applyFacts writes each submitted field and returns the field names touched, so the
// caller can stamp one attribution across exactly those (§11.2). `hashes` and `coverage`
// merge per sub-key: a scanner supplies three hashes and the SDK adds the fourth later.
func applyFacts(in *domain.VersionInsight, facts map[string]json.RawMessage) ([]string, error) {
	var touched, unknown []string
	names := make([]string, 0, len(facts))
	for k := range facts {
		names = append(names, k)
	}
	sort.Strings(names) // deterministic error text and audit payloads

	for _, k := range names {
		raw := facts[k]
		switch k {
		case "hashes":
			sub, err := applyHashes(in, raw)
			if err != nil {
				return nil, err
			}
			touched = append(touched, sub...)
		case "coverage":
			if err := mergeCoverage(in, raw); err != nil {
				return nil, err
			}
			touched = append(touched, "coverage")
		default:
			set, ok := factSetters[k]
			if !ok {
				unknown = append(unknown, k)
				continue
			}
			if err := set(in, raw); err != nil {
				return nil, wrapField(k, err)
			}
			touched = append(touched, k)
		}
	}
	if len(unknown) > 0 {
		return nil, domain.Invalid("unknown insight field(s): " + strings.Join(unknown, ", ") +
			" — this registry speaks schemaVersion " + InsightSchemaVersion)
	}
	return touched, nil
}

func applyHashes(in *domain.VersionInsight, raw json.RawMessage) ([]string, error) {
	if isJSONNull(raw) {
		in.Hashes = domain.Hashes{}
		return []string{"hashes"}, nil
	}
	var sub map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sub); err != nil {
		return nil, domain.Invalid("hashes must be an object of {topology,shape,dtype,weights}")
	}
	targets := map[string]*string{
		"topology": &in.Hashes.Topology, "shape": &in.Hashes.Shape,
		"dtype": &in.Hashes.Dtype, "weights": &in.Hashes.Weights,
	}
	keys := make([]string, 0, len(sub))
	for k := range sub {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var touched, unknown []string
	for _, k := range keys {
		t, ok := targets[k]
		if !ok {
			unknown = append(unknown, "hashes."+k)
			continue
		}
		if err := setString(t, sub[k]); err != nil {
			return nil, wrapField("hashes."+k, err)
		}
		touched = append(touched, "hashes."+k)
	}
	if len(unknown) > 0 {
		return nil, domain.Invalid("unknown hash level(s): " + strings.Join(unknown, ", ") +
			" — the ladder is topology, shape, dtype, weights (§11.4)")
	}
	return touched, nil
}

func mergeCoverage(in *domain.VersionInsight, raw json.RawMessage) error {
	if isJSONNull(raw) {
		in.Coverage = nil
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return domain.Invalid("coverage must be an object of field → filled|unavailable_for_format|not_attempted")
	}
	if in.Coverage == nil {
		in.Coverage = map[string]string{}
	}
	for k, v := range m {
		switch v {
		case domain.CoverageFilled, domain.CoverageUnavailable, domain.CoverageNotAttempted:
			in.Coverage[k] = v
		default:
			return domain.Invalid("coverage['" + k + "'] must be filled, unavailable_for_format, or not_attempted")
		}
	}
	return nil
}

// normalizeLayers validates the breakdown and fills the repeat default. Ordinals are
// assigned by position so producers need not track them.
func normalizeLayers(in []domain.LayerBlock) ([]*domain.LayerBlock, error) {
	out := make([]*domain.LayerBlock, 0, len(in))
	for i := range in {
		b := in[i]
		if b.Path == "" {
			return nil, domain.Invalid("every layer block requires a path")
		}
		if b.RepeatCount <= 0 {
			b.RepeatCount = 1
		}
		b.Ordinal = int64(i)
		out = append(out, &b)
	}
	return out, nil
}

// ---- small helpers ----

func isJSONNull(r json.RawMessage) bool {
	return len(r) == 0 || string(r) == "null"
}

func setString(target *string, r json.RawMessage) error {
	if isJSONNull(r) {
		*target = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(r, &s); err != nil {
		return domain.Invalid("expected a string")
	}
	*target = s
	return nil
}

func setInt(target **int64, r json.RawMessage) error {
	if isJSONNull(r) {
		*target = nil
		return nil
	}
	var n int64
	if err := json.Unmarshal(r, &n); err != nil {
		return domain.Invalid("expected an integer")
	}
	if n < 0 {
		return domain.Invalid("must not be negative")
	}
	*target = &n
	return nil
}

func setRaw(target *json.RawMessage, r json.RawMessage) error {
	if isJSONNull(r) {
		*target = nil
		return nil
	}
	*target = append(json.RawMessage(nil), r...)
	return nil
}

func setNameVersion(target **domain.NameVersion, r json.RawMessage) error {
	if isJSONNull(r) {
		*target = nil
		return nil
	}
	var nv domain.NameVersion
	if err := json.Unmarshal(r, &nv); err != nil {
		return domain.Invalid("expected an object of {name,version}")
	}
	*target = &nv
	return nil
}

func wrapField(field string, err error) error {
	if de, ok := err.(*domain.Error); ok {
		return domain.Invalid("field '" + field + "': " + de.Message)
	}
	return err
}

func ensureMap(m map[string]domain.FieldSource) map[string]domain.FieldSource {
	if m == nil {
		return map[string]domain.FieldSource{}
	}
	return m
}

func isNotFound(err error) bool {
	de, ok := err.(*domain.Error)
	return ok && de.Code == domain.CodeNotFound
}
