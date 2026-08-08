package domain

import "context"

// AttestationStore backs Merkle epoch sealing (§19.5): the sealer's window scan, the
// verifier's recompute, and the inclusion-proof lookup.
//
// It is append-only in the strongest sense the port can express — there is no UpdateEpoch and
// no DeleteEpoch, because a sealed epoch is immutable by construction (§19.6.1): re-sealing
// would be indistinguishable from tampering, so the capability simply does not exist.
type AttestationStore interface {
	// SealableEpochs returns, ascending, every epoch that has attested rows but no seal and
	// whose index is strictly below notAfter. The caller passes the first epoch still open
	// (adjusted for grace), so a window that might still accept a commit is never sealed.
	//
	// It exists as one query rather than "list epochs, then check each" because the answer is
	// a set difference the database can do in one pass, and the sealer runs it every interval
	// forever.
	SealableEpochs(ctx context.Context, notAfter int64, limit int) ([]int64, error)

	// AuditEventsInEpoch returns every row in an epoch, unordered — ordering is
	// SortAuditEvents' job, so the two engines cannot disagree via collation (§19.5.1).
	AuditEventsInEpoch(ctx context.Context, epoch int64) ([]*AuditEvent, error)

	// AppendEpoch writes one seal. It must fail rather than overwrite when the epoch is
	// already sealed: a second seal for a window is either a duplicated sealer or a rewrite,
	// and both want a loud failure.
	AppendEpoch(ctx context.Context, e *AuditEpoch) error

	// LatestSealedEpoch returns the highest sealed epoch, for prev_root chaining. NotFound
	// when nothing has been sealed yet, which is the start of the chain (§19.6.1).
	LatestSealedEpoch(ctx context.Context) (*AuditEpoch, error)

	// ListEpochs returns sealed epochs in [from, to] ascending; a zero `to` means unbounded.
	// This is :verify's scan, so it is ordered by epoch and not paginated — the chain has to
	// be walked in sequence for prev_root to mean anything.
	ListEpochs(ctx context.Context, from, to int64) ([]*AuditEpoch, error)

	// GetAuditEvent fetches one row by id, for :proof. NotFound if it does not exist.
	GetAuditEvent(ctx context.Context, id string) (*AuditEvent, error)

	// FirstAttestedEpoch returns the lowest epoch on any attested row, and false when there
	// are none.
	//
	// This is where `attestationStartedAt` comes from (§19.5.4). Deriving it from the rows
	// rather than storing a timestamp is deliberate: a stored "enabled at" would be a claim,
	// and this is a fact — it is the earliest window any row was actually covered by, which
	// is the thing a reader of :verify needs to know the covered range of.
	FirstAttestedEpoch(ctx context.Context) (int64, bool, error)
}
