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
}
