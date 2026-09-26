package domain

import "context"

// ChangePlanStore persists change control plans (§22.6) and gathers what §22.4 is computed
// from. A named sub-interface of MetadataStore, like ReviewStore.
//
// Nothing here answers "is this version within its plan?". The verdict is a lookup over four
// hashes, not a column, so the store returns plans and hashes and ConformanceOf decides — the
// ReviewStore arrangement, for the same reason.
type ChangePlanStore interface {
	// CreateChangePlan appends p. When supersedes is non-empty, that plan's effective_to is
	// stamped with p.EffectiveFrom in the same transaction. The overlap rule is
	// CheckPlanDeclaration, applied under the model's write lock, so two concurrent
	// declarations cannot both pass it: FailedPrecondition `plan_overlap` or
	// `already_superseded`, InvalidArgument for a bad `supersedes`.
	CreateChangePlan(ctx context.Context, p *ChangePlan, supersedes string) error

	// ListChangePlans returns a model's plans, newest effective_from first. An empty modelID
	// returns every model's plans in the same order, for the queue.
	ListChangePlans(ctx context.Context, modelID string) ([]*ChangePlan, error)

	// ListPlanDerivations returns `derived_from` edges with both sides' hash ladders — the
	// facts §22.4 needs beyond the plans themselves.
	//
	// Unnarrowed (modelID empty), it scans only models with at least one plan: the ListDerivations
	// inner-join argument, since on most installs most models have none. Narrowed to a model,
	// it returns that model's edges whether or not it has a plan, so the per-version read can
	// say `no_plan`. versionID narrows further. Ordered newest version first, then edge id.
	//
	// **Unpaginated**, like ListDerivations: conformance is computed, so the caller filters on
	// it and pages the survivors.
	ListPlanDerivations(ctx context.Context, modelID, versionID string) ([]*PlanDerivationRow, error)
}

// PlanDerivationRow is one `derived_from` edge with everything §22.4 and §22.7.2 need.
type PlanDerivationRow struct {
	ModelID   string
	Model     string
	Version   string
	VersionID string
	// PublishedAt is the version's created_at — the instant §22.4 asks which plan covered.
	PublishedAt int64
	Edge        *LineageEdge

	// ParentModel and ParentVersion name the other end when the edge points at a version
	// here; both empty for an external ref.
	ParentModel   string
	ParentVersion string

	// Zero Hashes means no insight on that side, which Classify reads as `unknown`.
	FromHashes Hashes
	ToHashes   Hashes
}
