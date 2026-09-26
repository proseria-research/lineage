package core_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

const eu = domain.RegimeEUAIAct

// tick advances the wall clock past one epoch-millisecond.
//
// §16.5 compares strictly: a version created at exactly ClassifiedAt did not happen *since*
// the classification. That is the right rule — without it, classifying a model in the same
// millisecond as a publish would report it stale the instant it was classified — but it does
// mean a test has to let the clock move before the two events can be told apart.
func tick() { time.Sleep(2 * time.Millisecond) }

// highRisk is a valid write: high risk, so it needs both a purpose and a basis (§16.6).
func highRisk() core.ClassificationInput {
	return core.ClassificationInput{
		EUSystemRiskClass: domain.EUClassHighAnnexIII,
		EUGpaiTier:        domain.EUGpaiNone,
		IntendedPurpose:   "Scores card-not-present transactions for manual review.",
		Basis:             "Annex III §5(b) — creditworthiness adjacent.",
	}
}

// classifiable returns a service with model "m" and no versions.
func classifiable(t *testing.T) (*core.Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	s := newSvc(t)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

func TestSetAndGetClassification(t *testing.T) {
	s, ctx := classifiable(t)

	v, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, highRisk())
	if err != nil {
		t.Fatalf("SetClassification: %v", err)
	}
	if v.EUSystemRiskClass != domain.EUClassHighAnnexIII {
		t.Fatalf("class = %q", v.EUSystemRiskClass)
	}
	// A model with no versions and no review date has nothing that could have changed.
	if v.State != domain.ClassificationCurrent {
		t.Fatalf("state = %q reasons = %v, want current", v.State, v.StaleReasons)
	}

	got, err := s.GetClassification(ctx, "m", eu)
	if err != nil {
		t.Fatalf("GetClassification: %v", err)
	}
	if got.Basis != highRisk().Basis || got.State != domain.ClassificationCurrent {
		t.Fatalf("round-trip: %+v", got)
	}
}

// §16.6: the server sets classifiedAt and classifiedBy. ClassificationInput has no field for
// either, so this asserts the values actually recorded rather than that a strip happened.
func TestClassifiedByAndAtAreServerSet(t *testing.T) {
	s, ctx := classifiable(t)
	before := domain.NowMillis()

	v, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, highRisk())
	if err != nil {
		t.Fatal(err)
	}
	if v.ClassifiedBy != "risk@acme.example" {
		t.Fatalf("classifiedBy = %q, want the actor", v.ClassifiedBy)
	}
	if v.ClassifiedAt < before {
		t.Fatalf("classifiedAt = %d, want >= %d", v.ClassifiedAt, before)
	}
}

// The acceptance criterion for M14: classify, publish, and the model reads stale on the very
// next request — with no background job having run.
func TestPublishingAVersionMakesTheClassificationStaleAtReadTime(t *testing.T) {
	s, ctx := classifiable(t)

	if _, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, highRisk()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetClassification(ctx, "m", eu); got.State != domain.ClassificationCurrent {
		t.Fatalf("state before publish = %q, want current", got.State)
	}

	tick()
	if _, _, err := s.PublishVersion(ctx, "eng", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetClassification(ctx, "m", eu)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.ClassificationStale {
		t.Fatalf("state after publish = %q, want stale", got.State)
	}
	if len(got.StaleReasons) != 1 || got.StaleReasons[0] != domain.StaleVersionPublishedSince {
		t.Fatalf("staleReasons = %v, want [%s]", got.StaleReasons, domain.StaleVersionPublishedSince)
	}
}

// Re-classifying moves the anchor forward, so the drift it was reporting clears — the only
// way staleness is ever resolved (§16.9: no "mark as current" that does not write a real
// classification).
func TestReclassifyingClearsStaleness(t *testing.T) {
	s, ctx := classifiable(t)
	if _, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, highRisk()); err != nil {
		t.Fatal(err)
	}
	tick()
	if _, _, err := s.PublishVersion(ctx, "eng", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetClassification(ctx, "m", eu); got.State != domain.ClassificationStale {
		t.Fatalf("expected stale before re-classification, got %q", got.State)
	}

	tick()
	v, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, highRisk())
	if err != nil {
		t.Fatal(err)
	}
	if v.State != domain.ClassificationCurrent {
		t.Fatalf("state after re-classification = %q reasons = %v, want current", v.State, v.StaleReasons)
	}
}

// §16.8: PUT replaces whole. A second write that omits the basis must not leave the first
// one sitting underneath the new class.
func TestSetReplacesTheWholeRow(t *testing.T) {
	s, ctx := classifiable(t)
	if _, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, highRisk()); err != nil {
		t.Fatal(err)
	}

	v, err := s.SetClassification(ctx, "counsel@acme.example", "m", eu, core.ClassificationInput{
		EUSystemRiskClass: domain.EUClassLimited,
		EUGpaiTier:        domain.EUGpaiNone,
		IntendedPurpose:   "Reclassified after counsel review.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Basis != "" {
		t.Fatalf("basis survived a replace: %q", v.Basis)
	}
	if v.ClassifiedBy != "counsel@acme.example" {
		t.Fatalf("classifiedBy = %q", v.ClassifiedBy)
	}
}

func TestSetClassificationValidation(t *testing.T) {
	tests := []struct {
		name string
		in   core.ClassificationInput
		code string
	}{{
		name: "unknown class",
		in:   core.ClassificationInput{EUSystemRiskClass: "high", EUGpaiTier: domain.EUGpaiNone},
		code: domain.CodeInvalidArgument,
	}, {
		name: "a stated class needs a purpose",
		in:   core.ClassificationInput{EUSystemRiskClass: domain.EUClassMinimal, EUGpaiTier: domain.EUGpaiNone},
		code: domain.CodeUnprocessable,
	}, {
		name: "high risk needs a basis",
		in: core.ClassificationInput{
			EUSystemRiskClass: domain.EUClassHighAnnexI, EUGpaiTier: domain.EUGpaiNone,
			IntendedPurpose: "p",
		},
		code: domain.CodeUnprocessable,
	}, {
		name: "a review date in the past",
		in: func() core.ClassificationInput {
			in := highRisk()
			past := domain.NowMillis() - 1000
			in.ReviewDueAt = &past
			return in
		}(),
		code: domain.CodeInvalidArgument,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := classifiable(t)
			_, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, tc.in)
			de, ok := err.(*domain.Error)
			if !ok {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
			if de.Code != tc.code {
				t.Fatalf("code = %s (%s), want %s", de.Code, de.Message, tc.code)
			}
			// A rejected write leaves the model unclassified, not half-written.
			if _, err := s.GetClassification(ctx, "m", eu); err == nil {
				t.Fatal("a rejected classification was persisted")
			}
		})
	}
}

// Omitting both enums is a legal write meaning "nobody has said yet" — and it must read back
// as `unclassified`, never as `minimal` (§16.3).
func TestOmittedEnumsDefaultToUnclassified(t *testing.T) {
	s, ctx := classifiable(t)
	v, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, core.ClassificationInput{})
	if err != nil {
		t.Fatalf("an empty classification should be accepted: %v", err)
	}
	if v.EUSystemRiskClass != domain.EUClassUnclassified || v.EUGpaiTier != domain.EUGpaiNone {
		t.Fatalf("defaults: class=%q tier=%q", v.EUSystemRiskClass, v.EUGpaiTier)
	}
	if v.State != domain.ClassificationUnclassified {
		t.Fatalf("state = %q, want unclassified", v.State)
	}
}

// §16.8: the write is audited, with the regime in structured data rather than only in prose.
func TestSetClassificationIsAudited(t *testing.T) {
	s, ctx := classifiable(t)
	if _, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, highRisk()); err != nil {
		t.Fatal(err)
	}

	events, _, err := s.ListAudit(ctx, "", "", domain.ListOptions{PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	var found *domain.AuditEvent
	for _, e := range events {
		if e.Action == "classification.set" {
			found = e
			break
		}
	}
	if found == nil {
		t.Fatal("no classification.set audit event")
	}
	if found.Actor != "risk@acme.example" || found.SubjectType != "model" {
		t.Fatalf("audit event: %+v", found)
	}
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["regime"] != string(eu) {
		t.Fatalf("audit data lacks the regime: %v", data)
	}
	if data["euSystemRiskClass"] != string(domain.EUClassHighAnnexIII) {
		t.Fatalf("audit data class = %v", data["euSystemRiskClass"])
	}
}

func TestGetClassificationNotFoundWhenNeverClassified(t *testing.T) {
	s, ctx := classifiable(t)
	_, err := s.GetClassification(ctx, "m", eu)
	de, ok := err.(*domain.Error)
	if !ok || de.Code != domain.CodeNotFound {
		t.Fatalf("want not_found, got %v", err)
	}

	list, err := s.ListClassifications(ctx, "m")
	if err != nil || len(list) != 0 {
		t.Fatalf("ListClassifications: %v len=%d", err, len(list))
	}
}

func TestClassificationOnUnknownModel(t *testing.T) {
	s, ctx := classifiable(t)
	if _, err := s.SetClassification(ctx, "risk@acme.example", "nope", eu, highRisk()); err == nil {
		t.Fatal("expected not_found for an unknown model")
	}
	if _, err := s.GetClassification(ctx, "nope", eu); err == nil {
		t.Fatal("expected not_found for an unknown model")
	}
}

// The view serializes flat, with the constant source and the computed state alongside the
// stored fields (§16.8.2) — the embedded entity's MarshalJSON must not hijack the encode.
func TestClassificationViewSerialization(t *testing.T) {
	s, ctx := classifiable(t)
	if _, err := s.SetClassification(ctx, "risk@acme.example", "m", eu, highRisk()); err != nil {
		t.Fatal(err)
	}
	tick()
	if _, _, err := s.PublishVersion(ctx, "eng", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetClassification(ctx, "m", eu)
	if err != nil {
		t.Fatal(err)
	}

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{
		"regime":            string(eu),
		"euSystemRiskClass": string(domain.EUClassHighAnnexIII),
		"source":            string(domain.SourceDeclared),
		"state":             string(domain.ClassificationStale),
	} {
		if got[k] != want {
			t.Fatalf("%s = %v, want %v (full: %s)", k, got[k], want, b)
		}
	}
	reasons, _ := got["staleReasons"].([]any)
	if len(reasons) != 1 || reasons[0] != domain.StaleVersionPublishedSince {
		t.Fatalf("staleReasons = %v", got["staleReasons"])
	}
}

// Clause 3 end to end (§16.5, §00.11.18): it fires when the system in service changes — a
// promotion (files can no longer be added once a version reaches staging, §00.11.19) — and
// not when the production version's description is edited.
func TestClause3FollowsTheSystemInService(t *testing.T) {
	s, ctx := classifiable(t)
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	for _, to := range []domain.Stage{domain.StageStaging, domain.StageProduction} {
		if _, err := s.Transition(ctx, "me", "m", "1.0.0", to, ""); err != nil {
			t.Fatal(err)
		}
	}
	// The successor exists before the classification, so only its promotion is news.
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "2.0.0",
		Artifacts: []core.ArtifactInput{{Name: "weights-v2.bin", URI: "s3://b/w2"}}}); err != nil {
		t.Fatal(err)
	}
	tick()
	if _, err := s.SetClassification(ctx, "risk", "m", eu, highRisk()); err != nil {
		t.Fatal(err)
	}
	state := func() (domain.ClassificationState, []string) {
		t.Helper()
		v, err := s.GetClassification(ctx, "m", eu)
		if err != nil {
			t.Fatal(err)
		}
		return v.State, v.StaleReasons
	}

	tick()
	desc := "typo fixed"
	if _, err := s.PatchVersion(ctx, "me", "m", "1.0.0", core.PatchVersionInput{Description: &desc}); err != nil {
		t.Fatal(err)
	}
	if st, rs := state(); st != domain.ClassificationCurrent {
		t.Fatalf("after a description edit: %q %v, want current", st, rs)
	}

	tick()
	for _, to := range []domain.Stage{domain.StageStaging, domain.StageProduction} {
		if _, err := s.Transition(ctx, "me", "m", "2.0.0", to, ""); err != nil {
			t.Fatal(err)
		}
	}
	if st, rs := state(); st != domain.ClassificationStale || len(rs) != 1 || rs[0] != domain.StaleProductionChanged {
		t.Fatalf("after a new version entered production: %q %v, want stale [%s]", st, rs, domain.StaleProductionChanged)
	}

	// Re-classify, then take the only version out of production. Nothing is in service, so
	// nothing in service changed: clause 3 reads the current production version, and there is none.
	tick()
	if _, err := s.SetClassification(ctx, "risk", "m", eu, highRisk()); err != nil {
		t.Fatal(err)
	}
	if st, _ := state(); st != domain.ClassificationCurrent {
		t.Fatalf("re-classifying did not clear it: %q", st)
	}
	tick()
	if _, err := s.Transition(ctx, "me", "m", "2.0.0", domain.StageArchived, ""); err != nil {
		t.Fatal(err)
	}
	if st, rs := state(); st != domain.ClassificationCurrent {
		t.Fatalf("taking a version out of production with nothing replacing it: %q %v, want current", st, rs)
	}
}
