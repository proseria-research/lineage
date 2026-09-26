package domain

import (
	"slices"
	"testing"
)

// §22.4, one branch per case. ConformanceOf is pure, so each row states the facts it is given
// and the answer those facts force.
func TestConformanceOf(t *testing.T) {
	closed := int64(2_000)
	old := &ChangePlan{ID: "old", AllowedVerdicts: []Verdict{VerdictIdentical}, EffectiveFrom: 1_000, EffectiveTo: &closed}
	cur := &ChangePlan{ID: "cur", AllowedVerdicts: []Verdict{VerdictIdentical, VerdictReweighted},
		AllowedMethods: []string{"retrain", "fine_tune"}, EffectiveFrom: 2_000}
	loose := &ChangePlan{ID: "loose", AllowedVerdicts: []Verdict{VerdictReweighted}, EffectiveFrom: 1_000}
	plans := []*ChangePlan{cur, old}

	for _, tc := range []struct {
		name    string
		plans   []*ChangePlan
		at      int64
		verdict Verdict
		method  string
		want    Conformance
		plan    string
		reasons []string
	}{
		{"no plan", nil, 3_000, VerdictReweighted, "", ConformanceNoPlan, "", nil},
		{"predates every plan", plans, 999, VerdictRearchitected, "", ConformanceUncovered, "", nil},
		{"within", plans, 3_000, VerdictReweighted, "retrain", ConformanceWithinPlan, "cur", nil},
		{"no declared method is not checked", plans, 3_000, VerdictIdentical, "", ConformanceWithinPlan, "cur", nil},
		{"verdict outside", plans, 3_000, VerdictRescaled, "retrain", ConformanceOutsidePlan, "cur", []string{OutsideVerdict}},
		{"method outside", plans, 3_000, VerdictReweighted, "distill", ConformanceOutsidePlan, "cur", []string{OutsideMethod}},
		{"both outside, both reported", plans, 3_000, VerdictRescaled, "distill", ConformanceOutsidePlan, "cur",
			[]string{OutsideVerdict, OutsideMethod}},
		{"unconstrained methods", []*ChangePlan{loose}, 3_000, VerdictReweighted, "distill", ConformanceWithinPlan, "loose", nil},
		// §22.4.1: a missing hash is queued, not passed — even when the plan would allow
		// either candidate.
		{"unknown is undetermined", plans, 3_000, VerdictUnknown, "retrain", ConformanceUndetermined, "cur", nil},
		{"empty verdict is undetermined", plans, 3_000, "", "", ConformanceUndetermined, "cur", nil},
		// The plan in force at publish decides, not the newest one.
		{"judged by the plan then in force", plans, 1_500, VerdictReweighted, "", ConformanceOutsidePlan, "old", []string{OutsideVerdict}},
		{"boundary belongs to the new plan", plans, 2_000, VerdictReweighted, "", ConformanceWithinPlan, "cur", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, p, reasons := ConformanceOf(tc.plans, tc.at, tc.verdict, tc.method)
			if got != tc.want {
				t.Fatalf("conformance = %q, want %q", got, tc.want)
			}
			if (p == nil) != (tc.plan == "") || (p != nil && p.ID != tc.plan) {
				t.Fatalf("plan = %+v, want %q", p, tc.plan)
			}
			if !slices.Equal(reasons, tc.reasons) {
				t.Fatalf("reasons = %v, want %v", reasons, tc.reasons)
			}
		})
	}
}
