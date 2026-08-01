package domain

import "testing"

// Every row of the §11.4.1 lookup, plus the partial-fact paths. The point of the four-hash
// ladder is that these are decided by table, not by heuristic, so each row is pinned.
func TestClassify(t *testing.T) {
	cases := []struct {
		name       string
		from, to   Hashes
		want       Verdict
		missing    []string
		candidates int
	}{
		{
			name: "identical — repackage",
			from: Hashes{"t", "s", "d", "w"}, to: Hashes{"t", "s", "d", "w"},
			want: VerdictIdentical,
		},
		{
			name: "reweighted — same shape and precision, different weights",
			from: Hashes{"t", "s", "d", "w1"}, to: Hashes{"t", "s", "d", "w2"},
			want: VerdictReweighted,
		},
		{
			name: "recast — quantized: only dtype differs",
			from: Hashes{"t", "s", "d16", "w1"}, to: Hashes{"t", "s", "d8", "w2"},
			want: VerdictRecast,
		},
		{
			name: "rescaled — same family, different width",
			from: Hashes{"t", "s4096", "d", "w1"}, to: Hashes{"t", "s5120", "d", "w2"},
			want: VerdictRescaled,
		},
		{
			name: "rearchitected — different topology",
			from: Hashes{"t1", "s1", "d1", "w1"}, to: Hashes{"t2", "s2", "d2", "w2"},
			want: VerdictRearchitected,
		},
		{
			name: "rearchitected is decidable from topology alone",
			from: Hashes{Topology: "t1"}, to: Hashes{Topology: "t2"},
			want: VerdictRearchitected,
		},
		{
			// A header-only producer supplies three hashes. That still separates shape
			// changes, but cannot tell an untouched republish from a fine-tune (§11.4.3).
			name: "header-only — narrowed, not guessed",
			from: Hashes{Topology: "t", Shape: "s", Dtype: "d"},
			to:   Hashes{Topology: "t", Shape: "s", Dtype: "d"},
			want: VerdictUnknown, missing: []string{"weights"}, candidates: 2,
		},
		{
			name: "no facts at all",
			from: Hashes{}, to: Hashes{},
			want: VerdictUnknown, missing: []string{"topology"},
		},
		{
			name: "one side never reported",
			from: Hashes{"t", "s", "d", "w"}, to: Hashes{},
			want: VerdictUnknown, missing: []string{"topology"},
		},
		{
			name: "shape missing on one side",
			from: Hashes{Topology: "t", Shape: "s"}, to: Hashes{Topology: "t"},
			want: VerdictUnknown, missing: []string{"shape"}, candidates: 4,
		},
		{
			name: "dtype missing on one side",
			from: Hashes{Topology: "t", Shape: "s", Dtype: "d"}, to: Hashes{Topology: "t", Shape: "s"},
			want: VerdictUnknown, missing: []string{"dtype"}, candidates: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.from, tc.to)
			if got.Verdict != tc.want {
				t.Fatalf("verdict = %q, want %q", got.Verdict, tc.want)
			}
			if len(tc.missing) > 0 {
				if len(got.Missing) != len(tc.missing) || got.Missing[0] != tc.missing[0] {
					t.Fatalf("missing = %v, want %v", got.Missing, tc.missing)
				}
			}
			if len(got.Candidates) != tc.candidates {
				t.Fatalf("candidates = %v, want %d of them", got.Candidates, tc.candidates)
			}
			// A determinate verdict must never also advertise candidates: that would read
			// as hedging on an answer the table actually settled.
			if got.Verdict != VerdictUnknown && len(got.Candidates) > 0 {
				t.Fatalf("determinate verdict %q should not carry candidates %v", got.Verdict, got.Candidates)
			}
		})
	}
}

// The hash comparison must distinguish "both sides reported, and they match" from "one
// side never reported" — collapsing those is how a registry starts inventing facts.
func TestClassifyHashPresence(t *testing.T) {
	c := Classify(Hashes{Topology: "t", Shape: "s", Dtype: "d"}, Hashes{Topology: "t", Shape: "s", Dtype: "d"})
	if w := c.Hashes["weights"]; w.Present || w.Changed != nil {
		t.Fatalf("absent weights hash should be Present=false, Changed=nil; got %+v", w)
	}
	if tp := c.Hashes["topology"]; !tp.Present || tp.Changed == nil || *tp.Changed {
		t.Fatalf("matching topology should be Present=true, Changed=false; got %+v", tp)
	}
}
