package domain

import "encoding/json"

// Risk-classification entities (§16). A classification is an *assessment*: somebody stated a
// risk class for a model, on a date, for a reason, with their own review cycle. The registry
// stores what was declared and notices when it has gone out of date (§16.5). It never decides
// a class, never downgrades one, and never blocks anything because one is stale (§16.2).

// ---- Enums (§16.3, §16.7.1) ----

// Regime is one body of rules. The EU AI Act is one; US model-risk supervision (`20`) will be
// another. It is the discriminator column of the classification table, so each regime keeps
// its own row per model — its own date, author, reason and review cycle (§16.3.2).
type Regime string

const (
	RegimeEUAIAct Regime = "eu_ai_act"
)

// Regimes lists every regime this build knows, in a stable order. It backs both the
// `details.allowedValues` of a rejected write and the per-dialect CHECK (§16.7.1).
var Regimes = []Regime{RegimeEUAIAct}

func ValidRegime(r Regime) bool {
	for _, k := range Regimes {
		if k == r {
			return true
		}
	}
	return false
}

// EUSystemRiskClass describes the *systems* a model is used in, as declared by a person
// (Annex III / Annex I). The values are one jurisdiction's vocabulary, which is why the type
// and its column carry the `eu` prefix (§16.3.1).
type EUSystemRiskClass string

const (
	// EUClassUnclassified is a real, visible answer meaning "nobody has said yet". It is
	// never rendered as, and never quietly becomes, `minimal` (§16.3).
	EUClassUnclassified EUSystemRiskClass = "unclassified"
	EUClassMinimal      EUSystemRiskClass = "minimal"
	EUClassLimited      EUSystemRiskClass = "limited"
	EUClassHighAnnexIII EUSystemRiskClass = "high_annex_iii"
	EUClassHighAnnexI   EUSystemRiskClass = "high_annex_i"
	EUClassProhibited   EUSystemRiskClass = "prohibited"
)

var euSystemRiskClasses = []EUSystemRiskClass{
	EUClassUnclassified, EUClassMinimal, EUClassLimited,
	EUClassHighAnnexIII, EUClassHighAnnexI, EUClassProhibited,
}

func ValidEUSystemRiskClass(c EUSystemRiskClass) bool {
	for _, k := range euSystemRiskClasses {
		if k == c {
			return true
		}
	}
	return false
}

// EUSystemRiskClasses lists the allowed values, for `details.allowedValues` (§16.6).
func EUSystemRiskClasses() []string { return stringsOf(euSystemRiskClasses) }

// IsHighRisk reports whether the class points at one of the Act's high-risk annexes. Those
// are the two that must also carry a stated basis (§16.6).
func (c EUSystemRiskClass) IsHighRisk() bool {
	return c == EUClassHighAnnexIII || c == EUClassHighAnnexI
}

// EUGpaiTier describes the *model itself* under Art. 53/55. The Act treats AI systems and
// general-purpose models as separate questions, so one field cannot hold both (§16.3).
type EUGpaiTier string

const (
	EUGpaiNone     EUGpaiTier = "none"
	EUGpai         EUGpaiTier = "gpai"
	EUGpaiSystemic EUGpaiTier = "gpai_systemic"
)

var euGpaiTiers = []EUGpaiTier{EUGpaiNone, EUGpai, EUGpaiSystemic}

func ValidEUGpaiTier(t EUGpaiTier) bool {
	for _, k := range euGpaiTiers {
		if k == t {
			return true
		}
	}
	return false
}

// EUGpaiTiers lists the allowed values, for `details.allowedValues` (§16.6).
func EUGpaiTiers() []string { return stringsOf(euGpaiTiers) }

// ---- State & drift vocabulary (§16.4, §16.5) ----

// ClassificationState is three-valued, per regime. "Nobody classified it" is not a kind of
// "out of date": a reader filtering for one must not be handed the other (§16.4).
type ClassificationState string

const (
	ClassificationUnclassified ClassificationState = "unclassified"
	ClassificationStale        ClassificationState = "stale"
	ClassificationCurrent      ClassificationState = "current"
)

// Stale reasons, one per disjunct of the §16.5 predicate. A read returns *every* reason that
// fired, not the first — someone deciding whether to redo an assessment wants the whole
// picture. The strings are part of the API contract.
const (
	StaleReviewDuePassed       = "review_due_passed"
	StaleVersionPublishedSince = "version_published_since"
	StaleProductionChanged     = "production_changed_since"
	StaleDerivationSince       = "derivation_since"
)

// ---- Entity (§16.7.1) ----

// RiskClassification is one model's assessment under one regime, keyed (ModelID, Regime).
//
// Only the `EU*` fields are jurisdictional. IntendedPurpose, Basis, ClassifiedAt,
// ClassifiedBy and ReviewDueAt keep plain names because every regime wants a stated purpose,
// a reason and a review date — it just wants *its own*, which is what the per-regime row buys
// (§16.3.2).
//
// There is deliberately no Source field: a classification is always `declared`, and neither
// the struct nor the schema should hint that a computed path could exist (§16.7.2). The
// constant is emitted on the wire by MarshalJSON.
type RiskClassification struct {
	ModelID string `json:"modelId"`
	Regime  Regime `json:"regime"`

	EUGpaiTier        EUGpaiTier        `json:"euGpaiTier,omitempty"`
	EUSystemRiskClass EUSystemRiskClass `json:"euSystemRiskClass,omitempty"`

	IntendedPurpose string `json:"intendedPurpose,omitempty"`
	Basis           string `json:"basis,omitempty"`

	// ClassifiedAt is this regime's "since when" — the anchor every §16.5 clause measures
	// against. Server-set on write; a value in the request is ignored (§16.6).
	ClassifiedAt int64  `json:"classifiedAt"`
	ClassifiedBy string `json:"classifiedBy,omitempty"`

	// ReviewDueAt is nil when no review is scheduled, which the console surfaces rather than
	// hides. A pointer, so "unscheduled" stays distinct from the zero epoch.
	ReviewDueAt *int64 `json:"reviewDueAt,omitempty"`
}

// RiskClassificationSource is the only source a classification can have (§16.7.2).
const RiskClassificationSource = SourceDeclared

// MarshalJSON emits the fixed `source` (§16.7.2, §16.8.2) without giving the struct a
// settable field for it.
func (c RiskClassification) MarshalJSON() ([]byte, error) {
	type alias RiskClassification
	return json.Marshal(struct {
		alias
		Source FactSource `json:"source"`
	}{alias(c), RiskClassificationSource})
}

// ApplyDefaults fills the EU row's schema defaults (§16.7.1) so a memory store and a SQL
// store agree on what an omitted field means. `unclassified` and `none` are answers, not
// absences, so they are materialized rather than left empty.
func (c *RiskClassification) ApplyDefaults() {
	if c.Regime != RegimeEUAIAct {
		return
	}
	if c.EUSystemRiskClass == "" {
		c.EUSystemRiskClass = EUClassUnclassified
	}
	if c.EUGpaiTier == "" {
		c.EUGpaiTier = EUGpaiNone
	}
}

// ---- Validation (§16.6) ----

// ValidateRiskClassification checks a write against §16.6 and returns the coded error the API
// surfaces. `now` is the server clock; ClassifiedAt and ClassifiedBy are not checked here
// because they are server-set and any request value is discarded before this runs.
//
// Call ApplyDefaults first: this treats an empty enum as a rejected value, not as a default.
func ValidateRiskClassification(c RiskClassification, now int64) *Error {
	if !ValidRegime(c.Regime) {
		return invalidWithAllowed("unknown regime", "regime", stringsOf(Regimes))
	}
	if c.Regime != RegimeEUAIAct {
		return nil
	}

	if !ValidEUSystemRiskClass(c.EUSystemRiskClass) {
		return invalidWithAllowed("unknown euSystemRiskClass", "euSystemRiskClass", EUSystemRiskClasses())
	}
	if !ValidEUGpaiTier(c.EUGpaiTier) {
		return invalidWithAllowed("unknown euGpaiTier", "euGpaiTier", EUGpaiTiers())
	}

	// A class nobody stated a purpose for cannot be reviewed by anyone.
	if c.EUSystemRiskClass != EUClassUnclassified && c.IntendedPurpose == "" {
		return Unprocessable("intendedPurpose is required unless euSystemRiskClass is " + string(EUClassUnclassified))
	}
	// High risk is the claim that most needs its reasoning recorded, not just its conclusion.
	if c.EUSystemRiskClass.IsHighRisk() && c.Basis == "" {
		return Unprocessable("basis is required for " + string(c.EUSystemRiskClass))
	}
	if c.ReviewDueAt != nil && *c.ReviewDueAt <= now {
		return Invalid("reviewDueAt must be in the future")
	}
	return nil
}

func invalidWithAllowed(msg, field string, allowed []string) *Error {
	e := Invalid(msg)
	e.Details = map[string]any{"field": field, "allowedValues": allowed}
	return e
}

func stringsOf[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

// ---- Read model (§16.8.2) ----

// ClassificationView is a stored row plus the state computed from it at read time (§16.5).
// The two are separate types on purpose: State and StaleReasons are derived, never stored,
// and giving the entity fields for them would create somewhere for a stale copy to live.
//
// It embeds by value for field access, but defines its own MarshalJSON — the embedded
// RiskClassification has one, and a promoted MarshalJSON would otherwise hijack the encode
// and silently drop the two fields this type exists to add.
type ClassificationView struct {
	RiskClassification
	State        ClassificationState `json:"state"`
	StaleReasons []string            `json:"staleReasons,omitempty"`
}

func (v ClassificationView) MarshalJSON() ([]byte, error) {
	type inner RiskClassification
	return json.Marshal(struct {
		inner
		Source       FactSource          `json:"source"`
		State        ClassificationState `json:"state"`
		StaleReasons []string            `json:"staleReasons,omitempty"`
	}{inner(v.RiskClassification), RiskClassificationSource, v.State, v.StaleReasons})
}

// ModelInventoryItem is a model list entry carrying its classification for one regime
// (§16.8.2). Classification is nil when the model has no row for that regime — absence *is*
// the `unclassified` state (§16.4), and inventing an empty row to carry the word would put a
// classification in the response that nobody wrote.
//
// Model embeds safely here: it has no MarshalJSON of its own, so its fields flatten into the
// item exactly as they do on GET /v1/models today.
type ModelInventoryItem struct {
	*Model
	Classification *ClassificationView `json:"classification,omitempty"`
}
