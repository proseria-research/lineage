package memstore

import (
	"context"
	"sort"

	"github.com/proseria-research/lineage/internal/domain"
)

// In-memory change-control-plan storage (§22.6), mirroring the sqlstore semantics: append-only
// rows with one set-once close, the overlap rule applied under the write lock, and the same
// edge scan.

var _ domain.ChangePlanStore = (*Store)(nil)

func (s *Store) CreateChangePlan(_ context.Context, p *domain.ChangePlan, supersedes string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.models[p.ModelID]; !ok {
		return domain.NotFound("model not found")
	}
	old, err := domain.CheckPlanDeclaration(s.plansFor(p.ModelID), p, supersedes)
	if err != nil {
		return err
	}
	if old != nil {
		to := p.EffectiveFrom
		old.EffectiveTo = &to
	}
	s.changePlans = append(s.changePlans, deepCopy(p))
	return nil
}

func (s *Store) ListChangePlans(_ context.Context, modelID string) ([]*domain.ChangePlan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return deepCopyAll(s.plansFor(modelID)), nil
}

// plansFor returns the store's own rows, newest effective_from first with the sqlstore id
// tiebreak. Caller holds s.mu; CreateChangePlan closes a plan through the returned pointer.
func (s *Store) plansFor(modelID string) []*domain.ChangePlan {
	out := []*domain.ChangePlan{}
	for _, p := range s.changePlans {
		if modelID == "" || p.ModelID == modelID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EffectiveFrom != out[j].EffectiveFrom {
			return out[i].EffectiveFrom > out[j].EffectiveFrom
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// ListPlanDerivations mirrors the sqlstore scan: unnarrowed, only models with a plan.
func (s *Store) ListPlanDerivations(_ context.Context, modelID, versionID string) ([]*domain.PlanDerivationRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	planned := map[string]bool{}
	for _, p := range s.changePlans {
		planned[p.ModelID] = true
	}
	out := []*domain.PlanDerivationRow{}
	for _, e := range s.lineage {
		if e.Relation != domain.RelDerivedFrom || e.SrcType != "model_version" {
			continue
		}
		v, ok := s.versions[e.SrcID]
		if !ok || (versionID != "" && v.ID != versionID) {
			continue
		}
		if (modelID == "" && !planned[v.ModelID]) || (modelID != "" && v.ModelID != modelID) {
			continue
		}
		m, ok := s.models[v.ModelID]
		if !ok {
			continue
		}
		d := &domain.PlanDerivationRow{
			ModelID: m.ID, Model: m.Name, Version: v.Name, VersionID: v.ID, PublishedAt: v.CreatedAt,
			Edge: deepCopy(e), ToHashes: s.hashesFor(v.ID),
		}
		if pv, ok := s.versions[e.DstID]; ok {
			d.ParentVersion, d.FromHashes = pv.Name, s.hashesFor(pv.ID)
			if pm, ok := s.models[pv.ModelID]; ok {
				d.ParentModel = pm.Name
			}
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.PublishedAt != b.PublishedAt {
			return a.PublishedAt > b.PublishedAt
		}
		if a.VersionID != b.VersionID {
			return a.VersionID > b.VersionID
		}
		return a.Edge.ID > b.Edge.ID
	})
	return out, nil
}
