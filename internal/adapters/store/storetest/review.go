package storetest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// RunReviews holds every adapter to identical modification-review behavior (§17.4, §17.5.1):
// rows append rather than replace, the derivation scan is gated on a classification row, the
// parent side is optional, and the queue's "has anyone looked at this" question reads the
// latest row per (version, edge).
//
// It builds its own models, like RunRetention, so a classification or an edge left behind here
// cannot change what an earlier suite thinks it is looking at.
func RunReviews(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	now := domain.NowMillis()

	m := &domain.Model{ID: domain.NewID(), Name: "review-subject", State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	parent := mkVersion(m.ID, "1.0.0")
	child := mkVersion(m.ID, "1.1.0")
	mustCreateVersion(t, store, parent)
	mustCreateVersion(t, store, child)

	edge := &domain.LineageEdge{
		ID: domain.NewID(), SrcType: "model_version", SrcID: child.ID,
		Relation: domain.RelDerivedFrom, DstType: "model_version", DstID: parent.ID,
		Properties: json.RawMessage(`{"method":"quantize"}`), CreatedAt: now,
	}
	if err := store.AddLineageEdge(ctx, edge); err != nil {
		t.Fatalf("AddLineageEdge: %v", err)
	}

	// ---- Condition 3 is a join, not a filter (§17.4) ----
	//
	// The edge exists, but nobody has classified the model, so it is not in the scan at all.
	if rows, err := store.ListDerivations(ctx, domain.RegimeEUAIAct, ""); err != nil {
		t.Fatalf("ListDerivations before classification: %v", err)
	} else if found(rows, child.ID, edge.ID) != nil {
		t.Fatal("an unclassified model must yield no derivation rows")
	}

	if err := store.PutClassification(ctx, &domain.RiskClassification{
		ModelID: m.ID, Regime: domain.RegimeEUAIAct,
		EUSystemRiskClass: domain.EUClassHighAnnexIII, EUGpaiTier: domain.EUGpaiNone,
		IntendedPurpose: "loan adjudication", Basis: "Annex III §5(b)",
		ClassifiedAt: now, ClassifiedBy: "risk@acme.example",
	}); err != nil {
		t.Fatalf("PutClassification: %v", err)
	}

	// ---- The row carries everything the queue needs ----

	rows, err := store.ListDerivations(ctx, domain.RegimeEUAIAct, "")
	if err != nil {
		t.Fatalf("ListDerivations: %v", err)
	}
	d := found(rows, child.ID, edge.ID)
	if d == nil {
		t.Fatal("a classified model's derived_from edge must appear")
	}
	if d.Model != "review-subject" || d.Version != "1.1.0" || d.ModelID != m.ID {
		t.Fatalf("subject naming: %+v", d)
	}
	if d.ParentModel != "review-subject" || d.ParentVersion != "1.0.0" {
		t.Fatalf("parent naming: model=%q version=%q", d.ParentModel, d.ParentVersion)
	}
	if d.EUSystemRiskClass != domain.EUClassHighAnnexIII || d.EUGpaiTier != domain.EUGpaiNone {
		t.Fatalf("classification enums: class=%q tier=%q", d.EUSystemRiskClass, d.EUGpaiTier)
	}
	if string(d.Edge.Properties) != `{"method":"quantize"}` {
		t.Fatalf("declared method must survive: %s", d.Edge.Properties)
	}
	// Neither side reported insight, so both ladders are empty. That is not an error state —
	// it is what Classify reads as `unknown`, which queues (§17.4).
	if d.FromHashes != (domain.Hashes{}) || d.ToHashes != (domain.Hashes{}) {
		t.Fatalf("hashes should be absent: from=%+v to=%+v", d.FromHashes, d.ToHashes)
	}
	if d.LatestReview != nil {
		t.Fatalf("nothing reviewed yet: %+v", d.LatestReview)
	}

	// ---- Hashes come from whichever side has them ----

	if err := store.UpsertInsight(ctx, &domain.VersionInsight{
		VersionID: child.ID, Source: domain.SourceDeclared,
		Hashes:    domain.Hashes{Topology: "t1", Shape: "s1", Dtype: "d2", Weights: "w2"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertInsight child: %v", err)
	}
	if err := store.UpsertInsight(ctx, &domain.VersionInsight{
		VersionID: parent.ID, Source: domain.SourceDeclared,
		Hashes:    domain.Hashes{Topology: "t1", Shape: "s1", Dtype: "d1", Weights: "w1"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertInsight parent: %v", err)
	}
	rows, _ = store.ListDerivations(ctx, domain.RegimeEUAIAct, "")
	d = found(rows, child.ID, edge.ID)
	if d.FromHashes.Dtype != "d1" || d.ToHashes.Dtype != "d2" {
		t.Fatalf("hash ladders must come back per side: from=%+v to=%+v", d.FromHashes, d.ToHashes)
	}

	// ---- Reviews append; the scan reads the latest ----

	first := &domain.ModificationReview{
		ID: domain.NewID(), VersionID: child.ID, EdgeID: edge.ID,
		VerdictAtReview: domain.VerdictRecast, Outcome: domain.OutcomeUndetermined,
		Note: "waiting on counsel", ReviewedBy: "ml@acme.example", ReviewedAt: now,
	}
	if err := store.CreateReview(ctx, first); err != nil {
		t.Fatalf("CreateReview: %v", err)
	}
	second := &domain.ModificationReview{
		ID: domain.NewID(), VersionID: child.ID, EdgeID: edge.ID,
		VerdictAtReview: domain.VerdictRecast, Outcome: domain.OutcomeNotSubstantial,
		Note: "quantization only", ReviewedBy: "counsel@acme.example", ReviewedAt: now + 1000,
	}
	if err := store.CreateReview(ctx, second); err != nil {
		t.Fatalf("CreateReview (re-review): %v", err)
	}

	list, err := store.ListReviews(ctx, child.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("both rows must survive: %v len=%d", err, len(list))
	}
	if list[0].ID != second.ID {
		t.Fatalf("ListReviews must be newest-first, got %q first", list[0].ID)
	}
	if list[1].Outcome != domain.OutcomeUndetermined || list[1].Note != "waiting on counsel" {
		t.Fatalf("the superseded review must be unchanged: %+v", list[1])
	}
	if list[0].VerdictAtReview != domain.VerdictRecast {
		t.Fatalf("frozen verdict round-trip: %q", list[0].VerdictAtReview)
	}

	rows, _ = store.ListDerivations(ctx, domain.RegimeEUAIAct, "")
	d = found(rows, child.ID, edge.ID)
	if d.LatestReview == nil || d.LatestReview.ID != second.ID {
		t.Fatalf("the scan must carry the latest review, got %+v", d.LatestReview)
	}

	// ---- A review for a different edge does not close this one ----

	otherEdge := &domain.LineageEdge{
		ID: domain.NewID(), SrcType: "model_version", SrcID: child.ID,
		Relation: domain.RelDerivedFrom, DstRef: "hf://acme/base-7b", CreatedAt: now + 1,
	}
	if err := store.AddLineageEdge(ctx, otherEdge); err != nil {
		t.Fatalf("AddLineageEdge (external ref): %v", err)
	}
	rows, _ = store.ListDerivations(ctx, domain.RegimeEUAIAct, "")
	od := found(rows, child.ID, otherEdge.ID)
	if od == nil {
		t.Fatal("an edge pointing at an external ref must still appear — it is the Art. 25 case")
	}
	if od.ParentModel != "" || od.ParentVersion != "" {
		t.Fatalf("an external ref has no parent row: %+v", od)
	}
	if od.FromHashes != (domain.Hashes{}) {
		t.Fatalf("an external ref supplies no hashes: %+v", od.FromHashes)
	}
	if od.LatestReview != nil {
		t.Fatal("a review on one edge must not close another")
	}

	// ---- modelID narrows the scan ----

	quiet := &domain.Model{ID: domain.NewID(), Name: "review-bystander", State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModel(ctx, quiet); err != nil {
		t.Fatalf("CreateModel bystander: %v", err)
	}
	if rows, err := store.ListDerivations(ctx, domain.RegimeEUAIAct, quiet.ID); err != nil || len(rows) != 0 {
		t.Fatalf("scoping to another model must return nothing: %v len=%d", err, len(rows))
	}
	if rows, err := store.ListDerivations(ctx, domain.RegimeEUAIAct, m.ID); err != nil || len(rows) != 2 {
		t.Fatalf("scoping to the subject must return its two edges: %v len=%d", err, len(rows))
	}

	// ---- Deleting the edge withdraws the item but keeps the record ----
	//
	// This is why edge_id carries no foreign key (§17.5.1): the queue is a view, the review is
	// evidence, and removing the first must not remove the second.
	if err := store.DeleteLineageEdge(ctx, edge.ID, child.ID); err != nil {
		t.Fatalf("DeleteLineageEdge: %v", err)
	}
	rows, _ = store.ListDerivations(ctx, domain.RegimeEUAIAct, m.ID)
	if found(rows, child.ID, edge.ID) != nil {
		t.Fatal("a deleted edge must leave the queue")
	}
	if list, err := store.ListReviews(ctx, child.ID); err != nil || len(list) != 2 {
		t.Fatalf("reviews must outlive their edge: %v len=%d", err, len(list))
	}

	// ---- But deleting the version does cascade ----

	if err := store.DeleteVersion(ctx, child.ID); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}
	if list, err := store.ListReviews(ctx, child.ID); err != nil || len(list) != 0 {
		t.Fatalf("reviews must cascade with their version: %v len=%d", err, len(list))
	}
}

func found(rows []*domain.DerivationRow, versionID, edgeID string) *domain.DerivationRow {
	for _, d := range rows {
		if d.VersionID == versionID && d.Edge.ID == edgeID {
			return d
		}
	}
	return nil
}
