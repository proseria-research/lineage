package domain

import "context"

// ComplianceStore persists risk classifications (§16.7.1) and, as M14–M16 land, the rest of
// the compliance record. It is a separate sub-interface of MetadataStore rather than more
// methods on the flat port, so the compliance surface stays nameable on its own.
//
// It is deliberately thin on reads. Staleness (§16.5) is computed at read time from facts
// other sub-interfaces already hold, so there is no stored `state` column and nothing here
// returns one — a store that could answer "is this stale?" would be a store that could
// answer it wrongly after the next publish.
type ComplianceStore interface {
	// PutClassification replaces this model's row for this regime whole (§16.8): PUT, not
	// PATCH, so an old basis can never sit underneath a brand-new class. Writing one regime
	// must leave every other regime's row — and crucially its ClassifiedAt anchor —
	// untouched (§16.3.2).
	PutClassification(ctx context.Context, c *RiskClassification) error
	// GetClassification returns one regime's row, or NotFound when this model has never
	// been classified under it. NotFound is the `unclassified` state (§16.4), not an error
	// the caller should surface as one.
	GetClassification(ctx context.Context, modelID string, regime Regime) (*RiskClassification, error)
	// ListClassifications returns every regime's row for a model, ordered by regime so the
	// response is stable across adapters and reruns.
	ListClassifications(ctx context.Context, modelID string) ([]*RiskClassification, error)
	// DriftFactsFor gathers the aggregates the §16.5 clauses measure against. It lives here
	// rather than on VersionStore because it is a compliance-shaped question that happens to
	// read model_version — and keeping it beside its only consumer is what lets the drift
	// predicate stay a pure function (§16.5.1).
	//
	// It is regime-independent: every clause compares against the *caller's* ClassifiedAt,
	// so one fetch serves every regime's row for a model.
	DriftFactsFor(ctx context.Context, modelID string) (DriftFacts, error)
	// ListInventory returns every model matching o's model-level filters and f's stored-enum
	// filters, each with its regime row and drift facts (§16.8.2).
	//
	// **Unpaginated on purpose.** The `classificationState` filter cannot be a SQL predicate
	// — the state is computed, not stored — so the caller filters on it in Go and pages the
	// survivors. Paging here instead would hand back short pages, or pages that skip
	// matching models entirely. This mirrors how ListModels already treats labels it cannot
	// push down.
	ListInventory(ctx context.Context, o ListOptions, f ClassificationFilter) ([]*ModelInventoryRow, error)
}

// ModelInventoryRow is one model with everything needed to state its classification status
// (§16.8.2): the model, its row for the regime asked about (nil when it has none), and the
// drift facts to measure that row against.
//
// The store returns facts rather than a state. Computing the state here would mean writing
// the §16.5 predicate a second time in SQL, and two implementations of a legal predicate in
// two languages is exactly the arrangement that eventually disagrees. The caller runs
// ClassificationStateOf, the same function every other read path uses.
type ModelInventoryRow struct {
	Model          *Model
	Classification *RiskClassification // nil ⇒ unclassified under this regime
	Facts          DriftFacts
}

// ClassificationFilter narrows an inventory query to stored enum values. The computed
// state is deliberately absent: it is not a column, so it cannot be a SQL predicate.
type ClassificationFilter struct {
	Regime            Regime
	EUSystemRiskClass EUSystemRiskClass
	EUGpaiTier        EUGpaiTier
}

// Active reports whether any stored-column filter is set.
func (f ClassificationFilter) Active() bool {
	return f.EUSystemRiskClass != "" || f.EUGpaiTier != ""
}
