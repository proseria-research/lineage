package domain

// Model-risk state (§20.7). The §16.5 *shape*, not its query: a list of independent triggers,
// evaluated on read, returning every reason that fired, repairing nothing. It anchors on a
// validation per version where §16.5 anchors on a classification per model, and its ladder
// has four rungs rather than three (§16.5.1).
//
// Like ClassificationStateOf it is a pure function over supplied facts, so the inventory
// filter and every single-row read call the same code and cannot disagree.

// ---- State (§20.7) ----

// The two MRM-only rungs of the ladder. `stale` and `current` are the shared constants.
const (
	MRMStateUntiered    ClassificationState = "untiered"
	MRMStateUnvalidated ClassificationState = "unvalidated"
)

// MRMStates lists the ladder in order, for `details.allowedValues`.
func MRMStates() []string {
	return []string{string(MRMStateUntiered), string(MRMStateUnvalidated), string(ClassificationStale), string(ClassificationCurrent)}
}

func ValidMRMState(s ClassificationState) bool {
	for _, k := range MRMStates() {
		if k == string(s) {
			return true
		}
	}
	return false
}

// Stale reasons for §20.7, one per clause. Clause 2 shares its string with §16.5 clause 2
// because it is the same fact — a version was published after the anchor — measured against
// a different anchor.
const (
	StaleValidationExpired       = "validation_expired"
	StaleUnmonitoredInProduction = "unmonitored_in_production"
	StaleConditionsOutstanding   = "conditions_outstanding"
)

// MRMStateOf returns the four-valued §20.7 state and every reason that fired.
//
// The ladder is decided top-down, and the first two rungs short-circuit before any clause
// runs, for the §16.4 reason: "nobody tiered it" and "nobody validated it" are not kinds of
// "out of date", and a reader filtering for one must not be handed the others.
//
// `undetermined` sits with `rejected` on the `unvalidated` rung. §20.7 named only `rejected`,
// but an undetermined validation is someone saying they cannot yet conclude the version is fit
// — reading that as `current` would tell an examiner the model is covered when the validator
// said it is not (§20.7, as corrected).
//
// `out_of_scope` runs the same ladder. A model outside the framework usually reads
// `unvalidated`, which is true, and the tier beside it says why that is fine.
func MRMStateOf(c *RiskClassification, f MRMFacts, now int64) (ClassificationState, []string) {
	if c == nil || c.MRMTier == "" || c.MRMTier == MRMUntiered {
		return MRMStateUntiered, nil
	}
	val := f.Validation
	if f.Subject == nil || val == nil || !val.Outcome.Relied() {
		return MRMStateUnvalidated, nil
	}

	var reasons []string

	// 1. The validation has expired. Strict: a validation valid until now is still valid.
	if val.ValidUntil != nil && *val.ValidUntil < now {
		reasons = append(reasons, StaleValidationExpired)
	}
	// 2. A version was published after the validation. Strict for the §16.5 reason: a publish
	// in the same millisecond is not *after* it.
	if f.LatestVersionCreatedAt > val.ValidatedAt {
		reasons = append(reasons, StaleVersionPublishedSince)
	}
	// 3. In production with no evaluation since it got there — the ongoing-monitoring failure
	// all three regimes are written to catch (§20.7). An evaluation run in the same
	// millisecond as the promotion does not count as monitoring *since* it.
	if f.Subject.Stage == StageProduction && f.LatestEvaluationRunAt <= f.Subject.StageChangedAt {
		reasons = append(reasons, StaleUnmonitoredInProduction)
	}
	// 4. Conditional, and the conditions have not been cleared.
	if val.Outcome == ValidationConditional && val.ConditionsClearedAt == nil {
		reasons = append(reasons, StaleConditionsOutstanding)
	}

	if len(reasons) == 0 {
		return ClassificationCurrent, nil
	}
	return ClassificationStale, reasons
}
