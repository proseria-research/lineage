package core_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// Change control plans (§22). Conformance is derived on read, so each test sets facts and
// reads the answer back; none of them writes a verdict anywhere.

// ladder is a full hash ladder, the shape of a producer's §11.4 submission.
func ladder(shape, weights string) map[string]string {
	return map[string]string{"topology": "t", "shape": shape, "dtype": "d", "weights": weights}
}

// planFixture builds model "m" with a 1.0.0 base, a plan allowing identical and reweighted
// via retrain or fine_tune, then 1.1.0 published under it and derived from 1.0.0 by `method`.
func planFixture(t *testing.T, method string) (*core.Service, context.Context, *domain.ChangePlan) {
	t.Helper()
	ctx := context.Background()
	s := newSvc(t)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	from := int64(0)
	p, err := s.DeclareChangePlan(ctx, "ra@acme.example", "m", core.ChangePlanInput{
		Ref: "K243117", Summary: "Periodic retraining. Architecture frozen.",
		AllowedVerdicts: []domain.Verdict{domain.VerdictIdentical, domain.VerdictReweighted},
		AllowedMethods:  []string{"retrain", "fine_tune"},
		EffectiveFrom:   &from,
	})
	if err != nil {
		t.Fatalf("DeclareChangePlan: %v", err)
	}
	derive(t, s, "1.1.0", method)
	return s, ctx, p
}

// derive publishes version and a derived_from edge from 1.0.0.
func derive(t *testing.T, s *core.Service, version, method string) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: version}); err != nil {
		t.Fatalf("publish %s: %v", version, err)
	}
	in := core.LineageInput{Relation: domain.RelDerivedFrom}
	if method != "" {
		in.Properties = json.RawMessage(`{"method":"` + method + `"}`)
	}
	in.To.Version = "1.0.0"
	if _, err := s.AddLineage(ctx, "ml@acme.example", "m", version, in); err != nil {
		t.Fatalf("AddLineage: %v", err)
	}
}

func conformance(t *testing.T, s *core.Service, version string) *domain.ConformanceItem {
	t.Helper()
	items, err := s.VersionConformance(context.Background(), "m", version)
	if err != nil || len(items) != 1 {
		t.Fatalf("VersionConformance(%s): %v %+v", version, err, items)
	}
	return items[0]
}

func planQueue(t *testing.T, s *core.Service, statuses ...domain.Conformance) []*domain.ConformanceItem {
	t.Helper()
	items, _, err := s.ConformanceQueue(context.Background(), statuses, domain.ListOptions{})
	if err != nil {
		t.Fatalf("ConformanceQueue(%v): %v", statuses, err)
	}
	return items
}

func TestConformanceWithinPlan(t *testing.T) {
	s, _, p := planFixture(t, "retrain")
	hashes(t, s, "m", "1.0.0", ladder("sh", "w1"))
	hashes(t, s, "m", "1.1.0", ladder("sh", "w2"))

	it := conformance(t, s, "1.1.0")
	if it.Conformance != domain.ConformanceWithinPlan || it.Verdict != domain.VerdictReweighted {
		t.Fatalf("item: %+v", it)
	}
	if it.Plan == nil || it.Plan.ID != p.ID || it.Plan.Ref != "K243117" || it.DeclaredMethod != "retrain" {
		t.Fatalf("plan and method: %+v", it)
	}
	if it.DerivedFrom == nil || it.DerivedFrom.Version != "1.0.0" || len(it.Basis.FromHashes) != 4 {
		t.Fatalf("derivedFrom and basis: %+v", it)
	}
	if got := planQueue(t, s, domain.ConformanceWithinPlan); len(got) != 1 {
		t.Fatalf("within_plan queue: %+v", got)
	}
	if got := planQueue(t, s, domain.ConformanceOutsidePlan, domain.ConformanceUndetermined); len(got) != 0 {
		t.Fatalf("nothing needs attention: %+v", got)
	}
}

// The M18 acceptance in core: a `rescaled` version under a plan allowing identical and
// reweighted is outside it, with the hashes that decided — and every write that got it there,
// publish included, succeeded.
func TestConformanceOutsidePlanNeverBlocks(t *testing.T) {
	s, ctx, _ := planFixture(t, "distill")
	hashes(t, s, "m", "1.0.0", ladder("sh", "w1"))
	hashes(t, s, "m", "1.1.0", ladder("sh-wider", "w2"))

	got := planQueue(t, s, domain.ConformanceOutsidePlan)
	if len(got) != 1 {
		t.Fatalf("outside_plan queue: %+v", got)
	}
	it := got[0]
	if it.Version != "1.1.0" || it.Verdict != domain.VerdictRescaled {
		t.Fatalf("item: %+v", it)
	}
	// Both reasons: the verdict and the declared method are each outside.
	if len(it.Reasons) != 2 || it.Reasons[0] != domain.OutsideVerdict || it.Reasons[1] != domain.OutsideMethod {
		t.Fatalf("reasons: %v", it.Reasons)
	}
	if c := it.Hashes["shape"].Changed; c == nil || !*c {
		t.Fatalf("shape must read changed: %+v", it.Hashes)
	}
	if len(it.AllowedVerdicts) != 2 {
		t.Fatalf("the envelope rides along: %v", it.AllowedVerdicts)
	}

	// Nothing downstream is gated: publish more, promote the outside version, add lineage.
	for _, to := range []domain.Stage{domain.StageStaging, domain.StageProduction} {
		if _, err := s.Transition(ctx, "ops", "m", "1.1.0", to, ""); err != nil {
			t.Fatalf("promotion must not be blocked by outside_plan: %v", err)
		}
	}
	derive(t, s, "1.2.0", "distill")
	if got := planQueue(t, s); len(got) != 2 {
		t.Fatalf("every row, unfiltered: %+v", got)
	}
}

// §22.4.1: a missing weights_hash leaves identical and reweighted indistinguishable, and both
// are allowed — the version is still queued, not passed.
func TestConformanceUndeterminedIsQueued(t *testing.T) {
	s, _, _ := planFixture(t, "retrain")
	hashes(t, s, "m", "1.0.0", map[string]string{"topology": "t", "shape": "sh", "dtype": "d"})
	hashes(t, s, "m", "1.1.0", ladder("sh", "w2"))

	it := conformance(t, s, "1.1.0")
	if it.Conformance != domain.ConformanceUndetermined || it.Verdict != domain.VerdictUnknown {
		t.Fatalf("item: %+v", it)
	}
	if len(it.Missing) != 1 || it.Missing[0] != "weights" || len(it.Basis.FromHashes) != 3 {
		t.Fatalf("the basis says which side lacked what: %+v", it)
	}
	if got := planQueue(t, s, domain.ConformanceUndetermined); len(got) != 1 {
		t.Fatalf("undetermined is queued: %+v", got)
	}

	// Supplying the hash moves it, because nothing was stored.
	hashes(t, s, "m", "1.0.0", ladder("sh", "w1"))
	if it := conformance(t, s, "1.1.0"); it.Conformance != domain.ConformanceWithinPlan {
		t.Fatalf("after the hash arrives: %+v", it)
	}
}

// §22.4.2: a version published before any plan took effect is uncovered, not outside.
func TestConformanceUncoveredIsNotOutside(t *testing.T) {
	ctx := context.Background()
	s := newSvc(t)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	derive(t, s, "1.1.0", "distill")
	hashes(t, s, "m", "1.0.0", ladder("sh", "w1"))
	hashes(t, s, "m", "1.1.0", ladder("sh-wider", "w2"))

	// No plan at all: the per-version read says so; the queue does not scan the model.
	if it := conformance(t, s, "1.1.0"); it.Conformance != domain.ConformanceNoPlan || it.Plan != nil {
		t.Fatalf("no_plan: %+v", it)
	}
	if got := planQueue(t, s); len(got) != 0 {
		t.Fatalf("an unplanned model is not in the queue: %+v", got)
	}

	future := domain.NowMillis() + 3_600_000
	if _, err := s.DeclareChangePlan(ctx, "ra", "m", core.ChangePlanInput{
		Summary: "from next hour", AllowedVerdicts: []domain.Verdict{domain.VerdictIdentical}, EffectiveFrom: &future,
	}); err != nil {
		t.Fatal(err)
	}
	it := conformance(t, s, "1.1.0")
	if it.Conformance != domain.ConformanceUncovered || it.Plan != nil {
		t.Fatalf("uncovered: %+v", it)
	}
	if got := planQueue(t, s, domain.ConformanceOutsidePlan); len(got) != 0 {
		t.Fatalf("history is not a violation: %+v", got)
	}
	if got := planQueue(t, s, domain.ConformanceUncovered); len(got) != 1 {
		t.Fatalf("uncovered is listable: %+v", got)
	}
}

// Supersession keeps history: a version is judged by the plan in force when it shipped, even
// after that plan is replaced.
func TestSupersessionJudgesByThePlanThenInForce(t *testing.T) {
	s, ctx, first := planFixture(t, "retrain")
	hashes(t, s, "m", "1.0.0", ladder("sh", "w1"))
	hashes(t, s, "m", "1.1.0", ladder("sh-wider", "w2"))

	// Without `supersedes`, a second open plan is an overlap.
	_, err := s.DeclareChangePlan(ctx, "ra", "m", core.ChangePlanInput{
		Summary: "wider", AllowedVerdicts: []domain.Verdict{domain.VerdictRescaled},
	})
	if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeFailedPrecondition || de.Details["reason"] != "plan_overlap" {
		t.Fatalf("overlap: %v", err)
	}

	// A tick either side of the supersession, so 1.1.0 and 1.2.0 cannot share its millisecond:
	// the boundary instant belongs to the new plan, and the test is about the two sides of it.
	time.Sleep(2 * time.Millisecond)
	second, err := s.DeclareChangePlan(ctx, "ra", "m", core.ChangePlanInput{
		Ref: "K250001", Summary: "width may grow", Supersedes: first.ID,
		AllowedVerdicts: []domain.Verdict{domain.VerdictReweighted, domain.VerdictRescaled},
	})
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}

	// 1.1.0 shipped under the first plan and is still judged by it.
	if it := conformance(t, s, "1.1.0"); it.Conformance != domain.ConformanceOutsidePlan || it.Plan.ID != first.ID {
		t.Fatalf("1.1.0 under the first plan: %+v", it)
	}
	// 1.2.0 ships under the second, which allows the same change.
	time.Sleep(2 * time.Millisecond)
	derive(t, s, "1.2.0", "retrain")
	hashes(t, s, "m", "1.2.0", ladder("sh-wider", "w3"))
	if it := conformance(t, s, "1.2.0"); it.Conformance != domain.ConformanceWithinPlan || it.Plan.ID != second.ID {
		t.Fatalf("1.2.0 under the second plan: %+v", it)
	}

	plans, err := s.ListChangePlans(ctx, "m")
	if err != nil || len(plans) != 2 || plans[1].EffectiveTo == nil || *plans[1].EffectiveTo != second.EffectiveFrom {
		t.Fatalf("history: %v %+v", err, plans)
	}

	events, _, err := s.ListAudit(ctx, "", "", domain.ListOptions{PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, e := range events {
		seen[e.Action]++
	}
	if seen["change_plan.declare"] != 2 || seen["change_plan.supersede"] != 1 {
		t.Fatalf("audit: %v", seen)
	}
}

func TestConformanceNeedsAPredecessor(t *testing.T) {
	s, ctx, _ := planFixture(t, "retrain")
	_, err := s.VersionConformance(ctx, "m", "1.0.0")
	if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeFailedPrecondition || de.Details["reason"] != "no_predecessor" {
		t.Fatalf("no_predecessor: %v", err)
	}
}

func TestDeclareChangePlanValidation(t *testing.T) {
	ctx := context.Background()
	s := newSvc(t)
	for _, name := range []string{"m", "other"} {
		if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	_, arts, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0", Artifacts: []core.ArtifactInput{
		{Name: "weights", URI: "s3://b/w"},
		{Name: "pccp.pdf", Kind: domain.KindDoc, URI: "s3://b/pccp"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := s.PublishVersion(ctx, "me", "other", core.PublishVersionInput{Name: "1.0.0", Artifacts: []core.ArtifactInput{
		{Name: "pccp.pdf", Kind: domain.KindDoc, URI: "s3://b/other"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ok := []domain.Verdict{domain.VerdictReweighted}

	for _, tc := range []struct {
		name  string
		in    core.ChangePlanInput
		field string
	}{
		{"no summary", core.ChangePlanInput{AllowedVerdicts: ok}, "summary"},
		{"no verdicts", core.ChangePlanInput{Summary: "s"}, "allowedVerdicts"},
		{"free text verdict", core.ChangePlanInput{Summary: "s", AllowedVerdicts: []domain.Verdict{"minor retraining only"}}, "allowedVerdicts"},
		{"protocol is not a DOC", core.ChangePlanInput{Summary: "s", AllowedVerdicts: ok, ProtocolArtifactID: arts[0].ID}, "protocolArtifactId"},
		{"protocol of another model", core.ChangePlanInput{Summary: "s", AllowedVerdicts: ok, ProtocolArtifactID: other[0].ID}, "protocolArtifactId"},
		{"protocol missing", core.ChangePlanInput{Summary: "s", AllowedVerdicts: ok, ProtocolArtifactID: "01NOPE"}, "protocolArtifactId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.DeclareChangePlan(ctx, "ra", "m", tc.in)
			de, isDE := err.(*domain.Error)
			if !isDE || de.Code != domain.CodeInvalidArgument || de.Details["field"] != tc.field {
				t.Fatalf("err = %v, want invalid_argument on %s", err, tc.field)
			}
		})
	}
	if plans, _ := s.ListChangePlans(ctx, "m"); len(plans) != 0 {
		t.Fatalf("a refused plan must not be stored: %+v", plans)
	}

	p, err := s.DeclareChangePlan(ctx, "ra@acme.example", "m", core.ChangePlanInput{
		Summary: "s", AllowedVerdicts: ok, ProtocolArtifactID: arts[1].ID,
	})
	if err != nil {
		t.Fatalf("a DOC on the model is a valid protocol: %v", err)
	}
	if p.DeclaredBy != "ra@acme.example" || p.DeclaredAt == 0 || p.EffectiveFrom != p.DeclaredAt || p.EffectiveTo != nil {
		t.Fatalf("server-set fields: %+v", p)
	}
	if _, _, err := s.ConformanceQueue(ctx, []domain.Conformance{"violating"}, domain.ListOptions{}); err == nil {
		t.Fatal("an unknown status must be refused")
	}
}
