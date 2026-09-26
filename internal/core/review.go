package core

import (
	"context"
	"encoding/json"

	"github.com/proseria-research/lineage/internal/domain"
)

// Modification review (§17). A `derived_from` edge on a high-risk or GPAI model may have made
// whoever created it the model's provider in law (Art. 25). This routes the §11.4 verdict to a
// human and records what they concluded.
//
// It flags; it does not decide, and it never blocks. Publishing, promotion and deletion are
// untouched by an open item — the queue is a list, not a gate (§17.1).

// ReviewInput is the client-settable half of a review.
//
// There is no field for VerdictAtReview, ReviewedBy or ReviewedAt. §17.6.1 says the server
// fills the verdict and rejects a supplied one; implementing that as "there is nowhere to put
// it" is stronger than a strip step, because the shared decoder rejects unknown fields and so
// the rejection cannot be forgotten by whoever adds the next field.
type ReviewInput struct {
	EdgeID  string               `json:"edgeId"`
	Outcome domain.ReviewOutcome `json:"outcome"`
	Note    string               `json:"note"`
}

// RecordReview appends a review of one derivation (§17.6.1).
//
// The verdict is computed here, from the hashes as they stand at this instant, and stored. It
// is never recomputed on read: a producer submitting a weights_hash next week must not be able
// to change what a reviewer is recorded as having seen (§17.4).
//
// It deliberately does **not** require the model to be classified. Condition 3 governs what
// the queue *surfaces*; refusing to record a human's judgement because nobody has filled in a
// classification would lose the judgement, which is the one thing here that cannot be
// recomputed.
func (s *Service) RecordReview(ctx context.Context, actor, model, version string, in ReviewInput) (*domain.ModificationReview, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if !domain.ValidReviewOutcome(in.Outcome) {
		e := domain.Invalid("unknown outcome")
		e.Details = map[string]any{"field": "outcome", "allowedValues": domain.ReviewOutcomes()}
		return nil, e
	}
	if in.EdgeID == "" {
		return nil, invalidField("edgeId is required — a review is of one derivation, not of a version", "edgeId")
	}

	edge, err := s.derivedFromEdge(ctx, v.ID, in.EdgeID)
	if err != nil {
		return nil, err
	}
	c := s.classifyAcross(ctx, v.ID, edge)

	r := &domain.ModificationReview{
		ID: domain.NewID(), VersionID: v.ID, EdgeID: edge.ID,
		VerdictAtReview: c.Verdict, Outcome: in.Outcome, Note: in.Note,
		ReviewedBy: actor, ReviewedAt: domain.NowMillis(),
	}
	// The outcome and the frozen verdict go on the event as structured data, not only in the
	// prose: an auditor reconstructing who concluded what, against what evidence, reads fields
	// (§16.8, same argument as classification.set).
	data, _ := json.Marshal(map[string]any{
		"edgeId":          edge.ID,
		"outcome":         string(in.Outcome),
		"verdictAtReview": string(c.Verdict),
	})
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.CreateReview(ctx, r); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "review.record", "model_version", v.ID,
			model+"@"+version+" derivation reviewed "+string(in.Outcome)+" (verdict "+string(c.Verdict)+")", data)
	}); err != nil {
		return nil, err
	}
	return r, nil
}

// ListVersionReviews returns every review recorded against a version, newest first (§17.6).
func (s *Service) ListVersionReviews(ctx context.Context, model, version string) ([]*domain.ModificationReview, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	return s.store.ListReviews(ctx, v.ID)
}

// ListReviewQueue answers §17.6.2 — "which derivations may have transferred provider
// liability, and has anyone looked?"
//
// status narrows to open or closed; empty returns both. Filtering happens here rather than in
// SQL because the verdict is a lookup over hashes, not a column, so paging is applied last —
// the same arrangement, and the same reason, as the inventory query (§16.8.2).
func (s *Service) ListReviewQueue(ctx context.Context, regime domain.Regime, status domain.ReviewStatus, o domain.ListOptions) ([]*domain.ReviewItem, string, error) {
	if regime == "" {
		regime = domain.RegimeEUAIAct
	}
	if !domain.ValidRegime(regime) {
		return nil, "", domain.Invalid("unknown regime")
	}
	if status != "" && !domain.ValidReviewStatus(status) {
		e := domain.Invalid("unknown status")
		e.Details = map[string]any{"field": "status", "allowedValues": []string{string(domain.ReviewOpen), string(domain.ReviewClosed)}}
		return nil, "", e
	}

	rows, err := s.store.ListDerivations(ctx, regime, "")
	if err != nil {
		return nil, "", err
	}
	items := make([]*domain.ReviewItem, 0, len(rows))
	for _, d := range rows {
		it := reviewItemOf(d)
		if it == nil || (status != "" && it.Status != status) {
			continue
		}
		items = append(items, it)
	}

	page, next := domain.Page(items, func(i *domain.ReviewItem) (int64, string) {
		return i.EdgeCreatedAt, i.EdgeID
	}, o.PageToken, o.PageSize)
	return page, next, nil
}

// reviewItemOf turns a store row into a queue item, or nil when §17.4's conditions 2 and 3 do
// not hold. It is the one place the queue is defined: the API, the console feed and drift
// clause 4 all read it, so none of them can disagree about what "open" means.
func reviewItemOf(d *domain.DerivationRow) *domain.ReviewItem {
	c := domain.Classify(d.FromHashes, d.ToHashes)
	if !domain.ReviewEligible(c.Verdict, d.EUSystemRiskClass, d.EUGpaiTier) {
		return nil
	}
	it := &domain.ReviewItem{
		Model: d.Model, Version: d.Version, VersionID: d.VersionID, EdgeID: d.Edge.ID,
		DerivedFromRef: d.Edge.DstRef,
		Verdict:        c.Verdict, Candidates: c.Candidates, Missing: c.Missing,
		Hashes: c.Hashes,
		Basis: domain.ReviewBasis{
			FromHashes: presentHashes(d.FromHashes),
			ToHashes:   presentHashes(d.ToHashes),
		},
		DeclaredMethod:    declaredMethod(d.Edge.Properties),
		EUSystemRiskClass: d.EUSystemRiskClass,
		EUGpaiTier:        d.EUGpaiTier,
		EdgeCreatedAt:     d.Edge.CreatedAt,
		Status:            domain.ReviewOpen,
	}
	if d.ParentVersion != "" {
		it.DerivedFrom = &domain.ReviewSide{Model: d.ParentModel, Version: d.ParentVersion}
	}
	// Condition 4. The review is returned alongside the *current* verdict rather than instead
	// of it: when the two differ, a producer submitted a hash after the review, and that
	// divergence is worth seeing (§17.7).
	if d.LatestReview != nil {
		it.Status, it.Review = domain.ReviewClosed, d.LatestReview
	}
	return it
}

// openReviewFacts returns, per model id, the newest **open** item's edge time — drift clause
// 4's input (§16.5). Zero for a model with no open item, which the predicate reads as an
// absent fact.
//
// modelID narrows the scan to one model; empty covers the whole inventory in one query.
func (s *Service) openReviewFacts(ctx context.Context, regime domain.Regime, modelID string) (map[string]int64, error) {
	rows, err := s.store.ListDerivations(ctx, regime, modelID)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, d := range rows {
		it := reviewItemOf(d)
		if it == nil || it.Status != domain.ReviewOpen {
			continue
		}
		if d.Edge.CreatedAt > out[d.ModelID] {
			out[d.ModelID] = d.Edge.CreatedAt
		}
	}
	return out, nil
}

// derivedFromEdge resolves an edge id against a version, refusing anything that is not a
// `derived_from` edge originating there (§17.6.3).
//
// The relation check is not pedantry: reviewing a `trained_on` edge would record an Art. 25
// judgement about a question Art. 25 does not ask, and §17.8 defers dataset-swap items until
// there are dataset-side facts to judge them on.
func (s *Service) derivedFromEdge(ctx context.Context, versionID, edgeID string) (*domain.LineageEdge, error) {
	edges, err := s.store.ListLineage(ctx, versionID)
	if err != nil {
		return nil, err
	}
	for _, e := range edges {
		if e.ID != edgeID {
			continue
		}
		// ListLineage returns edges in both directions; only the ones leaving this version
		// describe what *it* was derived from.
		if e.SrcID != versionID || e.Relation != domain.RelDerivedFrom {
			return nil, invalidField("edge '"+edgeID+"' is not a derived_from edge on this version", "edgeId")
		}
		return e, nil
	}
	return nil, invalidField("edge '"+edgeID+"' not found on this version", "edgeId")
}

// classifyAcross computes the §11.4 verdict over a derivation. An edge pointing at an external
// ref has no parent row and therefore no ladder, which Classify already reads as `unknown` —
// the honest answer for a fine-tune of a model this registry has never seen.
func (s *Service) classifyAcross(ctx context.Context, versionID string, e *domain.LineageEdge) domain.Classification {
	var from domain.Hashes
	if e.DstID != "" {
		if in := s.insightOrNil(ctx, e.DstID); in != nil {
			from = in.Hashes
		}
	}
	var to domain.Hashes
	if in := s.insightOrNil(ctx, versionID); in != nil {
		to = in.Hashes
	}
	return domain.Classify(from, to)
}

// declaredMethod reads properties.method off the edge (§11.3.6) — the intent, carried beside
// the measurement. Unparseable properties yield no method rather than an error: the field is
// free-form producer metadata, and a malformed one must not break the queue.
func declaredMethod(props json.RawMessage) string {
	if len(props) == 0 {
		return ""
	}
	var p struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(props, &p) != nil {
		return ""
	}
	return p.Method
}

func invalidField(msg, field string) *domain.Error {
	e := domain.Invalid(msg)
	e.Details = map[string]any{"field": field}
	return e
}
