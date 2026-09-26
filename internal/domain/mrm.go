package domain

// Model risk management (§20). SR 26-2, PRA SS1/23 and OSFI E-23 ask for the same three
// things — a tiered inventory, independent validation, ongoing monitoring — so one field set
// serves all three (§20.3). The tier is a second regime on the §16 classification table; the
// validation is its own append-only record; the monitoring check is computed on read.
//
// As in §16, the registry records and flags. It never assigns a tier, never judges a
// validation, and never blocks a promotion because a validation has gone stale (§20.1).

// ---- Tier (§20.4) ----

// MRMTier is the firm-assigned model-risk tier. Three ordered buckets rather than the firm's
// own labels: supervisors require tiering without mandating names, and a free-text tier would
// make the inventory query useless (§20.4).
type MRMTier string

const (
	// MRMUntiered is a visible state, never rendered as low risk (§20.4).
	MRMUntiered MRMTier = "untiered"
	MRMTier1    MRMTier = "tier_1"
	MRMTier2    MRMTier = "tier_2"
	MRMTier3    MRMTier = "tier_3"
	// MRMOutOfScope is a *declared* exclusion from the firm's framework — the SR 26-2 genAI
	// carve-out is the expected use. Declared, never inferred (§20.3).
	MRMOutOfScope MRMTier = "out_of_scope"
)

var mrmTiers = []MRMTier{MRMUntiered, MRMTier1, MRMTier2, MRMTier3, MRMOutOfScope}

func ValidMRMTier(t MRMTier) bool {
	for _, k := range mrmTiers {
		if k == t {
			return true
		}
	}
	return false
}

// MRMTiers lists the allowed values, for `details.allowedValues`.
func MRMTiers() []string { return stringsOf(mrmTiers) }

// NeedsBasis reports whether the tier must carry a stated reason: the most severe answer and
// the one that opts out, by the same argument as §16.6's high-risk rule (§20.4).
func (t MRMTier) NeedsBasis() bool { return t == MRMTier1 || t == MRMOutOfScope }

// validateMRM is the `mrm` row's half of ValidateRiskClassification. intendedPurpose is not
// required here: §20 asks for a tier and its reasoning, and the reasoning is `basis`.
func validateMRM(c RiskClassification, now int64) *Error {
	// The EU group on an MRM row is a caller writing to the wrong path (§16.3.2).
	if c.EUSystemRiskClass != "" {
		return invalidField("euSystemRiskClass belongs to the eu_ai_act regime, not mrm", "euSystemRiskClass")
	}
	if c.EUGpaiTier != "" {
		return invalidField("euGpaiTier belongs to the eu_ai_act regime, not mrm", "euGpaiTier")
	}
	if !ValidMRMTier(c.MRMTier) {
		return invalidWithAllowed("unknown mrmTier", "mrmTier", MRMTiers())
	}
	if c.MRMTier.NeedsBasis() && c.Basis == "" {
		return unprocessableField("basis is required for "+string(c.MRMTier), "basis")
	}
	if c.ReviewDueAt != nil && *c.ReviewDueAt <= now {
		return Invalid("reviewDueAt must be in the future")
	}
	return nil
}

// ---- Validation (§20.5) ----

// ValidationOutcome is what the validator concluded.
type ValidationOutcome string

const (
	ValidationApproved ValidationOutcome = "approved"
	// ValidationConditional is fit *subject to conditions*, which are required. It exists
	// because it is the common real answer, and forcing it into approved-or-not would lose
	// the conditions that make it true (§20.5).
	ValidationConditional  ValidationOutcome = "conditional"
	ValidationRejected     ValidationOutcome = "rejected"
	ValidationUndetermined ValidationOutcome = "undetermined"
)

var validationOutcomes = []ValidationOutcome{
	ValidationApproved, ValidationConditional, ValidationRejected, ValidationUndetermined,
}

func ValidValidationOutcome(o ValidationOutcome) bool {
	for _, k := range validationOutcomes {
		if k == o {
			return true
		}
	}
	return false
}

// ValidationOutcomes lists the allowed values, for `details.allowedValues`.
func ValidationOutcomes() []string { return stringsOf(validationOutcomes) }

// Relied reports whether the outcome says the version is fit to rely on. Only these two
// reach the stale/current half of the §20.7 ladder; see MRMStateOf.
func (o ValidationOutcome) Relied() bool {
	return o == ValidationApproved || o == ValidationConditional
}

// Validation is one independent judgement on one version (§20.8.2) — a judgement, not a
// measurement, so it is not an Evaluation (§20.5).
//
// Append-only, like Evaluation and ModificationReview: a re-validation is a new row and the
// current answer is the latest row per version. The one later write is ConditionsClearedAt,
// set once, never unset.
type Validation struct {
	ID        string            `json:"id"`
	VersionID string            `json:"versionId"`
	Outcome   ValidationOutcome `json:"outcome"`
	Scope     string            `json:"scope,omitempty"`
	Findings  string            `json:"findings,omitempty"`
	// Conditions is required non-empty when Outcome is conditional.
	Conditions string `json:"conditions,omitempty"`
	// ConditionsClearedAt drives §20.7 clause 4. Nil until a later write clears them.
	ConditionsClearedAt *int64 `json:"conditionsClearedAt,omitempty"`
	// ValidUntil is nil for no expiry, which the console surfaces rather than hides.
	ValidUntil *int64 `json:"validUntil,omitempty"`
	// EvidenceArtifactID is the report itself, an artifact of kind DOC or METRICS on the
	// same version.
	EvidenceArtifactID string `json:"evidenceArtifactId,omitempty"`
	// ValidatedBy and ValidatedAt are server-set: X-Lineage-Actor and the server clock. The
	// value of either is that the registry witnessed it (§20.9.1).
	ValidatedBy string `json:"validatedBy,omitempty"`
	ValidatedAt int64  `json:"validatedAt"`
}

// ValidationSource is the only source a validation can have: a person says so (§20.5).
const ValidationSource = SourceDeclared

// ValidationView is a stored validation plus what is derived from it on read.
//
// IndependenceEvidenced is §20.6: the validator is named and is not the version's author. It
// is a flag, never a refusal — a one-person team, a shared service account, or an actor
// header carrying a team are all legitimate and all trip it, and only a human can say which
// of those it is.
type ValidationView struct {
	*Validation
	IndependenceEvidenced bool       `json:"independenceEvidenced"`
	Source                FactSource `json:"source"`
}

// IndependenceEvidenced is the §20.6 predicate. A version with no recorded author compares as
// independent, exactly as the doc's formula reads: there is no name to have matched.
func IndependenceEvidenced(validatedBy, author string) bool {
	return validatedBy != "" && validatedBy != author
}

// NewValidationView attaches the derived fields.
func NewValidationView(v *Validation, author string) *ValidationView {
	return &ValidationView{Validation: v, IndependenceEvidenced: IndependenceEvidenced(v.ValidatedBy, author), Source: ValidationSource}
}

// ---- Facts (§20.7) ----

// MRMSubject is the version a model's MRM state is about.
type MRMSubject struct {
	VersionID string
	Version   string
	Author    string
	Stage     Stage
	// StageChangedAt is when the version entered its current stage — the anchor of clause 3.
	StageChangedAt int64
}

// MRMFacts are the stored facts §20.7 measures against, gathered by the caller so the
// predicate stays a pure function (§16.5.1), exactly as DriftFacts is.
//
// The subject is the model's **production version if it has one, else its newest version**.
// §20.7 is written per version while the inventory is per model, so a model-level read has to
// pick one; production is the version the monitoring clause is about, and the newest is the
// one a validator would be looking at before there is a production version (§20.7, as
// corrected).
type MRMFacts struct {
	// Subject is nil when the model has no versions.
	Subject *MRMSubject
	// LatestVersionCreatedAt is the model's newest version, for clause 2. Zero = none.
	LatestVersionCreatedAt int64
	// LatestEvaluationRunAt is the subject's newest evaluation run_at, for clause 3.
	// Zero = none, which compares as "not since" on its own.
	LatestEvaluationRunAt int64
	// Validation is the subject's latest validation, nil when it has none.
	Validation *Validation
}
