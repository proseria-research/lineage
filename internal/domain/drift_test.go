package domain

import (
	"slices"
	"testing"
)

const (
	classifiedAt = int64(1_773_000_000_000)
	testNow      = classifiedAt + 90*24*60*60*1000 // 90 days later
	before       = classifiedAt - 1
	after        = classifiedAt + 1
)

// classified returns a `current` high-risk row: no review scheduled, nothing stale.
func classified() *RiskClassification {
	return &RiskClassification{
		ModelID:           "m",
		Regime:            RegimeEUAIAct,
		EUSystemRiskClass: EUClassHighAnnexIII,
		EUGpaiTier:        EUGpaiNone,
		IntendedPurpose:   "p",
		Basis:             "b",
		ClassifiedAt:      classifiedAt,
	}
}

// Each §16.5 clause must fire on its own, so a reader can trust the reason it is shown.
func TestEachDriftClauseFiresIndependently(t *testing.T) {
	tests := []struct {
		name  string
		c     func(*RiskClassification)
		facts DriftFacts
		want  string
	}{{
		name: "clause 1 — review date passed",
		c:    func(c *RiskClassification) { c.ReviewDueAt = ptr(testNow - 1) },
		want: StaleReviewDuePassed,
	}, {
		name:  "clause 2 — a version was published since",
		facts: DriftFacts{LatestVersionCreatedAt: after},
		want:  StaleVersionPublishedSince,
	}, {
		name:  "clause 3 — the production version changed since",
		facts: DriftFacts{LatestProductionUpdatedAt: after},
		want:  StaleProductionChanged,
	}, {
		name:  "clause 4 — a review item was opened since",
		facts: DriftFacts{LatestOpenReviewCreatedAt: after},
		want:  StaleDerivationSince,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := classified()
			if tc.c != nil {
				tc.c(c)
			}
			state, reasons := ClassificationStateOf(c, tc.facts, testNow)
			if state != ClassificationStale {
				t.Fatalf("state = %q, want stale", state)
			}
			if len(reasons) != 1 || reasons[0] != tc.want {
				t.Fatalf("reasons = %v, want exactly [%s]", reasons, tc.want)
			}
		})
	}
}

// staleReasons[] returns *every* reason that applies, not the first — someone deciding
// whether to redo an assessment wants the whole picture (§16.5).
func TestAllFiredReasonsAreReturnedInClauseOrder(t *testing.T) {
	c := classified()
	c.ReviewDueAt = ptr(testNow - 1)
	state, reasons := ClassificationStateOf(c, DriftFacts{
		LatestVersionCreatedAt:    after,
		LatestProductionUpdatedAt: after,
		LatestOpenReviewCreatedAt: after,
	}, testNow)

	if state != ClassificationStale {
		t.Fatalf("state = %q, want stale", state)
	}
	want := []string{StaleReviewDuePassed, StaleVersionPublishedSince, StaleProductionChanged, StaleDerivationSince}
	if !slices.Equal(reasons, want) {
		t.Fatalf("reasons = %v, want %v", reasons, want)
	}
}

// Nothing has happened since: current, with no reasons.
func TestCurrentWhenNothingChangedSince(t *testing.T) {
	state, reasons := ClassificationStateOf(classified(), DriftFacts{
		// All strictly older than classifiedAt — the model existed before it was classified.
		LatestVersionCreatedAt:    before,
		LatestProductionUpdatedAt: before,
		LatestOpenReviewCreatedAt: before,
	}, testNow)
	if state != ClassificationCurrent {
		t.Fatalf("state = %q, want current", state)
	}
	if len(reasons) != 0 {
		t.Fatalf("reasons = %v, want none", reasons)
	}
}

// §16.4: `unclassified` is not a kind of `stale`. Both ways of being unclassified — no row
// at all, and a row that says `unclassified` — short-circuit before any clause runs, even
// when every fact that would otherwise trip one is present.
func TestUnclassifiedIsNotStale(t *testing.T) {
	everythingStale := DriftFacts{
		LatestVersionCreatedAt:    after,
		LatestProductionUpdatedAt: after,
		LatestOpenReviewCreatedAt: after,
	}

	t.Run("no row for this regime", func(t *testing.T) {
		state, reasons := ClassificationStateOf(nil, everythingStale, testNow)
		if state != ClassificationUnclassified {
			t.Fatalf("state = %q, want unclassified", state)
		}
		if reasons != nil {
			t.Fatalf("reasons = %v, want none", reasons)
		}
	})

	t.Run("a row that says unclassified", func(t *testing.T) {
		c := classified()
		c.EUSystemRiskClass = EUClassUnclassified
		c.ReviewDueAt = ptr(testNow - 1) // even with a blown review date
		state, reasons := ClassificationStateOf(c, everythingStale, testNow)
		if state != ClassificationUnclassified {
			t.Fatalf("state = %q, want unclassified", state)
		}
		if reasons != nil {
			t.Fatalf("reasons = %v, want none", reasons)
		}
	})
}

// Absent facts are zero, and zero must never read as "older than the epoch, therefore fine"
// *or* trip a clause. A model classified before it had any versions is current.
func TestAbsentFactsDoNotFire(t *testing.T) {
	state, reasons := ClassificationStateOf(classified(), DriftFacts{}, testNow)
	if state != ClassificationCurrent {
		t.Fatalf("state = %q reasons = %v, want current with none", state, reasons)
	}
}

// The comparisons are strict: a fact stamped at exactly ClassifiedAt did not happen *since*
// the classification. Without this, classifying a model in the same millisecond as a publish
// would report the model stale the instant it was classified.
func TestSimultaneousFactsAreNotSince(t *testing.T) {
	state, reasons := ClassificationStateOf(classified(), DriftFacts{
		LatestVersionCreatedAt:    classifiedAt,
		LatestProductionUpdatedAt: classifiedAt,
		LatestOpenReviewCreatedAt: classifiedAt,
	}, testNow)
	if state != ClassificationCurrent {
		t.Fatalf("state = %q reasons = %v, want current", state, reasons)
	}
}

// An unscheduled review is not an overdue one (§16.7.1: null means no review scheduled).
func TestNoReviewDateNeverFiresClauseOne(t *testing.T) {
	c := classified()
	c.ReviewDueAt = nil
	if state, _ := ClassificationStateOf(c, DriftFacts{}, testNow); state != ClassificationCurrent {
		t.Fatalf("state = %q, want current", state)
	}

	// A review date still in the future is likewise fine.
	c.ReviewDueAt = ptr(testNow + 1)
	if state, _ := ClassificationStateOf(c, DriftFacts{}, testNow); state != ClassificationCurrent {
		t.Fatalf("future review date should not be stale, got %q", state)
	}

	// Exactly now has not passed.
	c.ReviewDueAt = ptr(testNow)
	if state, _ := ClassificationStateOf(c, DriftFacts{}, testNow); state != ClassificationCurrent {
		t.Fatalf("review date of exactly now should not be stale, got %q", state)
	}
}

// §16.5 keeps one false positive on purpose: clause 3 reads updated_at, so editing the
// production version's *description* marks the classification stale. This asserts the
// behaviour is intended, so a later "fix" has to argue with a test rather than a comment.
func TestProductionEditIsADeliberateFalsePositive(t *testing.T) {
	// A description edit bumps updated_at without any stage change or new version.
	state, reasons := ClassificationStateOf(classified(), DriftFacts{
		LatestVersionCreatedAt:    before, // no new version
		LatestProductionUpdatedAt: after,  // only the edit
	}, testNow)

	if state != ClassificationStale {
		t.Fatalf("state = %q, want stale — the false positive is intended, see §16.5", state)
	}
	if len(reasons) != 1 || reasons[0] != StaleProductionChanged {
		t.Fatalf("reasons = %v, want exactly [%s] so the reader can see what tripped it",
			reasons, StaleProductionChanged)
	}
}
