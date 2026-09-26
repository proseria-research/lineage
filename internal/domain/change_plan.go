package domain

// Change control plans (§22). A plan is an envelope of changes declared in advance — the FDA
// PCCP shape — and conformance is whether what actually shipped falls inside it. The envelope
// is written in the §11.4.1 verdict vocabulary, so the check is a lookup over hashes the
// registry already holds rather than a reading of prose.
//
// The registry reports; it does not adjudicate. `outside_plan` is a fact about the plan, not
// a finding that a new submission is required, and nothing here blocks a publish (§22.5).

// ---- Entity (§22.6.1) ----

// ChangePlan is one declared envelope for one model.
//
// Rows are **append-only**, like Validation and ModificationReview. Superseding a plan writes a
// new row and stamps EffectiveTo on the old one, which is the only later write: the question
// is always *which plan was in force when that version shipped*, and only the history answers
// it (§22.6.1).
type ChangePlan struct {
	ID      string `json:"id"`
	ModelID string `json:"modelId"`
	// Ref is the submission or clearance identifier — a 510(k) number, for instance.
	Ref     string `json:"ref,omitempty"`
	Summary string `json:"summary"`
	// AllowedVerdicts are the pre-authorised §11.4.1 verdicts. Never empty, never `unknown`.
	AllowedVerdicts []Verdict `json:"allowedVerdicts"`
	// AllowedMethods are the pre-authorised derived_from methods (§11.3.6). Nil means
	// unconstrained; an empty list is refused at write, since it would mean "no declared
	// method may ship" and is far likelier a mistake than a policy.
	AllowedMethods []string `json:"allowedMethods,omitempty"`
	// ProtocolArtifactID is the PCCP document itself, a DOC artifact on one of the model's
	// versions. No foreign key, for the §17.5.1 reason: deleting the document must neither
	// erase the plan nor rewrite what it rested on. Checked at write instead.
	ProtocolArtifactID string `json:"protocolArtifactId,omitempty"`
	EffectiveFrom      int64  `json:"effectiveFrom"`
	// EffectiveTo is nil while the plan is open, and set once, when it is superseded.
	EffectiveTo *int64 `json:"effectiveTo,omitempty"`
	DeclaredBy  string `json:"declaredBy,omitempty"`
	DeclaredAt  int64  `json:"declaredAt"`
}

// Covers reports whether the plan was in force at `at`. The window is half-open —
// [EffectiveFrom, EffectiveTo) — so at the instant of a supersession exactly one plan covers,
// which is what keeps "which plan was in force" single-valued (§22.7.3).
func (p *ChangePlan) Covers(at int64) bool {
	return at >= p.EffectiveFrom && (p.EffectiveTo == nil || at < *p.EffectiveTo)
}

// PlanInForce returns the plan covering `at`, or nil when none does. The overlap rule makes
// the answer unique; this is the one place it is chosen, so the per-version read and the
// queue cannot pick differently.
func PlanInForce(plans []*ChangePlan, at int64) *ChangePlan {
	for _, p := range plans {
		if p.Covers(at) {
			return p
		}
	}
	return nil
}

// plannable are the verdicts a plan may pre-authorise. `unknown` is not among them: it is the
// absence of a verdict, and "we cannot tell what changed" is never something a plan can have
// approved in advance (§22.4.1).
var plannable = []Verdict{VerdictIdentical, VerdictReweighted, VerdictRecast, VerdictRescaled, VerdictRearchitected}

// PlannableVerdicts lists the allowed values, for `details.allowedValues` (§22.7.3).
func PlannableVerdicts() []string { return stringsOf(plannable) }

// ValidateChangePlanEnvelope checks the envelope against the real verdict enum (§22.3) and
// normalises it: duplicates are dropped, first occurrence kept.
func ValidateChangePlanEnvelope(p *ChangePlan) *Error {
	if len(p.AllowedVerdicts) == 0 {
		return invalidWithAllowed("allowedVerdicts must name at least one verdict", "allowedVerdicts", PlannableVerdicts())
	}
	seen := map[Verdict]bool{}
	verdicts := make([]Verdict, 0, len(p.AllowedVerdicts))
	for _, v := range p.AllowedVerdicts {
		ok := false
		for _, k := range plannable {
			ok = ok || k == v
		}
		if !ok {
			return invalidWithAllowed("unknown verdict '"+string(v)+"' in allowedVerdicts", "allowedVerdicts", PlannableVerdicts())
		}
		if !seen[v] {
			seen[v] = true
			verdicts = append(verdicts, v)
		}
	}
	p.AllowedVerdicts = verdicts

	if p.AllowedMethods != nil {
		if len(p.AllowedMethods) == 0 {
			return invalidField("allowedMethods must be omitted, not empty, to leave methods unconstrained", "allowedMethods")
		}
		seenM := map[string]bool{}
		methods := make([]string, 0, len(p.AllowedMethods))
		for _, m := range p.AllowedMethods {
			if m == "" {
				return invalidField("allowedMethods must not contain an empty method", "allowedMethods")
			}
			if !seenM[m] {
				seenM[m] = true
				methods = append(methods, m)
			}
		}
		p.AllowedMethods = methods
	}
	return nil
}

// CheckPlanDeclaration is the overlap rule (§22.7.3), shared by every adapter so the rule
// exists once. `existing` is every plan the model has; `supersedes` names the open plan the
// new one replaces, or is empty.
//
// A new plan is open-ended from p.EffectiveFrom. It may not overlap any existing window,
// except the superseded plan's, which is closed at p.EffectiveFrom by the same write. Two
// live plans would make §22.4 ambiguous, and picking the newest would hide a data-entry error
// that matters — so this refuses rather than resolves.
//
// On success it returns the plan to close (nil when superseding nothing). The caller stamps it
// and inserts p in one transaction.
func CheckPlanDeclaration(existing []*ChangePlan, p *ChangePlan, supersedes string) (*ChangePlan, error) {
	var old *ChangePlan
	if supersedes != "" {
		for _, q := range existing {
			if q.ID == supersedes {
				old = q
			}
		}
		if old == nil {
			return nil, invalidField("plan '"+supersedes+"' not found on this model", "supersedes")
		}
		if old.EffectiveTo != nil {
			return nil, Precondition("plan '"+supersedes+"' has already been superseded",
				map[string]any{"reason": "already_superseded", "planId": old.ID})
		}
		// Superseding at or before the old plan's start would leave it an empty or inverted
		// window: that is a replacement of history, not a supersession.
		if p.EffectiveFrom <= old.EffectiveFrom {
			return nil, invalidField("effectiveFrom must be after the superseded plan's effectiveFrom", "effectiveFrom")
		}
	}
	for _, q := range existing {
		if q == old {
			continue
		}
		if q.EffectiveTo == nil || *q.EffectiveTo > p.EffectiveFrom {
			return nil, Precondition("effectiveFrom overlaps plan '"+q.ID+"'",
				map[string]any{"reason": "plan_overlap", "planId": q.ID})
		}
	}
	return old, nil
}
