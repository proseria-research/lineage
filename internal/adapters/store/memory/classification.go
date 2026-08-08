package memstore

import (
	"context"
	"sort"

	"github.com/proseria-research/lineage/internal/domain"
)

// In-memory risk-classification storage (§16.7.1), mirroring the sqlstore semantics: one row
// per (model, regime), replaced whole on write, cascaded away with the model.

var _ domain.ComplianceStore = (*Store)(nil)

func (s *Store) PutClassification(_ context.Context, c *domain.RiskClassification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.models[c.ModelID]; !ok {
		return domain.NotFound("model not found")
	}
	byRegime, ok := s.classifications[c.ModelID]
	if !ok {
		byRegime = map[domain.Regime]*domain.RiskClassification{}
		s.classifications[c.ModelID] = byRegime
	}
	cp := *c
	// Copy the pointer's target too: the caller keeps its struct, and a shared *int64 would
	// let a later edit of the request reach into stored state.
	if c.ReviewDueAt != nil {
		v := *c.ReviewDueAt
		cp.ReviewDueAt = &v
	}
	byRegime[c.Regime] = &cp
	return nil
}

func (s *Store) GetClassification(_ context.Context, modelID string, regime domain.Regime) (*domain.RiskClassification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.classifications[modelID][regime]
	if !ok {
		return nil, domain.NotFound("no " + string(regime) + " classification recorded for this model")
	}
	return copyClassification(c), nil
}

func (s *Store) ListClassifications(_ context.Context, modelID string) ([]*domain.RiskClassification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byRegime := s.classifications[modelID]
	out := make([]*domain.RiskClassification, 0, len(byRegime))
	for _, c := range byRegime {
		out = append(out, copyClassification(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Regime < out[j].Regime })
	return out, nil
}

func copyClassification(c *domain.RiskClassification) *domain.RiskClassification {
	cp := *c
	if c.ReviewDueAt != nil {
		v := *c.ReviewDueAt
		cp.ReviewDueAt = &v
	}
	return &cp
}
