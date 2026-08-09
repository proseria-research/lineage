package domain

import "context"

// ReviewStore persists modification reviews and gathers what the §17.4 queue is computed from.
// Like ComplianceStore and RetentionStore it is a named sub-interface of MetadataStore, so the
// Art. 25 surface is nameable on its own.
//
// Nothing here answers "is this item open?". Openness depends on the §11.4 verdict, which is a
// lookup over four hashes rather than a column, so the store returns the hashes and
// ReviewEligible decides — the same arrangement ComplianceStore uses for staleness, and for
// the same reason: a store that could answer the question would be a store that could answer
// it differently from the verifier.
type ReviewStore interface {
	// CreateReview appends a row. Reviews are never updated or deleted (§17.4).
	CreateReview(ctx context.Context, r *ModificationReview) error

	// ListReviews returns a version's reviews, newest first, across every edge.
	ListReviews(ctx context.Context, versionID string) ([]*ModificationReview, error)

	// ListDerivations returns every `derived_from` edge whose source version belongs to a
	// model classified under regime, with both sides' hash ladders, the model's declared
	// enums, and the latest review for the (version, edge) pair.
	//
	// The classification join is **inner**, which is condition 3 of §17.4 expressed as a
	// join rather than a filter: a model nobody has classified cannot be in the queue, and
	// making that structural keeps the scan bounded to classified models on an install where
	// most models are not.
	//
	// modelID narrows to one model; empty means every model. Ordered newest edge first.
	//
	// **Unpaginated on purpose**, exactly as ListInventory is (§16.8.2): the verdict is not a
	// column, so `status` cannot be a SQL predicate and the caller pages the survivors.
	ListDerivations(ctx context.Context, regime Regime, modelID string) ([]*DerivationRow, error)
}

// DerivationRow is one `derived_from` edge with everything §17.4 and §17.6.2 need: no more, so
// the store is not quietly deciding what the queue means, and no less, so the caller never has
// to go back per row.
type DerivationRow struct {
	ModelID   string
	Model     string
	Version   string
	VersionID string
	Edge      *LineageEdge

	// ParentModel and ParentVersion name the other end when Edge.DstID points at a version
	// here; both empty when the edge points at an external ref (Edge.DstRef).
	ParentModel   string
	ParentVersion string

	// FromHashes is the parent's ladder, ToHashes this version's. A zero Hashes means no
	// insight was reported for that side, which Classify already reads as `unknown` — there
	// is no separate "absent" flag to keep in step.
	FromHashes Hashes
	ToHashes   Hashes

	EUSystemRiskClass EUSystemRiskClass
	EUGpaiTier        EUGpaiTier

	// LatestReview is the newest row for (VersionID, Edge.ID), or nil when nobody has
	// reviewed this pair — which is condition 4 of §17.4.
	LatestReview *ModificationReview
}
