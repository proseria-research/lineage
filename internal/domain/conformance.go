package domain

// ---- Conformance (§22.4) ----

// Conformance is §22.4's answer for one derivation. Computed on read and never stored, the
// §16.5 drift rule: a stored value would be a second truth that disagrees with the hashes the
// moment either side moves.
type Conformance string

const (
	// ConformanceNoPlan: the model has no plan at all. Never in the queue, which only scans
	// planned models; the per-version read says it.
	ConformanceNoPlan Conformance = "no_plan"
	// ConformanceUncovered: the model has plans, but none was in force when the version was
	// published. Not a violation — the version is simply not governed (§22.4.2).
	ConformanceUncovered Conformance = "uncovered"
	// ConformanceUndetermined: the verdict is `unknown`. Queued, not passed (§22.4.1).
	ConformanceUndetermined Conformance = "undetermined"
	ConformanceOutsidePlan  Conformance = "outside_plan"
	ConformanceWithinPlan   Conformance = "within_plan"
)

var conformances = []Conformance{
	ConformanceNoPlan, ConformanceUncovered, ConformanceUndetermined, ConformanceOutsidePlan, ConformanceWithinPlan,
}

func ValidConformance(c Conformance) bool {
	for _, k := range conformances {
		if k == c {
			return true
		}
	}
	return false
}

// Conformances lists the allowed values, for `details.allowedValues`.
func Conformances() []string { return stringsOf(conformances) }

// Reasons a derivation is outside its plan. Both are returned when both hold, the §20.7
// "every reason" rule, so a reader fixing one does not discover the other afterwards.
const (
	OutsideVerdict = "verdict_not_allowed"
	OutsideMethod  = "method_not_allowed"
)

// ConformanceOf is the §22.4 predicate: a **pure function over supplied facts**, the shape of
// ReviewEligible and MRMStateOf. One implementation serves the per-version read, the queue,
// the console and the tests.
//
// plans is every plan the model has; publishedAt is the version's created_at; verdict is the
// §11.4 verdict across the derivation; method is the edge's declared method, "" when none.
// It returns the plan in force (nil for no_plan and uncovered) and, for outside_plan, why.
func ConformanceOf(plans []*ChangePlan, publishedAt int64, verdict Verdict, method string) (Conformance, *ChangePlan, []string) {
	if len(plans) == 0 {
		return ConformanceNoPlan, nil, nil
	}
	p := PlanInForce(plans, publishedAt)
	if p == nil {
		return ConformanceUncovered, nil, nil
	}
	// Any `unknown`, not only a missing weights_hash: a missing topology or shape hash leaves
	// the verdict just as open. A verdict the present hashes *do* settle — a changed topology
	// with no weights hash is still `rearchitected` — is judged normally.
	if verdict == "" || verdict == VerdictUnknown {
		return ConformanceUndetermined, p, nil
	}
	var reasons []string
	allowed := false
	for _, v := range p.AllowedVerdicts {
		allowed = allowed || v == verdict
	}
	if !allowed {
		reasons = append(reasons, OutsideVerdict)
	}
	if method != "" && p.AllowedMethods != nil {
		ok := false
		for _, m := range p.AllowedMethods {
			ok = ok || m == method
		}
		if !ok {
			reasons = append(reasons, OutsideMethod)
		}
	}
	if len(reasons) > 0 {
		return ConformanceOutsidePlan, p, reasons
	}
	return ConformanceWithinPlan, p, nil
}

// PlanRef names the plan a row was judged against (§22.7.2).
type PlanRef struct {
	ID  string `json:"id"`
	Ref string `json:"ref,omitempty"`
}

// ConformanceItem is one derivation judged against its plan (§22.7.2): what changed, what the
// plan allowed, and the hashes that decided it.
type ConformanceItem struct {
	Model     string `json:"model"`
	Version   string `json:"version"`
	VersionID string `json:"versionId"`
	// EdgeID is the derived_from edge judged. A version with two predecessors (a merge) is
	// two rows, one per edge — §22.4 is defined over an edge, not a version.
	EdgeID         string      `json:"edgeId"`
	DerivedFrom    *ReviewSide `json:"derivedFrom,omitempty"`
	DerivedFromRef string      `json:"derivedFromRef,omitempty"`

	Plan        *PlanRef    `json:"plan,omitempty"`
	Conformance Conformance `json:"conformance"`
	// Reasons names why an outside_plan row is outside: the verdict, the method, or both.
	Reasons []string `json:"reasons,omitempty"`

	Verdict    Verdict   `json:"verdict"`
	Candidates []Verdict `json:"candidates,omitempty"`
	Missing    []string  `json:"missing,omitempty"`
	// AllowedVerdicts and AllowedMethods are the envelope the row was judged against,
	// repeated so a reader sees both sides of the comparison in one place.
	AllowedVerdicts []Verdict `json:"allowedVerdicts,omitempty"`
	AllowedMethods  []string  `json:"allowedMethods,omitempty"`
	DeclaredMethod  string    `json:"declaredMethod,omitempty"`

	// Hashes is the full per-level comparison; Basis its two-list projection — which side
	// supplied which levels. The §17.6.2 pair exactly, so the two queues read alike.
	Hashes map[string]HashCmp `json:"hashes"`
	Basis  ReviewBasis        `json:"basis"`

	PublishedAt int64 `json:"publishedAt"`
}
