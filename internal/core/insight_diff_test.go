package core_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

func archDoc(tensors map[string]string) json.RawMessage {
	doc := domain.ArchDoc{}
	for name, digest := range tensors {
		doc.Tensors = append(doc.Tensors, domain.ArchTensor{Name: name, Digest: digest})
	}
	b, _ := json.Marshal(doc)
	return b
}

func diff(t *testing.T, s *core.Service) *core.InsightDiff {
	t.Helper()
	d, err := s.DiffVersions(context.Background(), "m", "a", "m", "b")
	if err != nil {
		t.Fatalf("DiffVersions: %v", err)
	}
	return d
}

// A diff between versions nobody has reported on is `unknown` with the missing input
// named — never a guess (§11.6.2).
func TestDiffWithNoFacts(t *testing.T) {
	s, _ := twoVersions(t)
	d := diff(t, s)
	if d.Verdict != domain.VerdictUnknown {
		t.Fatalf("verdict = %q, want unknown", d.Verdict)
	}
	if len(d.Missing) == 0 || d.Missing[0] != "topology" {
		t.Fatalf("missing should name topology, got %v", d.Missing)
	}
	if d.Basis.FromHasInsight || d.Basis.ToHasInsight {
		t.Fatalf("basis should record that neither side reported: %+v", d.Basis)
	}
	if d.Tensors != nil {
		t.Fatal("no arch docs means no tensor diff, not an empty one")
	}
}

// The registry can only report a partial verdict when a producer submitted only the
// header-derived hashes; it must say which input is missing (§11.4.3).
func TestDiffHeaderOnlyIsNarrowed(t *testing.T) {
	s, _ := twoVersions(t)
	h := map[string]string{"topology": "t", "shape": "s", "dtype": "d"}
	write(t, s, "a", core.InsightWrite{Facts: facts(map[string]any{"hashes": h})}, false)
	write(t, s, "b", core.InsightWrite{Facts: facts(map[string]any{"hashes": h})}, false)

	d := diff(t, s)
	if d.Verdict != domain.VerdictUnknown || len(d.Candidates) != 2 {
		t.Fatalf("expected a narrowed unknown, got %q candidates=%v", d.Verdict, d.Candidates)
	}
	if len(d.Basis.FromHashes) != 3 {
		t.Fatalf("basis should list the three supplied hashes, got %v", d.Basis.FromHashes)
	}
}

// The LoRA case: same topology/shape/dtype, a handful of tensors differ. The verdict is
// `reweighted`, and the tensor summary is what distinguishes a merged adapter from a full
// fine-tune (§11.4.2).
func TestDiffDetectsPartialWeightChange(t *testing.T) {
	s, _ := twoVersions(t)
	base := map[string]string{
		"model.layers.0.self_attn.q_proj": "d1",
		"model.layers.1.self_attn.q_proj": "d2",
		"model.layers.0.mlp.up_proj":      "d3",
		"model.layers.1.mlp.up_proj":      "d4",
		"lm_head":                         "d5",
	}
	after := map[string]string{
		"model.layers.0.self_attn.q_proj": "CHANGED1",
		"model.layers.1.self_attn.q_proj": "CHANGED2",
		"model.layers.0.mlp.up_proj":      "d3",
		"model.layers.1.mlp.up_proj":      "d4",
		"lm_head":                         "d5",
	}
	write(t, s, "a", core.InsightWrite{Facts: facts(map[string]any{
		"hashes": map[string]string{"topology": "t", "shape": "s", "dtype": "d", "weights": "w1"},
	})}, false)
	write(t, s, "b", core.InsightWrite{Facts: facts(map[string]any{
		"hashes": map[string]string{"topology": "t", "shape": "s", "dtype": "d", "weights": "w2"},
	})}, false)
	// archDoc carries the per-tensor digests; submit separately to keep the maps readable.
	writeArch(t, s, "a", base)
	writeArch(t, s, "b", after)

	d := diff(t, s)
	if d.Verdict != domain.VerdictReweighted {
		t.Fatalf("verdict = %q, want reweighted", d.Verdict)
	}
	if d.Tensors == nil {
		t.Fatal("expected a tensor diff from the per-tensor digests")
	}
	if d.Tensors.Changed != 2 || d.Tensors.Unchanged != 3 || d.Tensors.Added != 0 || d.Tensors.Removed != 0 {
		t.Fatalf("tensor counts: %+v", d.Tensors)
	}
	// Repeated block indices collapse, so two changed q_proj tensors read as one pattern.
	if len(d.Tensors.ChangedPatterns) != 1 || d.Tensors.ChangedPatterns[0] != "model.layers.*.self_attn.q_proj" {
		t.Fatalf("changed patterns should collapse block indices, got %v", d.Tensors.ChangedPatterns)
	}
}

func TestDiffScalarDeltas(t *testing.T) {
	s, _ := twoVersions(t)
	write(t, s, "a", core.InsightWrite{Facts: facts(map[string]any{
		"hashes":    map[string]string{"topology": "t", "shape": "s", "dtype": "d16", "weights": "w1"},
		"diskBytes": int64(14_000_000_000),
	})}, false)
	write(t, s, "b", core.InsightWrite{Facts: facts(map[string]any{
		"hashes":    map[string]string{"topology": "t", "shape": "s", "dtype": "d8", "weights": "w2"},
		"diskBytes": int64(7_000_000_000),
	})}, false)

	d := diff(t, s)
	if d.Verdict != domain.VerdictRecast {
		t.Fatalf("quantization should read as recast, got %q", d.Verdict)
	}
	var disk *core.ScalarDelta
	for i := range d.Params {
		if d.Params[i].Field == "diskBytes" {
			disk = &d.Params[i]
		}
	}
	if disk == nil || disk.Delta == nil || *disk.Delta != -7_000_000_000 {
		t.Fatalf("diskBytes delta: %+v", disk)
	}
}

// Metric comparison is only valid within the same suite/metric/split/harness. Anything
// else is reported as not comparable rather than differenced anyway (§11.3.4).
func TestDiffMetricComparability(t *testing.T) {
	s, ctx := twoVersions(t)
	yes := true
	no := false
	add := func(version, suite, metric, split, harness string, value float64, higher *bool) {
		t.Helper()
		if _, err := s.AddEvaluation(ctx, "ci", "m", version, core.EvaluationInput{
			Suite: suite, Metric: metric, Split: split, HarnessVersion: harness,
			Value: value, HigherIsBetter: higher,
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("a", "mmlu", "acc", "5shot", "0.4.2", 0.712, &yes)
	add("b", "mmlu", "acc", "5shot", "0.4.2", 0.698, &yes)
	// Same suite/metric, different shot count: not the same measurement.
	add("a", "mmlu", "acc", "0shot", "0.4.2", 0.55, &yes)
	// Lower is better, and only the "to" side ran it.
	add("b", "wiki", "perplexity", "", "0.4.2", 8.1, &no)

	d := diff(t, s)
	byKey := map[string]core.MetricDelta{}
	for _, m := range d.Metrics {
		byKey[m.Suite+"/"+m.Metric+"/"+m.Split] = m
	}

	acc5 := byKey["mmlu/acc/5shot"]
	if !acc5.Comparable || acc5.Direction != "worse" {
		t.Fatalf("a real regression should read as worse: %+v", acc5)
	}
	if acc5.Delta == nil || *acc5.Delta > -0.013 || *acc5.Delta < -0.015 {
		t.Fatalf("delta should be ~-0.014: %+v", acc5.Delta)
	}

	acc0 := byKey["mmlu/acc/0shot"]
	if acc0.Comparable || acc0.Reason == "" {
		t.Fatalf("a split present on one side only must not be comparable: %+v", acc0)
	}

	ppl := byKey["wiki/perplexity/"]
	if ppl.Comparable || ppl.Reason == "" {
		t.Fatalf("metric only on the to side must not be comparable: %+v", ppl)
	}
}

// higherIsBetter applies to the direction, so a falling perplexity is an improvement.
func TestDiffLowerIsBetterDirection(t *testing.T) {
	s, ctx := twoVersions(t)
	no := false
	for _, tc := range []struct {
		version string
		value   float64
	}{{"a", 9.4}, {"b", 8.1}} {
		if _, err := s.AddEvaluation(ctx, "ci", "m", tc.version, core.EvaluationInput{
			Suite: "wiki", Metric: "perplexity", Value: tc.value, HigherIsBetter: &no,
		}); err != nil {
			t.Fatal(err)
		}
	}
	d := diff(t, s)
	if len(d.Metrics) != 1 || d.Metrics[0].Direction != "better" {
		t.Fatalf("falling perplexity should read as better: %+v", d.Metrics)
	}
}

func TestDiffFootprintsByScenario(t *testing.T) {
	s, ctx := twoVersions(t)
	put := func(version, scenario string, total int64, src domain.FootprintSource) {
		t.Helper()
		if _, err := s.PutFootprint(ctx, "ci", "m", version, scenario, core.FootprintInput{
			Source: src, TotalBytes: &total, Basis: json.RawMessage(`{"kvDtype":"fp16"}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("a", "bs1-2k", 20<<30, domain.FootprintEstimated)
	put("b", "bs1-2k", 11<<30, domain.FootprintMeasured)
	put("b", "bs32-8k", 60<<30, domain.FootprintEstimated)

	d := diff(t, s)
	byScenario := map[string]core.FootprintDelta{}
	for _, f := range d.Footprints {
		byScenario[f.Scenario] = f
	}
	shared := byScenario["bs1-2k"]
	if !shared.Comparable || shared.Delta == nil || *shared.Delta != -(9<<30) {
		t.Fatalf("shared scenario delta: %+v", shared)
	}
	// The sources differ; the reader needs to know one side is an estimate.
	if shared.FromSource != "estimated" || shared.ToSource != "measured" {
		t.Fatalf("footprint sources should be carried through: %+v", shared)
	}
	if only := byScenario["bs32-8k"]; only.Comparable {
		t.Fatalf("a scenario measured on one side only is not comparable: %+v", only)
	}
}

// Cross-model diff: a distilled student against its teacher (§11.6).
func TestDiffAcrossModels(t *testing.T) {
	s, ctx := twoVersions(t)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "student"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "student", core.PublishVersionInput{Name: "1"}); err != nil {
		t.Fatal(err)
	}
	write(t, s, "a", core.InsightWrite{Facts: facts(map[string]any{
		"hashes": map[string]string{"topology": "teacher", "shape": "s1", "dtype": "d", "weights": "w1"},
	})}, false)
	w := core.InsightWrite{SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
		Facts: facts(map[string]any{
			"hashes": map[string]string{"topology": "student", "shape": "s2", "dtype": "d", "weights": "w2"},
		})}
	if _, err := s.WriteInsight(ctx, "ci", "student", "1", w, false, false); err != nil {
		t.Fatal(err)
	}

	d, err := s.DiffVersions(ctx, "m", "a", "student", "1")
	if err != nil {
		t.Fatalf("cross-model diff: %v", err)
	}
	if d.Verdict != domain.VerdictRearchitected {
		t.Fatalf("verdict = %q, want rearchitected", d.Verdict)
	}
	if d.From.Model != "m" || d.To.Model != "student" {
		t.Fatalf("both sides should be identified: %+v %+v", d.From, d.To)
	}
}

func writeArch(t *testing.T, s *core.Service, version string, tensors map[string]string) {
	t.Helper()
	write(t, s, version, core.InsightWrite{Facts: map[string]json.RawMessage{
		"archDoc": archDoc(tensors),
	}}, false)
}
