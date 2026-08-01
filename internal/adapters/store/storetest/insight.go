package storetest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// runInsights holds every adapter to identical model-insight behavior (§11.3): upsert-whole
// insight, replace-as-a-set layer blocks, upsert-by-scenario footprints, append-only
// evaluations, and nullable numerics that survive a round-trip as nulls.
func runInsights(t *testing.T, store domain.MetadataStore, versionID string) {
	t.Helper()
	ctx := context.Background()
	now := domain.NowMillis()

	if _, err := store.GetInsight(ctx, versionID); err == nil {
		t.Fatal("expected not_found before any insight is recorded")
	} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}

	in := &domain.VersionInsight{
		VersionID:        versionID,
		Framework:        &domain.NameVersion{Name: "pytorch", Version: "2.4.1"},
		ParamCountTotal:  i64(7_000_000_000),
		ParamCountMethod: domain.ParamsFromTensors,
		DtypeDominant:    domain.DtypeBF16,
		Hashes:           domain.Hashes{Topology: "t1", Shape: "s1", Dtype: "d1", Weights: "w1"},
		ArchDoc:          json.RawMessage(`{"tensors":[{"name":"a","digest":"x"}]}`),
		Source:           domain.SourceDerived,
		ReporterName:     "lineage-sdk",
		FieldSources: map[string]domain.FieldSource{
			"paramCountTotal": {Source: domain.SourceDerived, Reporter: "lineage-sdk", At: now},
		},
		Coverage:  map[string]string{"quantMethod": domain.CoverageUnavailable},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpsertInsight(ctx, in); err != nil {
		t.Fatalf("UpsertInsight: %v", err)
	}
	got, err := store.GetInsight(ctx, versionID)
	if err != nil {
		t.Fatalf("GetInsight: %v", err)
	}
	if got.Framework == nil || got.Framework.Name != "pytorch" || got.Framework.Version != "2.4.1" {
		t.Fatalf("framework round-trip: %+v", got.Framework)
	}
	if got.ParamCountTotal == nil || *got.ParamCountTotal != 7_000_000_000 {
		t.Fatalf("paramCountTotal round-trip: %v", got.ParamCountTotal)
	}
	// An unsupplied numeric must stay null, not become zero — the difference between
	// "not reported" and "reported as 0" is load-bearing (§11.2).
	if got.TensorCount != nil {
		t.Fatalf("absent tensorCount should round-trip as nil, got %v", *got.TensorCount)
	}
	if got.Hashes.Weights != "w1" || got.Hashes.Topology != "t1" {
		t.Fatalf("hash ladder round-trip: %+v", got.Hashes)
	}
	if got.Source != domain.SourceDerived || got.ReporterName != "lineage-sdk" {
		t.Fatalf("provenance round-trip: %+v", got)
	}
	if fs, ok := got.FieldSources["paramCountTotal"]; !ok || fs.Reporter != "lineage-sdk" {
		t.Fatalf("fieldSources round-trip: %+v", got.FieldSources)
	}
	if got.Coverage["quantMethod"] != domain.CoverageUnavailable {
		t.Fatalf("coverage round-trip: %+v", got.Coverage)
	}
	if len(got.ArchDoc) == 0 {
		t.Fatal("archDoc should round-trip")
	}

	// Upsert replaces: the second write is the whole intended state.
	in.DtypeDominant = domain.DtypeInt8
	in.ParamCountTotal = nil
	in.UpdatedAt = now + 1
	if err := store.UpsertInsight(ctx, in); err != nil {
		t.Fatalf("UpsertInsight (replace): %v", err)
	}
	got, _ = store.GetInsight(ctx, versionID)
	if got.DtypeDominant != domain.DtypeInt8 || got.ParamCountTotal != nil {
		t.Fatalf("upsert should replace the row: %+v", got)
	}

	// Layer blocks replace as a set, and repeats stay collapsed.
	blocks := []*domain.LayerBlock{
		{Ordinal: 0, Path: "model.embed_tokens", OpType: "Embedding", RepeatCount: 1, ParamCount: i64(131072)},
		{Ordinal: 1, Path: "model.layers.*.self_attn.q_proj", OpType: "Linear", RepeatCount: 32, ParamCount: i64(16777216)},
	}
	if err := store.ReplaceLayerBlocks(ctx, versionID, blocks); err != nil {
		t.Fatalf("ReplaceLayerBlocks: %v", err)
	}
	ls, err := store.ListLayerBlocks(ctx, versionID)
	if err != nil || len(ls) != 2 {
		t.Fatalf("ListLayerBlocks: %v len=%d", err, len(ls))
	}
	if ls[0].Ordinal != 0 || ls[1].RepeatCount != 32 {
		t.Fatalf("layer blocks should come back ordered with repeats intact: %+v %+v", ls[0], ls[1])
	}
	if err := store.ReplaceLayerBlocks(ctx, versionID, blocks[:1]); err != nil {
		t.Fatalf("ReplaceLayerBlocks (shrink): %v", err)
	}
	if ls, _ = store.ListLayerBlocks(ctx, versionID); len(ls) != 1 {
		t.Fatalf("replace should swap the whole set, got len=%d", len(ls))
	}

	// Footprints upsert by scenario.
	fp := &domain.Footprint{ID: domain.NewID(), VersionID: versionID, Scenario: "bs1-2k",
		DeviceClass: "a100-80g", Batch: i64(1), SeqLen: i64(2048), TotalBytes: i64(20 << 30),
		Source: domain.FootprintEstimated, Basis: json.RawMessage(`{"kvDtype":"fp16"}`),
		CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertFootprint(ctx, fp); err != nil {
		t.Fatalf("UpsertFootprint: %v", err)
	}
	fp2 := *fp
	fp2.ID = domain.NewID()
	fp2.Source = domain.FootprintMeasured
	fp2.TotalBytes = i64(21 << 30)
	if err := store.UpsertFootprint(ctx, &fp2); err != nil {
		t.Fatalf("UpsertFootprint (same scenario): %v", err)
	}
	fps, err := store.ListFootprints(ctx, versionID)
	if err != nil || len(fps) != 1 {
		t.Fatalf("same scenario must upsert, not append: %v len=%d", err, len(fps))
	}
	if fps[0].Source != domain.FootprintMeasured || *fps[0].TotalBytes != 21<<30 {
		t.Fatalf("footprint upsert should supersede: %+v", fps[0])
	}
	if len(fps[0].Basis) == 0 {
		t.Fatal("footprint basis should round-trip — an estimate without its basis is unusable")
	}

	// Evaluations append: a re-run never overwrites, so regressions stay visible (§11.7).
	ev := &domain.Evaluation{ID: domain.NewID(), VersionID: versionID, Suite: "mmlu", Metric: "acc",
		Split: "5shot", Value: 0.712, HigherIsBetter: true, NSamples: i64(14042),
		HarnessName: "lm-eval", HarnessVersion: "0.4.2", Source: domain.SourceMeasured,
		RunAt: now, CreatedAt: now}
	if err := store.CreateEvaluation(ctx, ev); err != nil {
		t.Fatalf("CreateEvaluation: %v", err)
	}
	ev2 := *ev
	ev2.ID = domain.NewID()
	ev2.Value = 0.698
	ev2.RunAt = now + 1000
	if err := store.CreateEvaluation(ctx, &ev2); err != nil {
		t.Fatalf("CreateEvaluation (re-run): %v", err)
	}
	evs, err := store.ListEvaluations(ctx, versionID)
	if err != nil || len(evs) != 2 {
		t.Fatalf("evaluations must append: %v len=%d", err, len(evs))
	}
	if evs[0].RunAt != now+1000 {
		t.Fatalf("evaluations should list newest run first: %+v", evs[0])
	}
	if !evs[0].HigherIsBetter || evs[0].NSamples == nil || *evs[0].NSamples != 14042 {
		t.Fatalf("evaluation round-trip: %+v", evs[0])
	}
}

func i64(v int64) *int64 { return &v }
