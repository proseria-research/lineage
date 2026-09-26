package domain

import "context"

// ValidationStore persists model-risk validations (§20.8.2) and gathers what the §20.7 state
// is computed from. Like ReviewStore it is a named sub-interface of MetadataStore.
//
// As with ComplianceStore, nothing here answers "is this stale?": the store returns facts and
// MRMStateOf decides, so the predicate exists once.
type ValidationStore interface {
	// CreateValidation appends a row. Validations are never overwritten (§20.5).
	CreateValidation(ctx context.Context, v *Validation) error
	// ListValidations returns a version's validations, newest first. The first is the
	// current answer (§20.5).
	ListValidations(ctx context.Context, versionID string) ([]*Validation, error)
	// ClearValidationConditions sets conditions_cleared_at, once. It is the one later write
	// §20.8.2 allows, and it returns FailedPrecondition when the row is already cleared, so a
	// second clear cannot move the timestamp. NotFound when id is not a validation of
	// versionID.
	ClearValidationConditions(ctx context.Context, versionID, id string, at int64) error

	// ListMRMInventory returns every model matching o's model-level filters and f, each with
	// its `mrm` row (nil when it has none) and the §20.7 facts measured against its subject
	// version (§20.9.2).
	//
	// Unpaginated for the ListInventory reason: `mrmState` is computed, so the caller
	// filters on it in Go and pages the survivors.
	ListMRMInventory(ctx context.Context, o ListOptions, f MRMFilter) ([]*MRMInventoryRow, error)
}

// MRMFilter narrows ListMRMInventory to stored values. The computed state is absent for the
// ClassificationFilter reason.
type MRMFilter struct {
	Tier MRMTier
	// ModelID narrows to one model — the single-model read (GET …/classifications/mrm) goes
	// through the same statement as the inventory, so the subject is chosen in one place.
	ModelID string
}

// MRMInventoryRow is one model with everything needed to state its §20.7 status.
type MRMInventoryRow struct {
	Model          *Model
	Classification *RiskClassification // nil ⇒ untiered
	Facts          MRMFacts
}
