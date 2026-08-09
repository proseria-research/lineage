package domain

import "testing"

// The §17.4 gate, one case per reason it can fire or not. Two of these are the whole point of
// the doc: `unknown` must queue, and `identical` must not.
func TestReviewEligible(t *testing.T) {
	cases := []struct {
		name  string
		v     Verdict
		class EUSystemRiskClass
		tier  EUGpaiTier
		want  bool
	}{
		{"reweighted on annex iii", VerdictReweighted, EUClassHighAnnexIII, EUGpaiNone, true},
		{"reweighted on annex i", VerdictReweighted, EUClassHighAnnexI, EUGpaiNone, true},
		{"recast on annex iii", VerdictRecast, EUClassHighAnnexIII, EUGpaiNone, true},
		{"rescaled on annex iii", VerdictRescaled, EUClassHighAnnexIII, EUGpaiNone, true},
		{"rearchitected on annex iii", VerdictRearchitected, EUClassHighAnnexIII, EUGpaiNone, true},

		// "We cannot tell what changed" is the case that most wants human eyes. Excluding it
		// would make a missing weights_hash read as a clean bill of health.
		{"unknown on annex iii", VerdictUnknown, EUClassHighAnnexIII, EUGpaiNone, true},

		// A repackage moved no bytes; there is nothing for a reviewer to look at.
		{"identical on annex iii", VerdictIdentical, EUClassHighAnnexIII, EUGpaiNone, false},
		{"identical on gpai", VerdictIdentical, EUClassMinimal, EUGpaiSystemic, false},

		// The tier qualifies on its own — a derived GPAI picks up its own Art. 53 duties, so
		// it is not a weaker form of the system class.
		{"gpai with a minimal system class", VerdictReweighted, EUClassMinimal, EUGpai, true},
		{"systemic gpai", VerdictReweighted, EUClassUnclassified, EUGpaiSystemic, true},

		{"limited, no tier", VerdictReweighted, EUClassLimited, EUGpaiNone, false},
		{"minimal, no tier", VerdictRearchitected, EUClassMinimal, EUGpaiNone, false},
		{"unclassified, no tier", VerdictUnknown, EUClassUnclassified, EUGpaiNone, false},
		{"empty enums (no classification row)", VerdictReweighted, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ReviewEligible(c.v, c.class, c.tier); got != c.want {
				t.Fatalf("ReviewEligible(%q,%q,%q) = %v, want %v", c.v, c.class, c.tier, got, c.want)
			}
		})
	}
}

func TestValidReviewOutcome(t *testing.T) {
	for _, o := range []ReviewOutcome{OutcomeNotSubstantial, OutcomeSubstantial, OutcomeUndetermined} {
		if !ValidReviewOutcome(o) {
			t.Fatalf("%q should be valid", o)
		}
	}
	for _, o := range []ReviewOutcome{"", "SUBSTANTIAL", "pending", "not_reviewed"} {
		if ValidReviewOutcome(o) {
			t.Fatalf("%q should not be valid", o)
		}
	}
	if got := ReviewOutcomes(); len(got) != 3 {
		t.Fatalf("ReviewOutcomes() = %v", got)
	}
}
