package core_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// Modification review (§17). The queue is four conditions evaluated at read time; each test
// below moves exactly one of them.

// reviewFixture builds the §17.4 subject: model "m" classified high_annex_iii, a parent
// version 1.0.0 and a child 1.1.0 derived from it, declared as a quantization. Neither side
// has reported insight yet, so the verdict starts at `unknown`.
func reviewFixture(t *testing.T) (*core.Service, context.Context, string) {
	t.Helper()
	ctx := context.Background()
	s := newSvc(t)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"1.0.0", "1.1.0"} {
		if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	in := core.LineageInput{Relation: domain.RelDerivedFrom, Properties: json.RawMessage(`{"method":"quantize"}`)}
	in.To.Version = "1.0.0"
	e, err := s.AddLineage(ctx, "ml@acme.example", "m", "1.1.0", in)
	if err != nil {
		t.Fatalf("AddLineage: %v", err)
	}
	if _, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, highRisk()); err != nil {
		t.Fatalf("SetClassification: %v", err)
	}
	return s, ctx, e.ID
}

// hashes writes one version's hash ladder as a producer would.
func hashes(t *testing.T, s *core.Service, model, version string, h map[string]string) {
	t.Helper()
	w := core.InsightWrite{
		SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
		Facts: facts(map[string]any{"hashes": h}),
	}
	if _, err := s.WriteInsight(context.Background(), "ci", model, version, w, false, false); err != nil {
		t.Fatalf("WriteInsight(%s@%s): %v", model, version, err)
	}
}

func queue(t *testing.T, s *core.Service, status domain.ReviewStatus) []*domain.ReviewItem {
	t.Helper()
	items, _, err := s.ListReviewQueue(context.Background(), eu, status, domain.ListOptions{})
	if err != nil {
		t.Fatalf("ListReviewQueue(%q): %v", status, err)
	}
	return items
}

// The M16 acceptance, end to end: a fine-tune of a high-risk model shows up open with its
// verdict and its declared method, and recording a review closes it.
func TestReviewQueueOpensAndCloses(t *testing.T) {
	s, ctx, edgeID := reviewFixture(t)
	hashes(t, s, "m", "1.0.0", map[string]string{"topology": "t", "shape": "sh", "dtype": "d", "weights": "w1"})
	hashes(t, s, "m", "1.1.0", map[string]string{"topology": "t", "shape": "sh", "dtype": "d", "weights": "w2"})

	open := queue(t, s, domain.ReviewOpen)
	if len(open) != 1 {
		t.Fatalf("open queue = %d items, want 1", len(open))
	}
	it := open[0]
	if it.Verdict != domain.VerdictReweighted {
		t.Fatalf("verdict = %q, want reweighted", it.Verdict)
	}
	if it.DeclaredMethod != "quantize" {
		t.Fatalf("declaredMethod = %q", it.DeclaredMethod)
	}
	// The declared intent and the measurement disagree here — quantization would change the
	// dtype hash, and it did not. Surfacing both is the whole point (§17.3); the registry
	// still does not adjudicate which one is wrong.
	if it.DerivedFrom == nil || it.DerivedFrom.Version != "1.0.0" {
		t.Fatalf("derivedFrom = %+v", it.DerivedFrom)
	}
	if it.EUSystemRiskClass != domain.EUClassHighAnnexIII {
		t.Fatalf("class = %q", it.EUSystemRiskClass)
	}
	if len(it.Basis.FromHashes) != 4 || len(it.Basis.ToHashes) != 4 {
		t.Fatalf("basis should name all four levels per side: %+v", it.Basis)
	}
	if it.Review != nil {
		t.Fatalf("an open item carries no review: %+v", it.Review)
	}

	r, err := s.RecordReview(ctx, "counsel@acme.example", "m", "1.1.0", core.ReviewInput{
		EdgeID: edgeID, Outcome: domain.OutcomeNotSubstantial, Note: "envelope unchanged",
	})
	if err != nil {
		t.Fatalf("RecordReview: %v", err)
	}
	if r.VerdictAtReview != domain.VerdictReweighted || r.ReviewedBy != "counsel@acme.example" {
		t.Fatalf("server-set fields: %+v", r)
	}

	if got := queue(t, s, domain.ReviewOpen); len(got) != 0 {
		t.Fatalf("recording a review must close the item, still open: %+v", got)
	}
	closed := queue(t, s, domain.ReviewClosed)
	if len(closed) != 1 || closed[0].Review == nil || closed[0].Review.ID != r.ID {
		t.Fatalf("closed queue: %+v", closed)
	}
	if closed[0].Review.Outcome != domain.OutcomeNotSubstantial {
		t.Fatalf("outcome = %q", closed[0].Review.Outcome)
	}
	// No status filter returns both kinds; here there is only the one item, but the call must
	// not silently mean "open".
	if got := queue(t, s, ""); len(got) != 1 {
		t.Fatalf("unfiltered queue = %d", len(got))
	}
}

// §17.4 condition 2: a repackage moved no bytes, so there is nothing to review.
func TestQueueSkipsIdentical(t *testing.T) {
	s, _, _ := reviewFixture(t)
	same := map[string]string{"topology": "t", "shape": "sh", "dtype": "d", "weights": "w"}
	hashes(t, s, "m", "1.0.0", same)
	hashes(t, s, "m", "1.1.0", same)

	if got := queue(t, s, ""); len(got) != 0 {
		t.Fatalf("identical must not queue: %+v", got[0])
	}
}

// §17.4: "we cannot tell what changed" is precisely the case that wants human eyes. Excluding
// it would make a missing weights_hash read as a clean bill of health.
func TestQueueIncludesUnknown(t *testing.T) {
	s, _, _ := reviewFixture(t)
	// Neither side reported anything at all.
	open := queue(t, s, domain.ReviewOpen)
	if len(open) != 1 || open[0].Verdict != domain.VerdictUnknown {
		t.Fatalf("unknown must queue: %+v", open)
	}
	if len(open[0].Missing) == 0 {
		t.Fatalf("an unknown verdict must name what is missing: %+v", open[0])
	}
	if len(open[0].Basis.FromHashes) != 0 || len(open[0].Basis.ToHashes) != 0 {
		t.Fatalf("basis must show both sides empty: %+v", open[0].Basis)
	}
}

// §17.4 condition 3: Art. 25 is not in play, so nothing is routed. Both halves of the gate are
// moved independently — the class down, and the tier up.
func TestQueueGatesOnClassification(t *testing.T) {
	s, ctx, _ := reviewFixture(t)
	hashes(t, s, "m", "1.0.0", map[string]string{"topology": "t", "shape": "sh", "dtype": "d", "weights": "w1"})
	hashes(t, s, "m", "1.1.0", map[string]string{"topology": "t", "shape": "sh", "dtype": "d", "weights": "w2"})
	if len(queue(t, s, "")) != 1 {
		t.Fatal("fixture should start queued")
	}

	limited := core.ClassificationInput{
		EUSystemRiskClass: domain.EUClassLimited, EUGpaiTier: domain.EUGpaiNone,
		IntendedPurpose: "internal triage",
	}
	if _, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, limited); err != nil {
		t.Fatal(err)
	}
	if got := queue(t, s, ""); len(got) != 0 {
		t.Fatalf("a limited-risk model must not queue: %+v", got[0])
	}

	// The GPAI tier qualifies on its own: a derived GPAI picks up its own Art. 53 duties.
	limited.EUGpaiTier = domain.EUGpai
	if _, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, limited); err != nil {
		t.Fatal(err)
	}
	if got := queue(t, s, ""); len(got) != 1 {
		t.Fatalf("a GPAI tier must queue on its own: %d items", len(got))
	}
}

// The reason the column exists (§17.4): a producer submitting a weights_hash after the review
// must not be able to change what the reviewer is recorded as having seen.
func TestVerdictAtReviewIsFrozen(t *testing.T) {
	s, ctx, edgeID := reviewFixture(t)

	// Reviewed while the hashes did not reach.
	r, err := s.RecordReview(ctx, "counsel@acme.example", "m", "1.1.0", core.ReviewInput{
		EdgeID: edgeID, Outcome: domain.OutcomeUndetermined, Note: "no facts to go on",
	})
	if err != nil {
		t.Fatalf("RecordReview: %v", err)
	}
	if r.VerdictAtReview != domain.VerdictUnknown {
		t.Fatalf("verdictAtReview = %q, want unknown", r.VerdictAtReview)
	}

	// A producer fills in the ladder afterwards.
	hashes(t, s, "m", "1.0.0", map[string]string{"topology": "t", "shape": "sh", "dtype": "d1", "weights": "w1"})
	hashes(t, s, "m", "1.1.0", map[string]string{"topology": "t", "shape": "sh", "dtype": "d2", "weights": "w2"})

	closed := queue(t, s, domain.ReviewClosed)
	if len(closed) != 1 {
		t.Fatalf("closed queue = %d", len(closed))
	}
	if closed[0].Verdict != domain.VerdictRecast {
		t.Fatalf("the current verdict must move: %q", closed[0].Verdict)
	}
	if closed[0].Review.VerdictAtReview != domain.VerdictUnknown {
		t.Fatalf("the frozen verdict must not: %q", closed[0].Review.VerdictAtReview)
	}
	// Both are returned precisely so the divergence is visible (§17.7).
	if closed[0].Verdict == closed[0].Review.VerdictAtReview {
		t.Fatal("this test is pointless if the two agree")
	}
}

// Rows are append-only; the queue keys on the latest per (version, edge) (§17.4).
func TestReReviewSupersedesWithoutErasing(t *testing.T) {
	s, ctx, edgeID := reviewFixture(t)

	first, err := s.RecordReview(ctx, "ml@acme.example", "m", "1.1.0", core.ReviewInput{
		EdgeID: edgeID, Outcome: domain.OutcomeUndetermined, Note: "escalating",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Distinct milliseconds. Within one, the id tiebreak orders stably but arbitrarily — NewID
	// is random past the timestamp — so a test asserting "the later one wins" has to make one
	// actually later.
	tick()
	second, err := s.RecordReview(ctx, "counsel@acme.example", "m", "1.1.0", core.ReviewInput{
		EdgeID: edgeID, Outcome: domain.OutcomeSubstantial, Note: "provider duties transfer",
	})
	if err != nil {
		t.Fatal(err)
	}

	all, err := s.ListVersionReviews(ctx, "m", "1.1.0")
	if err != nil || len(all) != 2 {
		t.Fatalf("both rows must survive: %v len=%d", err, len(all))
	}
	if all[0].ID != second.ID || all[1].ID != first.ID {
		t.Fatalf("newest first: %q then %q", all[0].ID, all[1].ID)
	}
	if all[1].Outcome != domain.OutcomeUndetermined {
		t.Fatalf("the superseded row must be unchanged: %+v", all[1])
	}
	closed := queue(t, s, domain.ReviewClosed)
	if len(closed) != 1 || closed[0].Review.ID != second.ID {
		t.Fatalf("the queue must read the latest: %+v", closed)
	}
}

// §17.6.3: the edge must be a derived_from edge on this version.
func TestRecordReviewRejectsBadEdge(t *testing.T) {
	s, ctx, _ := reviewFixture(t)

	// A trained_on edge is a real edge on this version, and still not reviewable: recording an
	// Art. 25 judgement against it would answer a question Art. 25 does not ask (§17.8).
	other := core.LineageInput{Relation: domain.RelTrainedOn}
	other.To.URI = "s3://data/train.parquet"
	e, err := s.AddLineage(ctx, "ml@acme.example", "m", "1.1.0", other)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, edgeID string }{
		{"wrong relation", e.ID},
		{"unknown edge", domain.NewID()},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.RecordReview(ctx, "counsel@acme.example", "m", "1.1.0", core.ReviewInput{
				EdgeID: tc.edgeID, Outcome: domain.OutcomeNotSubstantial,
			})
			de, ok := err.(*domain.Error)
			if !ok || de.Code != domain.CodeInvalidArgument {
				t.Fatalf("expected invalid_argument, got %v", err)
			}
			if de.Details["field"] != "edgeId" {
				t.Fatalf("details.field = %v", de.Details["field"])
			}
		})
	}
}

// An edge belonging to a *different* version must not be reviewable through this one.
func TestRecordReviewRejectsAnotherVersionsEdge(t *testing.T) {
	s, ctx, edgeID := reviewFixture(t)
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.RecordReview(ctx, "counsel@acme.example", "m", "2.0.0", core.ReviewInput{
		EdgeID: edgeID, Outcome: domain.OutcomeNotSubstantial,
	})
	if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %v", err)
	}
}

func TestRecordReviewRejectsUnknownOutcome(t *testing.T) {
	s, ctx, edgeID := reviewFixture(t)
	_, err := s.RecordReview(ctx, "counsel@acme.example", "m", "1.1.0", core.ReviewInput{
		EdgeID: edgeID, Outcome: "pending",
	})
	de, ok := err.(*domain.Error)
	if !ok || de.Code != domain.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %v", err)
	}
	if _, ok := de.Details["allowedValues"]; !ok {
		t.Fatalf("details must name the allowed values: %+v", de.Details)
	}
}

// `undetermined` is a real outcome: "looked at it, needs counsel" must be distinguishable
// from "nobody opened it" (§17.5.1). It closes the item like any other.
func TestUndeterminedClosesTheItem(t *testing.T) {
	s, ctx, edgeID := reviewFixture(t)
	if _, err := s.RecordReview(ctx, "ml@acme.example", "m", "1.1.0", core.ReviewInput{
		EdgeID: edgeID, Outcome: domain.OutcomeUndetermined, Note: "with counsel",
	}); err != nil {
		t.Fatal(err)
	}
	if got := queue(t, s, domain.ReviewOpen); len(got) != 0 {
		t.Fatal("undetermined must not leave the item open — that is what it is for")
	}
	closed := queue(t, s, domain.ReviewClosed)
	if len(closed) != 1 || closed[0].Review.Outcome != domain.OutcomeUndetermined {
		t.Fatalf("closed: %+v", closed)
	}
}

// A derived_from edge pointing at an external ref is the Art. 25 case — somebody fine-tuned a
// third-party model. No hashes reach, so it queues as `unknown` and names no parent.
func TestQueueCoversExternalDerivations(t *testing.T) {
	s, ctx, _ := reviewFixture(t)
	in := core.LineageInput{Relation: domain.RelDerivedFrom, Properties: json.RawMessage(`{"method":"lora"}`)}
	in.To.URI = "hf://acme/base-7b"
	e, err := s.AddLineage(ctx, "ml@acme.example", "m", "1.1.0", in)
	if err != nil {
		t.Fatal(err)
	}

	var found *domain.ReviewItem
	for _, it := range queue(t, s, domain.ReviewOpen) {
		if it.EdgeID == e.ID {
			found = it
		}
	}
	if found == nil {
		t.Fatal("an external derivation must queue")
	}
	if found.DerivedFrom != nil || found.DerivedFromRef != "hf://acme/base-7b" {
		t.Fatalf("external ref: from=%+v ref=%q", found.DerivedFrom, found.DerivedFromRef)
	}
	if found.DeclaredMethod != "lora" {
		t.Fatalf("declaredMethod = %q", found.DeclaredMethod)
	}
	// And it is reviewable like any other.
	if _, err := s.RecordReview(ctx, "counsel@acme.example", "m", "1.1.0", core.ReviewInput{
		EdgeID: e.ID, Outcome: domain.OutcomeSubstantial,
	}); err != nil {
		t.Fatalf("RecordReview on an external derivation: %v", err)
	}
}

// Drift clause 4 (§16.5): an open item opened *after* the classification makes it stale — and
// closing the item makes it current again, because the reason it fired has gone away.
func TestOpenReviewMakesClassificationStale(t *testing.T) {
	s, ctx, _ := reviewFixture(t)

	// The fixture classifies *after* creating the edge, so nothing has happened since.
	v, err := s.GetClassification(ctx, "m", eu)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != domain.ClassificationCurrent {
		t.Fatalf("a derivation predating the classification is not drift: %q %v", v.State, v.StaleReasons)
	}

	// A second derivation, after the fact.
	tick()
	in := core.LineageInput{Relation: domain.RelDerivedFrom}
	in.To.URI = "hf://acme/base-7b"
	e2, err := s.AddLineage(ctx, "ml@acme.example", "m", "1.1.0", in)
	if err != nil {
		t.Fatal(err)
	}

	v, err = s.GetClassification(ctx, "m", eu)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != domain.ClassificationStale || !hasReason(v.StaleReasons, domain.StaleDerivationSince) {
		t.Fatalf("state = %q reasons = %v, want stale with derivation_since", v.State, v.StaleReasons)
	}

	// Reviewing the new item removes the reason. The older edge is still there and still
	// unreviewed, but it predates the classification, so it never contributed.
	if _, err := s.RecordReview(ctx, "counsel@acme.example", "m", "1.1.0", core.ReviewInput{
		EdgeID: e2.ID, Outcome: domain.OutcomeNotSubstantial,
	}); err != nil {
		t.Fatal(err)
	}
	v, err = s.GetClassification(ctx, "m", eu)
	if err != nil {
		t.Fatal(err)
	}
	if hasReason(v.StaleReasons, domain.StaleDerivationSince) {
		t.Fatalf("closing the item must clear clause 4: %v", v.StaleReasons)
	}
}

// The inventory read runs the same clause, in one batch rather than per row (§16.8.2).
func TestInventoryReportsDerivationDrift(t *testing.T) {
	s, ctx, _ := reviewFixture(t)
	tick()
	in := core.LineageInput{Relation: domain.RelDerivedFrom}
	in.To.URI = "hf://acme/base-7b"
	if _, err := s.AddLineage(ctx, "ml@acme.example", "m", "1.1.0", in); err != nil {
		t.Fatal(err)
	}

	items, _, err := s.ListInventory(ctx, domain.ListOptions{}, domain.ClassificationFilter{Regime: eu}, domain.ClassificationStale)
	if err != nil {
		t.Fatalf("ListInventory: %v", err)
	}
	if len(items) != 1 || items[0].Classification == nil {
		t.Fatalf("stale filter should return the model: %+v", items)
	}
	if !hasReason(items[0].Classification.StaleReasons, domain.StaleDerivationSince) {
		t.Fatalf("reasons = %v", items[0].Classification.StaleReasons)
	}
}

func hasReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}
