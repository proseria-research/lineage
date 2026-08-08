package domain

// Legal hold and the retention floor (§19.3, §19.4).
//
// Every mechanism here is a **refusal**. Nothing in this file deletes, expires, or
// garbage-collects, and nothing ever will: §19.1 is explicit that a registry which
// garbage-collected on a retention timer would be a liability rather than a feature. The
// floor does not remove a record when it lapses; it stops removing one before it does.
//
// Like §16.5's drift predicate, the guard is a pure function over supplied facts rather than
// a query. The caller gathers what the subject's deletion would destroy and asks; the answer
// is a *Error ready to return, or nil. That keeps one decision in one place — the model path
// and the version path must refuse identically, and the only way to guarantee that is for
// them to call the same function.

// millisPerDay converts the configured floor, which is in days because that is how a
// retention obligation is written down, to the epoch-millis the rows carry.
const millisPerDay int64 = 24 * 60 * 60 * 1000

// Refusal reasons carried in details.reason (§19.7.4). The error *code* stays
// failed_precondition for all of them — §03.9's table is deliberately small, and the
// specificity lives in details so that adding a reason never widens the code vocabulary.
const (
	RefusedLegalHold      = "legal_hold"
	RefusedRetentionFloor = "retention_floor"
	RefusedNotHeld        = "not_held"
)

// Subject types, as they appear in audit events and on the hold API's paths.
const (
	SubjectModel   = "model"
	SubjectVersion = "model_version"
)

// Hold is a live legal hold and its provenance (§19.7.1).
//
// **A nil *Hold is "not held".** There is no Held bool: a boolean beside a timestamp is two
// representations of one fact, and two representations eventually disagree. The pointer
// cannot be half-set.
//
// The hold carries when it was set and who set it — and nothing else. The *reason*
// ("Regulator inquiry REF-2026-118") goes on the audit event, not here. A reason on the row
// would be overwritten by the next hold; on the event it is permanent, which is what someone
// reconstructing the matter years later needs. Releasing clears the row for the same reason:
// current state lives on the subject, history lives in the trail.
//
// HeldBy is the **actor** who set it, consistent with every other attribution in the system
// (§00.2.4). When a hold is inherited rather than the subject's own, the holder is named
// separately — see DeleteGuard.HeldSubject — rather than overloaded into this field.
type Hold struct {
	HeldSince int64  `json:"heldSince"`
	HeldBy    string `json:"heldBy,omitempty"`
}

// RetentionConfig is the configured floor (§19.4), in days.
//
// Zero disables a floor and is a **real choice**, not an unset value — a dev install that
// wants to delete freely says 0 and means it. The chart's default is 3650 (Art. 18's ten
// years), so an operator who has expressed no opinion gets the floor, and one who has
// expressed the opinion "none" is not second-guessed.
type RetentionConfig struct {
	// MinAuditAgeDays is the floor on audit history. Nothing in core deletes an audit event
	// — §02.5 invariant 5 already retains them past their subject — so this is not enforced
	// by any guard. It is the number an install *reports* (`/healthz`, §19.4) so a filing can
	// cite the floor the registry actually ran under. It is here rather than in a config
	// struct because it is part of the same promise.
	MinAuditAgeDays int `json:"minAuditAgeDays"`
	// MinArchivedVersionDays is the floor DELETE enforces, on both models and versions.
	MinArchivedVersionDays int `json:"minArchivedVersionDays"`
}

// DefaultRetention is Art. 18's ten years on both floors (§19.4).
var DefaultRetention = RetentionConfig{MinAuditAgeDays: 3650, MinArchivedVersionDays: 3650}

// Validate rejects a negative floor. A negative is not "extra disabled" — it is a typo or a
// unit mistake, and silently reading it as 0 would turn a misconfiguration into a registry
// that deletes evidence on request.
func (c RetentionConfig) Validate() *Error {
	if c.MinAuditAgeDays < 0 {
		return Invalid("retention.minAuditAgeDays must not be negative (0 disables the floor)")
	}
	if c.MinArchivedVersionDays < 0 {
		return Invalid("retention.minArchivedVersionDays must not be negative (0 disables the floor)")
	}
	return nil
}

// DeleteGuard is what a deletion would destroy. The store gathers it (RetentionStore), since
// resolving it means walking the model/version tree; this file only decides what to do about
// it. That split is what keeps the predicate a pure function.
type DeleteGuard struct {
	// Hold is the **effective** hold — the subject's own, or one it inherits — and nil when
	// nothing holds this delete.
	Hold *Hold
	// HeldSubject names the holder when the hold is not the subject's own —
	// "model/fraud-detector", "version/fraud-detector@3" — and is empty when it is. §19.3.1
	// requires the model to be identified when a version is refused under it: without it the
	// caller is told the delete is blocked and given nothing to release.
	HeldSubject string
	// NewestCreatedAt is the creation time of the **youngest record this delete destroys**,
	// in epoch millis — not the subject's own.
	//
	// The distinction matters for a model. A model is created before all of its versions, so
	// measuring the model's own age would let a decade-old model be deleted the day after it
	// published a new version, cascading away a record one day into a ten-year floor. The
	// youngest record is the one the floor is protecting.
	//
	// Zero would read as 1970 and clear any floor. No real row carries it — every subject
	// gets CreatedAt at insert — so this is documented rather than guarded.
	NewestCreatedAt int64
}

// CheckDeletable returns the refusal a DELETE should surface, or nil to proceed (§19.3.1).
//
// Order follows §19.3's flowchart: hold first, floor second. It matters when both apply —
// the caller should hear "there is a legal hold on this", which is a decision someone made
// deliberately and can release, rather than "it is too young", which merely expires.
//
// **force does not appear here, and must not.** §03.4's ?force=true overrides the
// production-version guard, which protects an operator from their own mistake. A hold
// protects evidence from the operator, and a flag that clears it is not a hold. The
// production guard and this one are independent checks for exactly that reason.
func CheckDeletable(g DeleteGuard, cfg RetentionConfig, now int64) *Error {
	if g.Hold != nil {
		details := map[string]any{
			"reason":    RefusedLegalHold,
			"heldSince": g.Hold.HeldSince,
			"heldBy":    g.Hold.HeldBy,
		}
		msg := "subject is under legal hold; delete refused"
		if g.HeldSubject != "" {
			details["heldSubject"] = g.HeldSubject
			msg = "ancestor '" + g.HeldSubject + "' is under legal hold; delete refused"
		}
		return Precondition(msg, details)
	}

	if cfg.MinArchivedVersionDays > 0 {
		age := (now - g.NewestCreatedAt) / millisPerDay
		if age < int64(cfg.MinArchivedVersionDays) {
			return Precondition("subject is inside the configured retention floor; delete refused",
				map[string]any{
					"reason":    RefusedRetentionFloor,
					"floorDays": cfg.MinArchivedVersionDays,
					"ageDays":   age,
				})
		}
	}

	return nil
}
