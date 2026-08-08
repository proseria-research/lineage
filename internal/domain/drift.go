package domain

// Classification drift (§16.5). Worked out fresh on every read, from facts already stored,
// so the answer itself cannot go stale. Nothing here writes, repairs, or downgrades: the
// registry flags and stops (§16.2). A registry that quietly edited a legal field would be
// worse than one that never had the field.
//
// This is deliberately a pure function over supplied facts rather than a query. §16.5.1
// notes that `20.7` runs the same *shape* for model risk management — a list of independent
// triggers, evaluated on read, returning every reason that fired — while anchoring on a
// different row. Keeping the logic separate from the fetch is what would let that become
// one evaluator rather than two implementations that agree.

// DriftFacts are the stored facts the §16.5 clauses measure against, gathered by the caller
// so the predicate stays testable without a store.
//
// Each timestamp is epoch-millis, with **zero meaning "no such row"**. That is not a
// sentinel needing a guard: every clause asks whether the fact is *later* than
// ClassifiedAt, which is itself always non-zero on a real classification, so an absent fact
// compares false on its own.
type DriftFacts struct {
	// LatestVersionCreatedAt is the newest version's creation time, for clause 2.
	LatestVersionCreatedAt int64
	// LatestProductionUpdatedAt is the production version's last update, for clause 3.
	// Production is a singleton stage (§02.4), so there is at most one.
	LatestProductionUpdatedAt int64
	// LatestOpenReviewCreatedAt is the newest *open* modification-review item on the model,
	// for clause 4. Always zero until M16 populates it (`17.4`); a zero here means "no open
	// review", which is also the correct answer for an install that has none.
	LatestOpenReviewCreatedAt int64
}

// ClassificationStateOf returns the three-valued state (§16.4) and every reason that fired.
//
// c is nil when the model has no row for the regime being asked about. That is
// `unclassified`, which is **not a kind of stale** — someone filtering for one must not be
// handed the other, so it short-circuits before any clause runs. An explicit
// `unclassified` class is the same answer by the same argument: nobody has said yet, so
// there is no claim that could have gone out of date.
//
// Reasons come back in §16.5 clause order, and *all* of them, not the first — someone
// deciding whether to redo an assessment wants the whole picture.
func ClassificationStateOf(c *RiskClassification, f DriftFacts, now int64) (ClassificationState, []string) {
	if c == nil || c.EUSystemRiskClass == EUClassUnclassified {
		return ClassificationUnclassified, nil
	}

	var reasons []string

	// 1. The review date has passed.
	if c.ReviewDueAt != nil && *c.ReviewDueAt < now {
		reasons = append(reasons, StaleReviewDuePassed)
	}
	// 2. A version was published after the model was classified.
	if f.LatestVersionCreatedAt > c.ClassifiedAt {
		reasons = append(reasons, StaleVersionPublishedSince)
	}
	// 3. The production version changed after the model was classified.
	//
	// This reads updated_at, so editing the production version's description trips it. That
	// false positive is kept on purpose (§16.5): getting it exact would mean scanning the
	// audit log for `version.stage_changed` on every row, turning a list query into a
	// per-row audit scan. For a legal field, erring toward "take another look" is the right
	// direction, and the reason string tells the reader precisely what fired.
	if f.LatestProductionUpdatedAt > c.ClassifiedAt {
		reasons = append(reasons, StaleProductionChanged)
	}
	// 4. A modification-review item was opened after the model was classified (`17.4`).
	if f.LatestOpenReviewCreatedAt > c.ClassifiedAt {
		reasons = append(reasons, StaleDerivationSince)
	}

	if len(reasons) == 0 {
		return ClassificationCurrent, nil
	}
	return ClassificationStale, reasons
}
