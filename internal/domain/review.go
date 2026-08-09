package domain

// Modification review (§17). A `derived_from` edge on a high-risk or GPAI model may have made
// whoever created it the model's *provider* in law (Art. 25). The registry routes the §11.4
// verdict to a human and records what they decided. It never decides itself: Art. 3(23) turns
// on the system, its context and its documentation, none of which is stored here, so asserting
// "substantial modification" from a weights hash is exactly the fabricated completeness §15.3.2
// rejects (§17.3).

// ---- Outcome (§17.5.1) ----

// ReviewOutcome is what a human concluded.
type ReviewOutcome string

const (
	OutcomeNotSubstantial ReviewOutcome = "not_substantial"
	OutcomeSubstantial    ReviewOutcome = "substantial"
	// OutcomeUndetermined is a real outcome, not a placeholder: a reviewer who needs counsel
	// must be able to close the loop on "looked at it, cannot resolve yet" rather than leave
	// the item indistinguishable from one nobody opened (§17.5.1).
	OutcomeUndetermined ReviewOutcome = "undetermined"
)

var reviewOutcomes = []ReviewOutcome{OutcomeNotSubstantial, OutcomeSubstantial, OutcomeUndetermined}

func ValidReviewOutcome(o ReviewOutcome) bool {
	for _, k := range reviewOutcomes {
		if k == o {
			return true
		}
	}
	return false
}

// ReviewOutcomes lists the allowed values, for `details.allowedValues` (§17.6.3).
func ReviewOutcomes() []string { return stringsOf(reviewOutcomes) }

// ---- Entity (§17.5.1) ----

// ModificationReview is one human's judgement on one derivation.
//
// Rows are **append-only**, like Evaluation (§11.7): a re-review is a new row and the queue
// keys on the latest per (VersionID, EdgeID). There is no update and no delete, because the
// value of the record is that it says what somebody concluded at a moment, not what the
// current opinion is.
type ModificationReview struct {
	ID        string `json:"id"`
	VersionID string `json:"versionId"`
	// EdgeID is the derived_from edge reviewed. There is deliberately **no foreign key** to
	// lineage_edge: deleting the edge takes the item out of the queue, but must not erase the
	// record that a human looked at it.
	EdgeID string `json:"edgeId"`
	// VerdictAtReview freezes the §11.4 verdict as it stood when the review was recorded, so a
	// producer submitting a weights_hash afterwards cannot rewrite what a reviewer actually
	// saw (§17.4). Server-set; a client-supplied value is rejected (§17.6.1).
	VerdictAtReview Verdict       `json:"verdictAtReview"`
	Outcome         ReviewOutcome `json:"outcome"`
	Note            string        `json:"note,omitempty"`
	ReviewedBy      string        `json:"reviewedBy,omitempty"`
	ReviewedAt      int64         `json:"reviewedAt"`
}

// ---- The queue (§17.4) ----

// ReviewStatus is an item's position in the queue, not a stored column: it is condition 4
// evaluated at read time.
type ReviewStatus string

const (
	ReviewOpen   ReviewStatus = "open"
	ReviewClosed ReviewStatus = "closed"
)

func ValidReviewStatus(s ReviewStatus) bool { return s == ReviewOpen || s == ReviewClosed }

// ReviewEligible reports §17.4 conditions 2 and 3 — the technical delta is not nothing, and
// the model's declared class puts Art. 25 in play.
//
// Condition 1 is structural (the caller only ever holds derived_from edges) and condition 4
// decides open from closed, so those two are not here. What is here is a **pure function over
// declared facts**, the same shape as §16.5's drift predicate: one implementation serves the
// queue, the inventory's clause 4, and the tests, so none of them can disagree.
//
// `unknown` is eligible, deliberately. "We cannot tell what changed" is precisely the case
// that wants human eyes — excluding it would make a missing weights_hash look like a clean
// bill of health (§17.4).
func ReviewEligible(v Verdict, class EUSystemRiskClass, tier EUGpaiTier) bool {
	if v == VerdictIdentical {
		return false
	}
	// A derived GPAI can pick up its own Art. 53 duties, so the tier qualifies on its own —
	// it is not a weaker form of the system class.
	return class.IsHighRisk() || (tier != "" && tier != EUGpaiNone)
}

// ReviewBasis records which hash levels each side actually supplied, so a partial verdict is
// identifiable as partial rather than read as confident (§17.6.2, §11.6.2).
//
// §17.6.2 sketched this as one flat list. Two are needed: the whole question `basis` answers
// is which side was missing what, and a single list cannot say it.
type ReviewBasis struct {
	FromHashes []string `json:"fromHashes,omitempty"`
	ToHashes   []string `json:"toHashes,omitempty"`
}

// ReviewItem is one derivation in the queue (§17.6.2): what changed, what the modifier said
// they were doing, and whether anyone has looked at it.
type ReviewItem struct {
	Model     string `json:"model"`
	Version   string `json:"version"`
	VersionID string `json:"versionId"`
	EdgeID    string `json:"edgeId"`

	// DerivedFrom names the other end when the edge points at a version in this registry.
	// DerivedFromRef carries the external reference when it does not — a fine-tune of a
	// third-party model is the Art. 25 case, and it is the one where no hashes reach.
	DerivedFrom    *ReviewSide `json:"derivedFrom,omitempty"`
	DerivedFromRef string      `json:"derivedFromRef,omitempty"`

	Verdict    Verdict   `json:"verdict"`
	Candidates []Verdict `json:"candidates,omitempty"`
	Missing    []string  `json:"missing,omitempty"`
	// Hashes is the full ladder, per level, both sides. Basis is its two-list projection:
	// the compact "which side was missing what" a reader cites. Carrying both mirrors the
	// §11.6.2 diff response exactly, and the console needs the values — §17.7's side-by-side
	// fingerprints are drawn from them, with the changed rings emphasised (§12.6.2).
	Hashes map[string]HashCmp `json:"hashes"`
	Basis  ReviewBasis        `json:"basis"`
	// DeclaredMethod is the edge's properties.method (§11.3.6) — the intent, carried beside
	// the measurement so a reviewer can notice when the two disagree (§17.3).
	DeclaredMethod string `json:"declaredMethod,omitempty"`

	EUSystemRiskClass EUSystemRiskClass `json:"euSystemRiskClass,omitempty"`
	EUGpaiTier        EUGpaiTier        `json:"euGpaiTier,omitempty"`
	EdgeCreatedAt     int64             `json:"edgeCreatedAt"`

	Status ReviewStatus `json:"status"`
	// Review is the latest row for the pair, present only on a closed item. When its
	// VerdictAtReview differs from Verdict above, a producer submitted a hash after the
	// review — worth seeing, which is why both are returned (§17.7).
	Review *ModificationReview `json:"review,omitempty"`
}

// ReviewSide identifies a version by name.
type ReviewSide struct {
	Model   string `json:"model"`
	Version string `json:"version"`
}
