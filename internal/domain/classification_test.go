package domain

import (
	"encoding/json"
	"testing"
)

func ptr[T any](v T) *T { return &v }

// A valid EU row, for tests that vary one thing about it.
func euClassification() RiskClassification {
	return RiskClassification{
		ModelID:           "01J000000000000000000MODEL",
		Regime:            RegimeEUAIAct,
		EUSystemRiskClass: EUClassHighAnnexIII,
		EUGpaiTier:        EUGpaiNone,
		IntendedPurpose:   "Scores card-not-present transactions for manual review.",
		Basis:             "Annex III §5(b) — creditworthiness adjacent.",
		ClassifiedAt:      1_773_000_000_000,
	}
}

func TestValidateRiskClassification(t *testing.T) {
	const now = 1_773_000_000_000

	tests := []struct {
		name    string
		mutate  func(*RiskClassification)
		code    string // "" = accepted
		allowed []string
	}{
		{name: "high risk with purpose and basis"},
		{
			name: "unclassified needs neither purpose nor basis",
			mutate: func(c *RiskClassification) {
				*c = RiskClassification{ModelID: c.ModelID, Regime: RegimeEUAIAct}
				c.ApplyDefaults()
			},
		},
		{
			name:   "minimal still needs a purpose",
			mutate: func(c *RiskClassification) { c.EUSystemRiskClass = EUClassMinimal; c.IntendedPurpose = "" },
			code:   CodeUnprocessable,
		},
		{
			name:   "minimal does not need a basis",
			mutate: func(c *RiskClassification) { c.EUSystemRiskClass = EUClassMinimal; c.Basis = "" },
		},
		{
			name:   "high_annex_iii needs a basis",
			mutate: func(c *RiskClassification) { c.Basis = "" },
			code:   CodeUnprocessable,
		},
		{
			name:   "high_annex_i needs a basis",
			mutate: func(c *RiskClassification) { c.EUSystemRiskClass = EUClassHighAnnexI; c.Basis = "" },
			code:   CodeUnprocessable,
		},
		{
			name:    "unknown class is rejected with its allowed values",
			mutate:  func(c *RiskClassification) { c.EUSystemRiskClass = "high" },
			code:    CodeInvalidArgument,
			allowed: EUSystemRiskClasses(),
		},
		{
			name:    "unknown tier is rejected with its allowed values",
			mutate:  func(c *RiskClassification) { c.EUGpaiTier = "frontier" },
			code:    CodeInvalidArgument,
			allowed: EUGpaiTiers(),
		},
		{
			name:    "unknown regime is rejected before any EU rule runs",
			mutate:  func(c *RiskClassification) { c.Regime = "uk_ai_bill"; c.EUSystemRiskClass = "" },
			code:    CodeInvalidArgument,
			allowed: []string{string(RegimeEUAIAct), string(RegimeMRM)},
		},
		{
			name:   "future review date accepted",
			mutate: func(c *RiskClassification) { c.ReviewDueAt = ptr[int64](now + 1) },
		},
		{
			name:   "past review date rejected",
			mutate: func(c *RiskClassification) { c.ReviewDueAt = ptr[int64](now - 1) },
			code:   CodeInvalidArgument,
		},
		{
			name:   "review date of exactly now is not in the future",
			mutate: func(c *RiskClassification) { c.ReviewDueAt = ptr[int64](now) },
			code:   CodeInvalidArgument,
		},
		{
			name:   "absent review date is allowed — unscheduled is a real answer",
			mutate: func(c *RiskClassification) { c.ReviewDueAt = nil },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := euClassification()
			if tc.mutate != nil {
				tc.mutate(&c)
			}
			err := ValidateRiskClassification(c, now)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want %s, got accepted", tc.code)
			}
			if err.Code != tc.code {
				t.Fatalf("want code %s, got %s (%s)", tc.code, err.Code, err.Message)
			}
			if tc.allowed == nil {
				return
			}
			got, _ := err.Details["allowedValues"].([]string)
			if len(got) != len(tc.allowed) {
				t.Fatalf("allowedValues = %v, want %v", err.Details["allowedValues"], tc.allowed)
			}
			for i := range got {
				if got[i] != tc.allowed[i] {
					t.Fatalf("allowedValues = %v, want %v", got, tc.allowed)
				}
			}
		})
	}
}

// §16.6 maps the enum failures to 400 and the missing-narrative failures to 422. The codes
// carry the status, so assert the status the client actually sees.
func TestValidateRiskClassificationStatuses(t *testing.T) {
	if got := Invalid("x").Status(); got != 400 {
		t.Fatalf("invalid_argument status = %d, want 400", got)
	}
	if got := Unprocessable("x").Status(); got != 422 {
		t.Fatalf("unprocessable status = %d, want 422", got)
	}
}

// §16.3: `unclassified` is an answer, not an absence, and must never be confused with
// `minimal`. The default fills it in explicitly rather than leaving the field empty.
func TestApplyDefaults(t *testing.T) {
	c := RiskClassification{ModelID: "m", Regime: RegimeEUAIAct}
	c.ApplyDefaults()
	if c.EUSystemRiskClass != EUClassUnclassified {
		t.Fatalf("class = %q, want %q", c.EUSystemRiskClass, EUClassUnclassified)
	}
	if c.EUGpaiTier != EUGpaiNone {
		t.Fatalf("tier = %q, want %q", c.EUGpaiTier, EUGpaiNone)
	}

	// Defaults never overwrite a stated value.
	stated := euClassification()
	stated.ApplyDefaults()
	if stated.EUSystemRiskClass != EUClassHighAnnexIII {
		t.Fatalf("defaults clobbered a stated class: %q", stated.EUSystemRiskClass)
	}
}

// §16.7.2: `source` is always `declared` on the wire, and there is no field a caller could
// set to anything else.
func TestClassificationAlwaysSerializesDeclaredSource(t *testing.T) {
	b, err := json.Marshal(euClassification())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["source"] != string(SourceDeclared) {
		t.Fatalf("source = %v, want %q", got["source"], SourceDeclared)
	}
	if got["euSystemRiskClass"] != string(EUClassHighAnnexIII) {
		t.Fatalf("euSystemRiskClass = %v", got["euSystemRiskClass"])
	}

	// A caller cannot smuggle a different source in: unmarshalling drops the key, so a
	// round-trip re-emits `declared`.
	var back RiskClassification
	if err := json.Unmarshal([]byte(`{"regime":"eu_ai_act","source":"derived"}`), &back); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(back)
	var got2 map[string]any
	_ = json.Unmarshal(b2, &got2)
	if got2["source"] != string(SourceDeclared) {
		t.Fatalf("round-tripped source = %v, want %q", got2["source"], SourceDeclared)
	}
}

// An unscheduled review is null on the wire, not epoch zero.
func TestReviewDueAtOmittedWhenUnscheduled(t *testing.T) {
	b, _ := json.Marshal(euClassification())
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if _, present := got["reviewDueAt"]; present {
		t.Fatalf("reviewDueAt present when unscheduled: %s", b)
	}
}
