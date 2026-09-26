package core_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

const mrm = domain.RegimeMRM

func tier1() core.ClassificationInput {
	return core.ClassificationInput{MRMTier: domain.MRMTier1, Basis: "Drives automated card-not-present declines above $500."}
}

// mrmFixture is model "m" with version 1.0.0 by dev@acme.example, promoted to production.
func mrmFixture(t *testing.T) (*core.Service, context.Context) {
	t.Helper()
	s, ctx := classifiable(t)
	if _, _, err := s.PublishVersion(ctx, "dev@acme.example", "m", core.PublishVersionInput{Name: "1.0.0", Author: "dev@acme.example"}); err != nil {
		t.Fatal(err)
	}
	promote(t, s, ctx, "1.0.0")
	return s, ctx
}

func promote(t *testing.T, s *core.Service, ctx context.Context, version string) {
	t.Helper()
	for _, to := range []domain.Stage{domain.StageStaging, domain.StageProduction} {
		if _, err := s.Transition(ctx, "release@acme.example", "m", version, to, ""); err != nil {
			t.Fatalf("Transition %s → %s: %v", version, to, err)
		}
	}
}

func evaluate(t *testing.T, s *core.Service, ctx context.Context, version string) {
	t.Helper()
	yes := true
	if _, err := s.AddEvaluation(ctx, "ci", "m", version, core.EvaluationInput{Suite: "holdout", Metric: "auc", Value: 0.9, HigherIsBetter: &yes}); err != nil {
		t.Fatal(err)
	}
}

func validate(t *testing.T, s *core.Service, ctx context.Context, actor, version string, in core.ValidationInput) *domain.ValidationView {
	t.Helper()
	v, err := s.RecordValidation(ctx, actor, "m", version, in)
	if err != nil {
		t.Fatalf("RecordValidation: %v", err)
	}
	return v
}

func mrmInventory(t *testing.T, s *core.Service, ctx context.Context, q core.MRMInventory) []*domain.ModelInventoryItem {
	t.Helper()
	items, _, err := s.Inventory(ctx, domain.ListOptions{}, core.InventoryQuery{MRM: &q})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	return items
}

// The M17 acceptance: a tier-1 model whose production version has had no evaluation since it
// was promoted comes back from mrmTier=tier_1&mrmState=stale, with that reason.
func TestUnmonitoredProductionModelIsListedStale(t *testing.T) {
	s, ctx := mrmFixture(t)
	if _, err := s.SetClassification(ctx, "mrm@acme.example", "m", mrm, tier1()); err != nil {
		t.Fatal(err)
	}
	validate(t, s, ctx, "mrm@acme.example", "1.0.0", core.ValidationInput{Outcome: domain.ValidationApproved})

	items := mrmInventory(t, s, ctx, core.MRMInventory{Tier: domain.MRMTier1, State: domain.ClassificationStale})
	if len(items) != 1 || items[0].MRM == nil {
		t.Fatalf("want the model, got %d items", len(items))
	}
	got := items[0].MRM
	if !slices.Equal(got.StaleReasons, []string{domain.StaleUnmonitoredInProduction}) {
		t.Fatalf("staleReasons = %v", got.StaleReasons)
	}
	if got.Version != "1.0.0" || got.LatestValidation == nil || !got.LatestValidation.IndependenceEvidenced {
		t.Fatalf("view: version=%q latest=%+v", got.Version, got.LatestValidation)
	}
	// It is not current, and the EU lens was never asked for.
	if cur := mrmInventory(t, s, ctx, core.MRMInventory{State: domain.ClassificationCurrent}); len(cur) != 0 {
		t.Fatalf("current returned %d", len(cur))
	}
	if items[0].Classification != nil {
		t.Fatal("the EU lens rode along uninvited")
	}

	// Measuring it clears the clause — nothing else does.
	tick()
	evaluate(t, s, ctx, "1.0.0")
	if cur := mrmInventory(t, s, ctx, core.MRMInventory{State: domain.ClassificationCurrent}); len(cur) != 1 {
		t.Fatalf("after an evaluation: current returned %d", len(cur))
	}

	// A new promotion restarts the clock: an evaluation of the *previous* production period
	// does not count as monitoring this one.
	tick()
	if _, _, err := s.PublishVersion(ctx, "dev@acme.example", "m", core.PublishVersionInput{Name: "2.0.0", Author: "dev@acme.example"}); err != nil {
		t.Fatal(err)
	}
	promote(t, s, ctx, "2.0.0")
	got2, err := s.GetClassification(ctx, "m", mrm)
	if err != nil {
		t.Fatal(err)
	}
	// 2.0.0 is now the subject, and it has never been validated.
	if got2.Version != "2.0.0" || got2.State != domain.MRMStateUnvalidated {
		t.Fatalf("after promoting 2.0.0: version=%q state=%s", got2.Version, got2.State)
	}
}

// Regime isolation, deferred from M14 (§16.3.2): writing the `mrm` row leaves the EU row's
// classified_at, and so its staleness, exactly where it was.
func TestMRMWriteLeavesEUStalenessAlone(t *testing.T) {
	s, ctx := classifiable(t)
	euRow, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, highRisk())
	if err != nil {
		t.Fatal(err)
	}
	tick()
	if _, _, err := s.PublishVersion(ctx, "dev", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetClassification(ctx, "m", eu)
	if before.State != domain.ClassificationStale {
		t.Fatalf("EU state before the mrm write = %s, want stale", before.State)
	}

	// The June write: a later date, a different team, a different regime.
	tick()
	if _, err := s.SetClassification(ctx, "mrm@acme.example", "m", mrm, tier1()); err != nil {
		t.Fatal(err)
	}

	after, err := s.GetClassification(ctx, "m", eu)
	if err != nil {
		t.Fatal(err)
	}
	if after.ClassifiedAt != euRow.ClassifiedAt || after.ClassifiedBy != "risk@acme.example" {
		t.Fatalf("the mrm write moved the EU anchor: %d → %d", euRow.ClassifiedAt, after.ClassifiedAt)
	}
	if after.State != domain.ClassificationStale || !slices.Equal(after.StaleReasons, before.StaleReasons) {
		t.Fatalf("the mrm write cleared EU staleness: %s %v", after.State, after.StaleReasons)
	}

	// Each row's state comes from its own predicate.
	list, err := s.ListClassifications(ctx, "m")
	if err != nil || len(list) != 2 {
		t.Fatalf("ListClassifications: %v len=%d", err, len(list))
	}
	if list[0].Regime != eu || list[0].State != domain.ClassificationStale {
		t.Fatalf("EU row: %+v", list[0])
	}
	if list[1].Regime != mrm || list[1].State != domain.MRMStateUnvalidated || list[1].MRMTier != domain.MRMTier1 {
		t.Fatalf("mrm row: %+v", list[1])
	}
}

func TestSetMRMClassificationValidation(t *testing.T) {
	s, ctx := classifiable(t)
	for _, tc := range []struct {
		name string
		in   core.ClassificationInput
		code string
	}{
		{"tier_1 without basis", core.ClassificationInput{MRMTier: domain.MRMTier1}, domain.CodeUnprocessable},
		{"out_of_scope without basis", core.ClassificationInput{MRMTier: domain.MRMOutOfScope}, domain.CodeUnprocessable},
		{"unknown tier", core.ClassificationInput{MRMTier: "tier_0"}, domain.CodeInvalidArgument},
		{"EU class on the mrm path", core.ClassificationInput{MRMTier: domain.MRMTier2, EUSystemRiskClass: domain.EUClassMinimal}, domain.CodeInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.SetClassification(ctx, "mrm", "m", mrm, tc.in)
			if de, ok := err.(*domain.Error); !ok || de.Code != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}
	// And an mrm tier on the EU path.
	in := highRisk()
	in.MRMTier = domain.MRMTier1
	if _, err := s.SetClassification(ctx, "risk", "m", eu, in); err == nil {
		t.Fatal("an mrmTier on the eu_ai_act path was accepted")
	}

	// An omitted tier is `untiered`, a visible answer — never a low tier.
	v, err := s.SetClassification(ctx, "mrm", "m", mrm, core.ClassificationInput{})
	if err != nil {
		t.Fatal(err)
	}
	if v.MRMTier != domain.MRMUntiered || v.State != domain.MRMStateUntiered {
		t.Fatalf("omitted tier: %q %s", v.MRMTier, v.State)
	}
}

func TestRecordValidationRules(t *testing.T) {
	s, ctx := mrmFixture(t)
	past := domain.NowMillis() - 1
	for _, tc := range []struct {
		name  string
		in    core.ValidationInput
		code  string
		field string
	}{
		{"unknown outcome", core.ValidationInput{Outcome: "fine"}, domain.CodeInvalidArgument, "outcome"},
		{"conditional without conditions", core.ValidationInput{Outcome: domain.ValidationConditional}, domain.CodeUnprocessable, "conditions"},
		{"conditional with blank conditions", core.ValidationInput{Outcome: domain.ValidationConditional, Conditions: "  "}, domain.CodeUnprocessable, "conditions"},
		{"validUntil in the past", core.ValidationInput{Outcome: domain.ValidationApproved, ValidUntil: &past}, domain.CodeInvalidArgument, "validUntil"},
		{"evidence that is not on the version", core.ValidationInput{Outcome: domain.ValidationApproved, EvidenceArtifactID: "01NOPE"}, domain.CodeInvalidArgument, "evidenceArtifactId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.RecordValidation(ctx, "mrm", "m", "1.0.0", tc.in)
			de, ok := err.(*domain.Error)
			if !ok || de.Code != tc.code || de.Details["field"] != tc.field {
				t.Fatalf("want %s on %s, got %v", tc.code, tc.field, err)
			}
		})
	}
	if list, _ := s.ListValidations(ctx, "m", "1.0.0"); len(list.Items) != 0 {
		t.Fatalf("rejected writes left %d rows", len(list.Items))
	}
}

func TestValidationEvidenceMustBeAReport(t *testing.T) {
	s, ctx := classifiable(t)
	_, arts, err := s.PublishVersion(ctx, "dev", "m", core.PublishVersionInput{Name: "1.0.0", Artifacts: []core.ArtifactInput{
		{Name: "weights", URI: "s3://b/w"},
		{Name: "report.pdf", Kind: domain.KindDoc, URI: "s3://b/r"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordValidation(ctx, "mrm", "m", "1.0.0", core.ValidationInput{Outcome: domain.ValidationApproved, EvidenceArtifactID: arts[0].ID}); err == nil {
		t.Fatal("a MODEL artifact was accepted as validation evidence")
	}
	v := validate(t, s, ctx, "mrm", "1.0.0", core.ValidationInput{Outcome: domain.ValidationApproved, EvidenceArtifactID: arts[1].ID})
	if v.EvidenceArtifactID != arts[1].ID {
		t.Fatalf("evidence = %q", v.EvidenceArtifactID)
	}
}

// validatedBy and validatedAt are server-set; the input has nowhere to put either.
func TestValidationIsServerAttributedAndAudited(t *testing.T) {
	s, ctx := mrmFixture(t)
	before := domain.NowMillis()
	v := validate(t, s, ctx, "mrm@acme.example", "1.0.0", core.ValidationInput{Outcome: domain.ValidationApproved})
	if v.ValidatedBy != "mrm@acme.example" || v.ValidatedAt < before || v.Source != domain.SourceDeclared {
		t.Fatalf("attribution: %+v", v)
	}

	ver, _ := s.GetVersion(ctx, "m", "1.0.0")
	events, _, err := s.ListAudit(ctx, "model_version", ver.ID, domain.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.Action == "validation.record" && e.Actor == "mrm@acme.example" {
			var d map[string]any
			_ = json.Unmarshal(e.Data, &d)
			found = d["outcome"] == "approved" && d["validationId"] == v.ID
		}
	}
	if !found {
		t.Fatal("no validation.record audit event with structured data")
	}
}

// §20.6: a self-validation is flagged, never refused.
func TestSelfValidationIsFlaggedNotRefused(t *testing.T) {
	s, ctx := mrmFixture(t)
	v, err := s.RecordValidation(ctx, "dev@acme.example", "m", "1.0.0", core.ValidationInput{Outcome: domain.ValidationApproved})
	if err != nil {
		t.Fatalf("a self-validation was refused: %v", err)
	}
	if v.IndependenceEvidenced {
		t.Fatal("validator == author, but independence was evidenced")
	}
	other := validate(t, s, ctx, "mrm@acme.example", "1.0.0", core.ValidationInput{Outcome: domain.ValidationApproved})
	if !other.IndependenceEvidenced {
		t.Fatal("an independent validator was not evidenced")
	}
}

// Append-only: the latest row is the answer, and the superseded one is still there.
func TestLatestValidationPerVersionWins(t *testing.T) {
	s, ctx := mrmFixture(t)
	if _, err := s.SetClassification(ctx, "mrm", "m", mrm, tier1()); err != nil {
		t.Fatal(err)
	}
	evaluate(t, s, ctx, "1.0.0")
	tick()
	first := validate(t, s, ctx, "mrm", "1.0.0", core.ValidationInput{Outcome: domain.ValidationApproved})
	tick()
	validate(t, s, ctx, "mrm", "1.0.0", core.ValidationInput{Outcome: domain.ValidationRejected, Findings: "PSI out of bounds"})

	list, err := s.ListValidations(ctx, "m", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || list.Items[0].Outcome != domain.ValidationRejected || list.Items[1].ID != first.ID {
		t.Fatalf("history: %+v", list.Items)
	}
	if list.State != domain.MRMStateUnvalidated {
		t.Fatalf("latest is rejected, state = %s", list.State)
	}
	got, _ := s.GetClassification(ctx, "m", mrm)
	if got.State != domain.MRMStateUnvalidated || got.LatestValidation.Outcome != domain.ValidationRejected {
		t.Fatalf("model view: %s %+v", got.State, got.LatestValidation)
	}
}

func TestClearingConditions(t *testing.T) {
	s, ctx := mrmFixture(t)
	if _, err := s.SetClassification(ctx, "mrm", "m", mrm, tier1()); err != nil {
		t.Fatal(err)
	}
	tick()
	evaluate(t, s, ctx, "1.0.0")
	cond := validate(t, s, ctx, "mrm", "1.0.0", core.ValidationInput{
		Outcome: domain.ValidationConditional, Conditions: "Re-measure PSI monthly."})

	got, _ := s.GetClassification(ctx, "m", mrm)
	if !slices.Equal(got.StaleReasons, []string{domain.StaleConditionsOutstanding}) {
		t.Fatalf("before clearing: %s %v", got.State, got.StaleReasons)
	}

	cleared, err := s.ClearValidationConditions(ctx, "mrm", "m", "1.0.0", cond.ID)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.ConditionsClearedAt == nil {
		t.Fatal("conditionsClearedAt not set")
	}
	if got, _ := s.GetClassification(ctx, "m", mrm); got.State != domain.ClassificationCurrent {
		t.Fatalf("after clearing: %s %v", got.State, got.StaleReasons)
	}

	precondition := func(err error, reason string) {
		t.Helper()
		de, ok := err.(*domain.Error)
		if !ok || de.Code != domain.CodeFailedPrecondition || de.Details["reason"] != reason {
			t.Fatalf("want 409 %s, got %v", reason, err)
		}
	}
	_, err = s.ClearValidationConditions(ctx, "mrm", "m", "1.0.0", cond.ID)
	precondition(err, "already_cleared")

	approved := validate(t, s, ctx, "mrm", "1.0.0", core.ValidationInput{Outcome: domain.ValidationApproved})
	_, err = s.ClearValidationConditions(ctx, "mrm", "m", "1.0.0", approved.ID)
	precondition(err, "not_conditional")

	if _, err := s.ClearValidationConditions(ctx, "mrm", "m", "1.0.0", "01NOPE"); !domain.IsNotFound(err) {
		t.Fatalf("unknown id: %v", err)
	}
}

// The validity window and a newer publish each stale a validation on their own.
func TestValidationGoesStaleOnPublish(t *testing.T) {
	s, ctx := mrmFixture(t)
	if _, err := s.SetClassification(ctx, "mrm", "m", mrm, tier1()); err != nil {
		t.Fatal(err)
	}
	tick()
	evaluate(t, s, ctx, "1.0.0")
	validate(t, s, ctx, "mrm", "1.0.0", core.ValidationInput{Outcome: domain.ValidationApproved})
	tick()
	if _, _, err := s.PublishVersion(ctx, "dev", "m", core.PublishVersionInput{Name: "1.1.0"}); err != nil {
		t.Fatal(err)
	}
	// 1.0.0 is still production, so still the subject.
	got, _ := s.GetClassification(ctx, "m", mrm)
	if got.Version != "1.0.0" || !slices.Equal(got.StaleReasons, []string{domain.StaleVersionPublishedSince}) {
		t.Fatalf("after publish: version=%s %s %v", got.Version, got.State, got.StaleReasons)
	}
}

// Nothing here gates anything: a stale, unvalidated tier-1 model can still be promoted.
func TestMRMStateBlocksNothing(t *testing.T) {
	s, ctx := classifiable(t)
	if _, err := s.SetClassification(ctx, "mrm", "m", mrm, tier1()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "dev", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	promote(t, s, ctx, "1.0.0")
}

func TestInventoryWithBothLenses(t *testing.T) {
	s, ctx := classifiable(t)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetClassification(ctx, "risk", "m", eu, highRisk()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetClassification(ctx, "mrm", "m", mrm, tier1()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetClassification(ctx, "risk", "other", eu, highRisk()); err != nil {
		t.Fatal(err)
	}

	items, _, err := s.Inventory(ctx, domain.ListOptions{}, core.InventoryQuery{
		EU: &core.EUInventory{}, MRM: &core.MRMInventory{Tier: domain.MRMTier1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "m" || items[0].Classification == nil || items[0].MRM == nil {
		t.Fatalf("both lenses: %d items", len(items))
	}
	if items[0].Classification.Regime != eu || items[0].MRM.Regime != mrm {
		t.Fatalf("lenses crossed: %s / %s", items[0].Classification.Regime, items[0].MRM.Regime)
	}

	// Untiered is filterable and carries no row.
	un := mrmInventory(t, s, ctx, core.MRMInventory{State: domain.MRMStateUntiered})
	if len(un) != 1 || un[0].Name != "other" || un[0].MRM != nil {
		t.Fatalf("untiered: %d", len(un))
	}

	if _, _, err := s.Inventory(ctx, domain.ListOptions{}, core.InventoryQuery{MRM: &core.MRMInventory{State: "unclassified"}}); err == nil {
		t.Fatal("an EU state was accepted on the mrm lens")
	}
}
