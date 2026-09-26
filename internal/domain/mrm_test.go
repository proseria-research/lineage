package domain

import (
	"slices"
	"testing"
)

func mrmRow() RiskClassification {
	return RiskClassification{
		ModelID: "m", Regime: RegimeMRM, MRMTier: MRMTier1,
		Basis: "Drives automated card-not-present declines above $500.", ClassifiedAt: classifiedAt,
	}
}

func TestValidateMRMClassification(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*RiskClassification)
		code    string // "" = accepted
		field   string
		allowed []string
	}{
		{name: "tier_1 with a basis"},
		{
			name:   "tier_1 needs a basis",
			mutate: func(c *RiskClassification) { c.Basis = "" },
			code:   CodeUnprocessable, field: "basis",
		},
		{
			// The SR 26-2 carve-out is declared, and a declaration that opts out needs its reason.
			name:   "out_of_scope needs a basis",
			mutate: func(c *RiskClassification) { c.MRMTier = MRMOutOfScope; c.Basis = "" },
			code:   CodeUnprocessable, field: "basis",
		},
		{
			name:   "tier_2 does not need a basis",
			mutate: func(c *RiskClassification) { c.MRMTier = MRMTier2; c.Basis = "" },
		},
		{
			name:   "no intendedPurpose is required on an mrm row",
			mutate: func(c *RiskClassification) { c.IntendedPurpose = "" },
		},
		{
			name: "an omitted tier defaults to untiered, which needs nothing",
			mutate: func(c *RiskClassification) {
				*c = RiskClassification{ModelID: "m", Regime: RegimeMRM}
				c.ApplyDefaults()
			},
		},
		{
			name:   "unknown tier is rejected with its allowed values",
			mutate: func(c *RiskClassification) { c.MRMTier = "tier_4" },
			code:   CodeInvalidArgument, field: "mrmTier",
			allowed: MRMTiers(),
		},
		{
			name:   "an EU class on the mrm row is the wrong path",
			mutate: func(c *RiskClassification) { c.EUSystemRiskClass = EUClassMinimal },
			code:   CodeInvalidArgument, field: "euSystemRiskClass",
		},
		{
			name:   "an EU tier on the mrm row is the wrong path",
			mutate: func(c *RiskClassification) { c.EUGpaiTier = EUGpaiNone },
			code:   CodeInvalidArgument, field: "euGpaiTier",
		},
		{
			name:   "past review date rejected",
			mutate: func(c *RiskClassification) { c.ReviewDueAt = ptr[int64](classifiedAt) },
			code:   CodeInvalidArgument,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := mrmRow()
			if tc.mutate != nil {
				tc.mutate(&c)
			}
			err := ValidateRiskClassification(c, classifiedAt)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			if err == nil || err.Code != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
			if tc.field != "" && err.Details["field"] != tc.field {
				t.Fatalf("details.field = %v, want %s", err.Details["field"], tc.field)
			}
			if tc.allowed != nil && !slices.Equal(err.Details["allowedValues"].([]string), tc.allowed) {
				t.Fatalf("allowedValues = %v", err.Details["allowedValues"])
			}
		})
	}

	// And the reverse: an mrm tier on the EU row.
	eu := euClassification()
	eu.MRMTier = MRMTier1
	if err := ValidateRiskClassification(eu, classifiedAt); err == nil || err.Details["field"] != "mrmTier" {
		t.Fatalf("mrmTier on eu_ai_act: %v", err)
	}
}

func TestApplyDefaultsTouchesOnlyItsOwnGroup(t *testing.T) {
	c := RiskClassification{Regime: RegimeMRM}
	c.ApplyDefaults()
	if c.MRMTier != MRMUntiered || c.EUSystemRiskClass != "" || c.EUGpaiTier != "" {
		t.Fatalf("mrm defaults: %+v", c)
	}
	e := RiskClassification{Regime: RegimeEUAIAct}
	e.ApplyDefaults()
	if e.MRMTier != "" {
		t.Fatalf("EU defaults filled the mrm group: %+v", e)
	}
}
