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
	byRegime[c.Regime] = deepCopy(c)
	return nil
}

func (s *Store) GetClassification(_ context.Context, modelID string, regime domain.Regime) (*domain.RiskClassification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.classifications[modelID][regime]
	if !ok {
		return nil, domain.NotFound("no " + string(regime) + " classification recorded for this model")
	}
	return deepCopy(c), nil
}

func (s *Store) ListClassifications(_ context.Context, modelID string) ([]*domain.RiskClassification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byRegime := s.classifications[modelID]
	out := make([]*domain.RiskClassification, 0, len(byRegime))
	for _, c := range byRegime {
		out = append(out, deepCopy(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Regime < out[j].Regime })
	return out, nil
}

// DriftFactsFor mirrors the sqlstore aggregate: the newest version's creation time, and the
// production version's last update (production is a singleton stage, §02.4). Zero means no
// such version, which the §16.5 predicate reads as an absent fact.
func (s *Store) DriftFactsFor(_ context.Context, modelID string) (domain.DriftFacts, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var f domain.DriftFacts
	for _, v := range s.versions {
		if v.ModelID != modelID {
			continue
		}
		if v.CreatedAt > f.LatestVersionCreatedAt {
			f.LatestVersionCreatedAt = v.CreatedAt
		}
		if v.Stage == domain.StageProduction && v.UpdatedAt > f.LatestProductionUpdatedAt {
			f.LatestProductionUpdatedAt = v.UpdatedAt
		}
	}
	// LatestOpenReviewCreatedAt stays zero until M16 (`17.4`).
	return f, nil
}

// ListInventory mirrors the sqlstore join in memory (§16.8.2). Unclassified models are kept
// unless a stored-enum filter is set, since a null column never equals a value.
func (s *Store) ListInventory(ctx context.Context, o domain.ListOptions, f domain.ClassificationFilter) ([]*domain.ModelInventoryRow, error) {
	models, _, err := s.ListModels(ctx, domain.ListOptions{
		// Reuse the model-level filtering, but not the paging: the caller pages after it has
		// applied the computed-state filter.
		Filters: o.Filters, Q: o.Q, Labels: o.Labels, CustomProps: o.CustomProps,
	})
	if err != nil {
		return nil, err
	}

	out := []*domain.ModelInventoryRow{}
	for _, m := range models {
		c := s.classificationFor(m.ID, f.Regime)
		if f.EUSystemRiskClass != "" && (c == nil || c.EUSystemRiskClass != f.EUSystemRiskClass) {
			continue
		}
		if f.EUGpaiTier != "" && (c == nil || c.EUGpaiTier != f.EUGpaiTier) {
			continue
		}
		facts, err := s.DriftFactsFor(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, &domain.ModelInventoryRow{Model: m, Classification: c, Facts: facts})
	}
	return out, nil
}

func (s *Store) classificationFor(modelID string, regime domain.Regime) *domain.RiskClassification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c, ok := s.classifications[modelID][regime]; ok {
		return deepCopy(c)
	}
	return nil
}
