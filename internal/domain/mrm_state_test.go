package domain

import (
	"slices"
	"testing"
)

// ---- §20.7 ----

const (
	validatedAt = classifiedAt + 10_000
	promotedAt  = classifiedAt + 5_000
)

// validatedFacts is a `current` production version: approved, no expiry, monitored since
// promotion, nothing newer.
func validatedFacts() MRMFacts {
	return MRMFacts{
		Subject: &MRMSubject{VersionID: "v", Version: "1.4.0", Author: "dev@acme.example",
			Stage: StageProduction, StageChangedAt: promotedAt},
		LatestVersionCreatedAt: promotedAt - 1,
		LatestEvaluationRunAt:  promotedAt + 1,
		Validation: &Validation{ID: "val", VersionID: "v", Outcome: ValidationApproved,
			ValidatedBy: "mrm@acme.example", ValidatedAt: validatedAt},
	}
}

func TestMRMStateLadder(t *testing.T) {
	row := mrmRow()
	tests := []struct {
		name  string
		c     *RiskClassification
		facts func(*MRMFacts)
		want  ClassificationState
	}{
		{name: "no mrm row is untiered", c: nil, want: MRMStateUntiered},
		{name: "an explicit untiered is untiered", c: &RiskClassification{Regime: RegimeMRM, MRMTier: MRMUntiered}, want: MRMStateUntiered},
		{
			// Untiered short-circuits: every clause below would fire, and none may be reported.
			name: "untiered wins over every clause",
			c:    nil,
			facts: func(f *MRMFacts) {
				f.LatestVersionCreatedAt = validatedAt + 1
				f.Validation.Outcome = ValidationConditional
			},
			want: MRMStateUntiered,
		},
		{name: "no versions is unvalidated", c: &row, facts: func(f *MRMFacts) { *f = MRMFacts{} }, want: MRMStateUnvalidated},
		{name: "no validation is unvalidated", c: &row, facts: func(f *MRMFacts) { f.Validation = nil }, want: MRMStateUnvalidated},
		{name: "latest rejected is unvalidated", c: &row, facts: func(f *MRMFacts) { f.Validation.Outcome = ValidationRejected }, want: MRMStateUnvalidated},
		{
			// §20.7 as corrected: "cannot conclude yet" is not "fit to rely on".
			name: "latest undetermined is unvalidated", c: &row,
			facts: func(f *MRMFacts) { f.Validation.Outcome = ValidationUndetermined }, want: MRMStateUnvalidated,
		},
		{name: "approved and quiet is current", c: &row, want: ClassificationCurrent},
		{
			name: "out_of_scope runs the same ladder", c: &RiskClassification{Regime: RegimeMRM, MRMTier: MRMOutOfScope, Basis: "b"},
			facts: func(f *MRMFacts) { f.Validation = nil }, want: MRMStateUnvalidated,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := validatedFacts()
			if tc.facts != nil {
				tc.facts(&f)
			}
			got, reasons := MRMStateOf(tc.c, f, testNow)
			if got != tc.want {
				t.Fatalf("state = %s, want %s (reasons %v)", got, tc.want, reasons)
			}
			if got != ClassificationStale && reasons != nil {
				t.Fatalf("a non-stale state carried reasons: %v", reasons)
			}
		})
	}
}

// Each §20.7 clause fires on its own, so a reader can trust the reason it is shown.
func TestEachMRMClauseFiresIndependently(t *testing.T) {
	tests := []struct {
		name  string
		facts func(*MRMFacts)
		want  string
	}{{
		name:  "clause 1 — validation expired",
		facts: func(f *MRMFacts) { f.Validation.ValidUntil = ptr(testNow - 1) },
		want:  StaleValidationExpired,
	}, {
		name:  "clause 2 — a version was published since",
		facts: func(f *MRMFacts) { f.LatestVersionCreatedAt = validatedAt + 1 },
		want:  StaleVersionPublishedSince,
	}, {
		name:  "clause 3 — in production, never evaluated",
		facts: func(f *MRMFacts) { f.LatestEvaluationRunAt = 0 },
		want:  StaleUnmonitoredInProduction,
	}, {
		name:  "clause 3 — in production, last evaluated before promotion",
		facts: func(f *MRMFacts) { f.LatestEvaluationRunAt = promotedAt - 1 },
		want:  StaleUnmonitoredInProduction,
	}, {
		name:  "clause 4 — conditional, conditions outstanding",
		facts: func(f *MRMFacts) { f.Validation.Outcome = ValidationConditional; f.Validation.Conditions = "c" },
		want:  StaleConditionsOutstanding,
	}}
	row := mrmRow()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := validatedFacts()
			tc.facts(&f)
			state, reasons := MRMStateOf(&row, f, testNow)
			if state != ClassificationStale || !slices.Equal(reasons, []string{tc.want}) {
				t.Fatalf("got %s %v, want stale [%s]", state, reasons, tc.want)
			}
		})
	}
}

// Every reason that fired, in clause order — not the first.
func TestMRMReturnsEveryReason(t *testing.T) {
	row := mrmRow()
	f := validatedFacts()
	f.Validation.ValidUntil = ptr(testNow - 1)
	f.LatestVersionCreatedAt = validatedAt + 1
	f.LatestEvaluationRunAt = 0
	f.Validation.Outcome = ValidationConditional
	_, reasons := MRMStateOf(&row, f, testNow)
	want := []string{StaleValidationExpired, StaleVersionPublishedSince, StaleUnmonitoredInProduction, StaleConditionsOutstanding}
	if !slices.Equal(reasons, want) {
		t.Fatalf("reasons = %v, want %v", reasons, want)
	}
}

// The boundaries are strict in the direction that keeps a fresh record from reading stale,
// and in the direction that keeps "evaluated at the moment of promotion" from reading as
// monitoring since it.
func TestMRMBoundaries(t *testing.T) {
	row := mrmRow()
	tests := []struct {
		name  string
		facts func(*MRMFacts)
		want  ClassificationState
	}{
		{name: "validUntil exactly now is still valid", facts: func(f *MRMFacts) { f.Validation.ValidUntil = ptr[int64](testNow) }, want: ClassificationCurrent},
		{name: "a publish in the validation's millisecond is not since", facts: func(f *MRMFacts) { f.LatestVersionCreatedAt = validatedAt }, want: ClassificationCurrent},
		{name: "an evaluation in the promotion's millisecond is not monitoring since", facts: func(f *MRMFacts) { f.LatestEvaluationRunAt = promotedAt }, want: ClassificationStale},
		{name: "a non-production subject never trips clause 3", facts: func(f *MRMFacts) {
			f.Subject.Stage = StageStaging
			f.LatestEvaluationRunAt = 0
		}, want: ClassificationCurrent},
		{name: "cleared conditions are not outstanding", facts: func(f *MRMFacts) {
			f.Validation.Outcome = ValidationConditional
			f.Validation.ConditionsClearedAt = ptr[int64](validatedAt + 1)
		}, want: ClassificationCurrent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := validatedFacts()
			tc.facts(&f)
			if got, reasons := MRMStateOf(&row, f, testNow); got != tc.want {
				t.Fatalf("state = %s %v, want %s", got, reasons, tc.want)
			}
		})
	}
}

func TestIndependenceEvidenced(t *testing.T) {
	for _, tc := range []struct {
		by, author string
		want       bool
	}{
		{"mrm@acme.example", "dev@acme.example", true},
		{"dev@acme.example", "dev@acme.example", false}, // self-validated: flagged
		{"", "dev@acme.example", false},                 // nobody named: nothing evidenced
		{"mrm@acme.example", "", true},                  // §20.6's formula, read literally
	} {
		if got := IndependenceEvidenced(tc.by, tc.author); got != tc.want {
			t.Fatalf("IndependenceEvidenced(%q, %q) = %v, want %v", tc.by, tc.author, got, tc.want)
		}
	}
}
