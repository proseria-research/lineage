package memstore

import (
	"context"
	"sort"

	"github.com/proseria-research/lineage/internal/domain"
)

// In-memory model-risk validation storage (§20.8.2), mirroring the sqlstore semantics:
// append-only rows, one set-once clear, and the same subject-version rule for the inventory.

var _ domain.ValidationStore = (*Store)(nil)

func (s *Store) CreateValidation(_ context.Context, v *domain.Validation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.versions[v.VersionID]; !ok {
		return domain.NotFound("version not found")
	}
	s.validations = append(s.validations, deepCopy(v))
	return nil
}

func (s *Store) ListValidations(_ context.Context, versionID string) ([]*domain.Validation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return deepCopyAll(s.validationsFor(versionID)), nil
}

func (s *Store) ClearValidationConditions(_ context.Context, versionID, id string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.validations {
		if v.ID != id || v.VersionID != versionID {
			continue
		}
		if v.ConditionsClearedAt != nil {
			return domain.Precondition("conditions already cleared", map[string]any{"reason": "already_cleared"})
		}
		v.ConditionsClearedAt = &at
		return nil
	}
	return domain.NotFound("validation '" + id + "' not found on this version")
}

// ListMRMInventory mirrors the sqlstore statement (§20.9.2), including its subject rule:
// the production version if there is one, else the newest.
func (s *Store) ListMRMInventory(ctx context.Context, o domain.ListOptions, f domain.MRMFilter) ([]*domain.MRMInventoryRow, error) {
	models, _, err := s.ListModels(ctx, domain.ListOptions{
		Filters: o.Filters, Q: o.Q, Labels: o.Labels, CustomProps: o.CustomProps,
	})
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*domain.MRMInventoryRow{}
	for _, m := range models {
		if f.ModelID != "" && m.ID != f.ModelID {
			continue
		}
		var c *domain.RiskClassification
		if row, ok := s.classifications[m.ID][domain.RegimeMRM]; ok {
			c = deepCopy(row)
		}
		if f.Tier != "" && (c == nil || c.MRMTier != f.Tier) {
			continue
		}
		out = append(out, &domain.MRMInventoryRow{Model: m, Classification: c, Facts: s.mrmFactsLocked(m.ID)})
	}
	return out, nil
}

func (s *Store) mrmFactsLocked(modelID string) domain.MRMFacts {
	var (
		f       domain.MRMFacts
		subject *domain.ModelVersion
	)
	for _, v := range s.versions {
		if v.ModelID != modelID {
			continue
		}
		if v.CreatedAt > f.LatestVersionCreatedAt {
			f.LatestVersionCreatedAt = v.CreatedAt
		}
		if subject == nil || subjectBefore(v, subject) {
			subject = v
		}
	}
	if subject == nil {
		return f
	}
	f.Subject = &domain.MRMSubject{
		VersionID: subject.ID, Version: subject.Name, Author: subject.Author,
		Stage: subject.Stage, StageChangedAt: subject.StageChangedAt,
	}
	for _, e := range s.evaluations[subject.ID] {
		if e.RunAt > f.LatestEvaluationRunAt {
			f.LatestEvaluationRunAt = e.RunAt
		}
	}
	if vals := s.validationsFor(subject.ID); len(vals) > 0 {
		f.Validation = deepCopy(vals[0])
	}
	return f
}

// subjectBefore orders candidates exactly as the sqlstore subquery does: production first,
// then newest, then id.
func subjectBefore(a, b *domain.ModelVersion) bool {
	ap, bp := a.Stage == domain.StageProduction, b.Stage == domain.StageProduction
	if ap != bp {
		return ap
	}
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt > b.CreatedAt
	}
	return a.ID > b.ID
}

// validationsFor returns the live rows newest-first; callers copy before handing them out.
func (s *Store) validationsFor(versionID string) []*domain.Validation {
	out := []*domain.Validation{}
	for _, v := range s.validations {
		if v.VersionID == versionID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ValidatedAt != out[j].ValidatedAt {
			return out[i].ValidatedAt > out[j].ValidatedAt
		}
		return out[i].ID > out[j].ID
	})
	return out
}
