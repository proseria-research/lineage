package domain

import "testing"

// The envelope is checked against the real verdict enum (§22.3): anything the §11.4.1 table
// cannot produce, or `unknown`, which is the absence of a verdict, is refused with the list.
func TestValidateChangePlanEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		verdicts []Verdict
		methods  []string
		ok       bool
	}{
		{"identical and reweighted", []Verdict{VerdictIdentical, VerdictReweighted}, nil, true},
		{"with methods", []Verdict{VerdictReweighted}, []string{"retrain", "fine_tune"}, true},
		{"empty verdicts", nil, nil, false},
		{"free text", []Verdict{"minor retraining only"}, nil, false},
		{"unknown is not a verdict", []Verdict{VerdictUnknown}, nil, false},
		{"empty methods list", []Verdict{VerdictReweighted}, []string{}, false},
		{"blank method", []Verdict{VerdictReweighted}, []string{""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &ChangePlan{AllowedVerdicts: tc.verdicts, AllowedMethods: tc.methods}
			err := ValidateChangePlanEnvelope(p)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if err != nil && err.Code != CodeInvalidArgument {
				t.Fatalf("code = %s", err.Code)
			}
		})
	}

	err := ValidateChangePlanEnvelope(&ChangePlan{AllowedVerdicts: []Verdict{"minor"}})
	if allowed, _ := err.Details["allowedValues"].([]string); len(allowed) != 5 {
		t.Fatalf("allowedValues lists the five plannable verdicts: %v", err.Details)
	}

	p := &ChangePlan{AllowedVerdicts: []Verdict{VerdictReweighted, VerdictIdentical, VerdictReweighted},
		AllowedMethods: []string{"retrain", "retrain"}}
	if err := ValidateChangePlanEnvelope(p); err != nil {
		t.Fatal(err)
	}
	if len(p.AllowedVerdicts) != 2 || p.AllowedVerdicts[0] != VerdictReweighted || len(p.AllowedMethods) != 1 {
		t.Fatalf("duplicates dropped, order kept: %v %v", p.AllowedVerdicts, p.AllowedMethods)
	}
}
