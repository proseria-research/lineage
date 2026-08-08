package core

import (
	"context"
	"encoding/json"

	"github.com/proseria-research/lineage/internal/domain"
)

// Risk-classification writes and reads (§16). The registry records what a person declared
// and reports when that declaration has gone out of date. It never decides a class, never
// downgrades one, and never blocks anything because one is stale (§16.2).

// ClassificationInput is the client-settable half of a classification.
//
// There is no field for ClassifiedAt or ClassifiedBy. §16.6 says the server sets them and a
// request value is ignored — implementing that as "there is nowhere to put one" is stronger
// than implementing it as a line of code that strips them, because it cannot be forgotten by
// the next person to add a field.
type ClassificationInput struct {
	EUSystemRiskClass domain.EUSystemRiskClass `json:"euSystemRiskClass"`
	EUGpaiTier        domain.EUGpaiTier        `json:"euGpaiTier"`
	IntendedPurpose   string                   `json:"intendedPurpose"`
	Basis             string                   `json:"basis"`
	ReviewDueAt       *int64                   `json:"reviewDueAt"`
}

// SetClassification replaces this model's assessment under one regime (§16.8: PUT, not
// PATCH — an old basis must never sit underneath a brand-new class).
//
// The regime comes from the path, so writing one regime cannot see or touch another's row,
// and in particular cannot move another's ClassifiedAt anchor (§16.3.2).
func (s *Service) SetClassification(ctx context.Context, actor, model string, regime domain.Regime, in ClassificationInput) (*domain.ClassificationView, error) {
	m, err := s.store.GetModel(ctx, model)
	if err != nil {
		return nil, err
	}

	now := domain.NowMillis()
	c := domain.RiskClassification{
		ModelID:           m.ID,
		Regime:            regime,
		EUSystemRiskClass: in.EUSystemRiskClass,
		EUGpaiTier:        in.EUGpaiTier,
		IntendedPurpose:   in.IntendedPurpose,
		Basis:             in.Basis,
		ReviewDueAt:       in.ReviewDueAt,
		// Server-set. ClassifiedAt is the anchor every §16.5 clause measures against, so a
		// client that could backdate it could hide a staleness.
		ClassifiedAt: now,
		ClassifiedBy: actor,
	}
	c.ApplyDefaults()
	if verr := domain.ValidateRiskClassification(c, now); verr != nil {
		return nil, verr
	}
	if err := s.store.PutClassification(ctx, &c); err != nil {
		return nil, err
	}

	data, _ := json.Marshal(map[string]any{
		"regime":            string(regime),
		"euSystemRiskClass": string(c.EUSystemRiskClass),
		"euGpaiTier":        string(c.EUGpaiTier),
		"reviewDueAt":       c.ReviewDueAt,
	})
	// The regime is recorded on the event, not just in the summary: an auditor reconstructing
	// who classified what under which rulebook reads structured data, not prose (§16.8).
	s.audit(ctx, actor, "classification.set", "model", m.ID,
		model+" classified "+string(c.EUSystemRiskClass)+" under "+string(regime), data)

	return s.viewFor(ctx, m.ID, &c, now)
}

// GetClassification returns one regime's row with its computed state.
//
// A model with no row for this regime is a not_found. That is the `unclassified` state
// (§16.4), and the caller distinguishes it from `stale` by the code — this endpoint answers
// "show me the assessment", and there is no assessment to show. The inventory query is where
// `unclassified` appears as a state alongside the others (§16.8.2).
func (s *Service) GetClassification(ctx context.Context, model string, regime domain.Regime) (*domain.ClassificationView, error) {
	m, err := s.store.GetModel(ctx, model)
	if err != nil {
		return nil, err
	}
	c, err := s.store.GetClassification(ctx, m.ID, regime)
	if err != nil {
		return nil, err
	}
	return s.viewFor(ctx, m.ID, c, domain.NowMillis())
}

// ListClassifications returns every regime's row for a model, each with its own state.
// States are computed per row against that row's own ClassifiedAt, which is the whole point
// of the per-regime key (§16.3.2).
func (s *Service) ListClassifications(ctx context.Context, model string) ([]*domain.ClassificationView, error) {
	m, err := s.store.GetModel(ctx, model)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListClassifications(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	// One fetch serves every row: the facts are regime-independent, and only the anchor
	// they are compared against differs.
	facts, err := s.store.DriftFactsFor(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	now := domain.NowMillis()

	out := make([]*domain.ClassificationView, 0, len(rows))
	for _, c := range rows {
		out = append(out, newView(c, facts, now))
	}
	return out, nil
}

func (s *Service) viewFor(ctx context.Context, modelID string, c *domain.RiskClassification, now int64) (*domain.ClassificationView, error) {
	facts, err := s.store.DriftFactsFor(ctx, modelID)
	if err != nil {
		return nil, err
	}
	return newView(c, facts, now), nil
}

func newView(c *domain.RiskClassification, facts domain.DriftFacts, now int64) *domain.ClassificationView {
	state, reasons := domain.ClassificationStateOf(c, facts, now)
	return &domain.ClassificationView{RiskClassification: *c, State: state, StaleReasons: reasons}
}
