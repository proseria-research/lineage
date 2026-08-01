package core_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// twoVersions publishes m@a and m@b so diffs have something to compare.
func twoVersions(t *testing.T) (*core.Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	s := newSvc(t)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"a", "b"} {
		if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: v}); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx
}

func facts(kv map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		b, _ := json.Marshal(v)
		out[k] = b
	}
	return out
}

func write(t *testing.T, s *core.Service, version string, w core.InsightWrite, replace bool) *domain.VersionInsight {
	t.Helper()
	if w.SchemaVersion == "" {
		w.SchemaVersion = core.InsightSchemaVersion
	}
	if w.Source == "" {
		w.Source = domain.SourceDerived
	}
	in, err := s.WriteInsight(context.Background(), "ci", "m", version, w, replace, false)
	if err != nil {
		t.Fatalf("WriteInsight(%s): %v", version, err)
	}
	return in
}

// Three producers write disjoint facts about one version. None may clobber another's —
// that is the whole reason PATCH merges per field rather than replacing (§11.6.1).
func TestPatchMergesAcrossProducers(t *testing.T) {
	s, ctx := twoVersions(t)

	write(t, s, "a", core.InsightWrite{
		Reporter: "scanner", Source: domain.SourceDerived,
		Facts: facts(map[string]any{
			"hashes":        map[string]string{"topology": "t1", "shape": "s1", "dtype": "d1"},
			"tensorCount":   int64(291),
			"dtypeDominant": "bf16",
		}),
	}, false)

	write(t, s, "a", core.InsightWrite{
		Reporter: "lineage-sdk", Source: domain.SourceDerived,
		Facts: facts(map[string]any{
			"paramCountTotal":  int64(7_000_000_000),
			"paramCountMethod": "from_tensors",
			"hashes":           map[string]string{"weights": "w1"},
		}),
	}, false)

	write(t, s, "a", core.InsightWrite{
		Reporter: "release-bot", Source: domain.SourceDeclared,
		Facts: facts(map[string]any{"quantMethod": "gptq"}),
	}, false)

	got, err := s.GetInsight(ctx, "m", "a", false)
	if err != nil {
		t.Fatalf("GetInsight: %v", err)
	}
	if got.Hashes.Topology != "t1" || got.Hashes.Weights != "w1" {
		t.Fatalf("later writes must not drop earlier hash levels: %+v", got.Hashes)
	}
	if got.TensorCount == nil || *got.TensorCount != 291 {
		t.Fatalf("scanner's tensorCount lost: %v", got.TensorCount)
	}
	if got.ParamCountTotal == nil || *got.ParamCountTotal != 7_000_000_000 {
		t.Fatalf("sdk's paramCountTotal lost: %v", got.ParamCountTotal)
	}
	if got.QuantMethod != "gptq" || got.DtypeDominant != domain.DtypeBF16 {
		t.Fatalf("field lost across producers: %+v", got)
	}

	// Attribution is per field, so a reader can tell the declared claim from the derived
	// measurement without a second call (§11.2).
	if fs := got.FieldSources["tensorCount"]; fs.Reporter != "scanner" || fs.Source != domain.SourceDerived {
		t.Fatalf("tensorCount attribution: %+v", fs)
	}
	if fs := got.FieldSources["quantMethod"]; fs.Reporter != "release-bot" || fs.Source != domain.SourceDeclared {
		t.Fatalf("quantMethod attribution: %+v", fs)
	}
	if fs := got.FieldSources["hashes.weights"]; fs.Reporter != "lineage-sdk" {
		t.Fatalf("hash-level attribution: %+v", fs)
	}
}

func TestPutReplacesWholeDocument(t *testing.T) {
	s, ctx := twoVersions(t)
	write(t, s, "a", core.InsightWrite{
		Reporter: "scanner",
		Facts:    facts(map[string]any{"tensorCount": int64(291), "quantMethod": "gptq"}),
	}, false)
	write(t, s, "a", core.InsightWrite{
		Reporter: "owner",
		Facts:    facts(map[string]any{"tensorCount": int64(300)}),
	}, true)

	got, _ := s.GetInsight(ctx, "m", "a", false)
	if got.QuantMethod != "" {
		t.Fatalf("PUT should replace the document, quantMethod survived: %q", got.QuantMethod)
	}
	if got.TensorCount == nil || *got.TensorCount != 300 {
		t.Fatalf("PUT value not applied: %v", got.TensorCount)
	}
}

// Absent leaves alone; explicit null clears. Conflating the two would make it impossible
// to retract a fact without wiping the record.
func TestPatchNullClearsAbsentKeeps(t *testing.T) {
	s, ctx := twoVersions(t)
	write(t, s, "a", core.InsightWrite{
		Facts: facts(map[string]any{"tensorCount": int64(291), "quantMethod": "gptq"}),
	}, false)

	write(t, s, "a", core.InsightWrite{Facts: map[string]json.RawMessage{
		"quantMethod": json.RawMessage(`null`),
	}}, false)

	got, _ := s.GetInsight(ctx, "m", "a", false)
	if got.QuantMethod != "" {
		t.Fatalf("explicit null should clear the field, got %q", got.QuantMethod)
	}
	if got.TensorCount == nil || *got.TensorCount != 291 {
		t.Fatalf("absent field should be untouched, got %v", got.TensorCount)
	}
}

func TestWriteInsightRejections(t *testing.T) {
	s, _ := twoVersions(t)
	ctx := context.Background()
	base := func() core.InsightWrite {
		return core.InsightWrite{SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived}
	}

	cases := []struct {
		name string
		w    core.InsightWrite
		code string
	}{
		{"missing schemaVersion", core.InsightWrite{Source: domain.SourceDerived}, domain.CodeInvalidArgument},
		{"unknown schemaVersion", core.InsightWrite{SchemaVersion: "99", Source: domain.SourceDerived}, domain.CodeInvalidArgument},
		{"missing source", core.InsightWrite{SchemaVersion: core.InsightSchemaVersion}, domain.CodeInvalidArgument},
		{"bad source", core.InsightWrite{SchemaVersion: core.InsightSchemaVersion, Source: "guessed"}, domain.CodeInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.WriteInsight(ctx, "ci", "m", "a", tc.w, false, false); err == nil {
				t.Fatal("expected rejection")
			} else if de, ok := err.(*domain.Error); !ok || de.Code != tc.code {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
		})
	}

	// An unknown field must be rejected, not dropped: silently discarding it would leave a
	// producer believing a fact was recorded (§11.6.1).
	w := base()
	w.Facts = facts(map[string]any{"paramCount": int64(7)}) // near-miss for paramCountTotal
	_, err := s.WriteInsight(ctx, "ci", "m", "a", w, false, false)
	if err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
	if de, _ := err.(*domain.Error); de == nil || de.Code != domain.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %v", err)
	}

	w = base()
	w.Facts = facts(map[string]any{"hashes": map[string]string{"topolgy": "t"}})
	if _, err := s.WriteInsight(ctx, "ci", "m", "a", w, false, false); err == nil {
		t.Fatal("expected unknown hash level to be rejected")
	}

	w = base()
	w.Facts = facts(map[string]any{"dtypeDominant": "float8"})
	if _, err := s.WriteInsight(ctx, "ci", "m", "a", w, false, false); err == nil {
		t.Fatal("expected invalid dtype to be rejected")
	}
}

// A version's bytes are immutable, so a contradicting weights hash is a conflict, not an
// update — unless the caller explicitly forces it (§11.7).
func TestWeightsHashConflict(t *testing.T) {
	s, ctx := twoVersions(t)
	write(t, s, "a", core.InsightWrite{Facts: facts(map[string]any{
		"hashes": map[string]string{"topology": "t", "shape": "s", "dtype": "d", "weights": "w1"},
	})}, false)

	w := core.InsightWrite{SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
		Facts: facts(map[string]any{"hashes": map[string]string{"weights": "w2"}})}

	_, err := s.WriteInsight(ctx, "ci", "m", "a", w, false, false)
	if err == nil {
		t.Fatal("expected a conflict on a differing weights hash")
	}
	if de, _ := err.(*domain.Error); de == nil || de.Code != domain.CodeFailedPrecondition {
		t.Fatalf("expected failed_precondition (409), got %v", err)
	}

	// Re-reporting the *same* hash is not a conflict; producers re-run.
	same := core.InsightWrite{SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
		Facts: facts(map[string]any{"hashes": map[string]string{"weights": "w1"}})}
	if _, err := s.WriteInsight(ctx, "ci", "m", "a", same, false, false); err != nil {
		t.Fatalf("re-reporting an identical hash should succeed: %v", err)
	}

	if _, err := s.WriteInsight(ctx, "ci", "m", "a", w, false, true); err != nil {
		t.Fatalf("force should override the guard: %v", err)
	}
	got, _ := s.GetInsight(ctx, "m", "a", false)
	if got.Hashes.Weights != "w2" {
		t.Fatalf("forced write not applied: %+v", got.Hashes)
	}
}

func TestLayerBlocksReplaceAsSet(t *testing.T) {
	s, ctx := twoVersions(t)
	layers := []domain.LayerBlock{
		{Path: "model.embed_tokens", OpType: "Embedding"},
		{Path: "model.layers.*.self_attn.q_proj", OpType: "Linear", RepeatCount: 32},
	}
	write(t, s, "a", core.InsightWrite{Layers: &layers}, false)

	got, err := s.GetInsight(ctx, "m", "a", true)
	if err != nil {
		t.Fatalf("GetInsight: %v", err)
	}
	if len(got.Layers) != 2 {
		t.Fatalf("expected 2 layer blocks, got %d", len(got.Layers))
	}
	if got.Layers[0].RepeatCount != 1 {
		t.Fatalf("repeat count should default to 1, got %d", got.Layers[0].RepeatCount)
	}
	if got.Layers[1].RepeatCount != 32 {
		t.Fatalf("collapsed repeats must survive, got %d", got.Layers[1].RepeatCount)
	}

	// Layers are omitted unless asked for: the breakdown is large and most reads want the
	// scalars (§11.3.1).
	if bare, _ := s.GetInsight(ctx, "m", "a", false); len(bare.Layers) != 0 {
		t.Fatalf("layers should only be hydrated on request, got %d", len(bare.Layers))
	}

	// A write that does not mention layers leaves them alone.
	write(t, s, "a", core.InsightWrite{Facts: facts(map[string]any{"tensorCount": int64(2)})}, false)
	if after, _ := s.GetInsight(ctx, "m", "a", true); len(after.Layers) != 2 {
		t.Fatalf("layers should be untouched by an unrelated write, got %d", len(after.Layers))
	}

	empty := []domain.LayerBlock{}
	write(t, s, "a", core.InsightWrite{Layers: &empty}, false)
	if after, _ := s.GetInsight(ctx, "m", "a", true); len(after.Layers) != 0 {
		t.Fatalf("an explicit empty array should clear the set, got %d", len(after.Layers))
	}
}

func TestFootprintRequiresBasisWhenEstimated(t *testing.T) {
	s, ctx := twoVersions(t)
	_, err := s.PutFootprint(ctx, "ci", "m", "a", "bs1-2k", core.FootprintInput{
		Source: domain.FootprintEstimated, TotalBytes: ptr(int64(20 << 30)),
	})
	if err == nil {
		t.Fatal("an estimate without a basis should be rejected — it cannot be interpreted")
	}
	_, err = s.PutFootprint(ctx, "ci", "m", "a", "bs1-2k", core.FootprintInput{
		Source: domain.FootprintEstimated, TotalBytes: ptr(int64(20 << 30)),
		Basis: json.RawMessage(`{"kvDtype":"fp16"}`),
	})
	if err != nil {
		t.Fatalf("estimate with a basis should be accepted: %v", err)
	}
	// A measurement stands on its own.
	if _, err := s.PutFootprint(ctx, "ci", "m", "a", "measured", core.FootprintInput{
		Source: domain.FootprintMeasured, TotalBytes: ptr(int64(21 << 30)),
	}); err != nil {
		t.Fatalf("measured footprint should not require a basis: %v", err)
	}
}

func TestEvaluationRequiresDirection(t *testing.T) {
	s, ctx := twoVersions(t)
	_, err := s.AddEvaluation(ctx, "ci", "m", "a", core.EvaluationInput{
		Suite: "mmlu", Metric: "acc", Value: 0.7,
	})
	if err == nil {
		t.Fatal("higherIsBetter must be required, not defaulted")
	}
	yes := true
	if _, err := s.AddEvaluation(ctx, "ci", "m", "a", core.EvaluationInput{
		Suite: "mmlu", Metric: "acc", Value: 0.7, HigherIsBetter: &yes,
	}); err != nil {
		t.Fatalf("AddEvaluation: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
