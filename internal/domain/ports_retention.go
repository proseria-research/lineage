package domain

import "context"

// RetentionStore persists legal hold and answers the deletion guard (§19.3, §19.4). Like
// ComplianceStore it is a named sub-interface of MetadataStore rather than more methods on a
// flat port, so the evidence-survival surface is nameable on its own — and a background
// sweeper or a decorator can depend on these two methods instead of all fifty.
//
// Reading a hold is deliberately absent. A subject's own hold rides on the subject
// (Model.LegalHold, ModelVersion.LegalHold), so every existing GET already returns it and
// the console needs no second call (§19.8). What is here is the write, and the one question
// no single row can answer.
type RetentionStore interface {
	// SetHold sets or clears a subject's hold; a nil h releases it. subjectType is
	// SubjectModel or SubjectVersion, subjectID the row's id.
	//
	// It is a targeted write rather than a field on UpdateModel, for two reasons. A hold must
	// not be settable as a side effect of a metadata PATCH — §19.3.1 makes hold.set and
	// hold.release explicit, separately-audited actions — and a read-modify-write of the whole
	// entity would race a concurrent PATCH and silently restore stale fields alongside the
	// hold.
	//
	// Idempotent: setting a held subject, or releasing an unheld one, is not an error at this
	// layer. §19.7.4's not_held refusal is a core decision made against the subject the caller
	// already fetched, so the store is not asked to distinguish "changed nothing" from
	// "matched nothing" — a distinction SQL reports inconsistently across dialects.
	SetHold(ctx context.Context, subjectType, subjectID string, h *Hold) error

	// DeleteGuardFor gathers everything CheckDeletable needs about deleting this subject:
	// the effective hold, who holds it, and the youngest record the delete would destroy.
	//
	// It lives in the store because answering it means walking the model/version tree, and
	// hold inheritance runs **both ways** for destruction:
	//
	//   - down (§19.3.1): a hold on a model refuses deleting its versions.
	//   - up: a hold on any version refuses deleting the model, because that cascade would
	//     destroy the held version. §19 states only the downward rule, but the argument for
	//     it — a delete must not destroy evidence someone put a hold on — applies unchanged
	//     in this direction, and a model delete is the *larger* destruction of the two.
	//
	// NotFound if the subject does not exist.
	DeleteGuardFor(ctx context.Context, subjectType, subjectID string) (DeleteGuard, error)
}
