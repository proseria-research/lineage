package memstore

import (
	"context"
	"sort"

	"github.com/proseria-research/lineage/internal/domain"
)

// In-memory model-insight storage (§11.3), mirroring the sqlstore semantics: insight is
// upserted whole (the core merges), layer blocks are replaced as a set, footprints upsert
// by scenario, and evaluations append.

func (s *Store) GetInsight(_ context.Context, versionID string) (*domain.VersionInsight, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.insights[versionID]
	if !ok {
		return nil, domain.NotFound("no insight recorded for this version")
	}
	return deepCopy(in), nil
}

func (s *Store) UpsertInsight(_ context.Context, in *domain.VersionInsight) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := deepCopy(in)
	cp.Layers = nil // layers live in their own set, never inline
	s.insights[in.VersionID] = cp
	return nil
}

func (s *Store) ReplaceLayerBlocks(_ context.Context, versionID string, blocks []*domain.LayerBlock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(blocks) == 0 {
		delete(s.layers, versionID)
		return nil
	}
	out := make([]*domain.LayerBlock, 0, len(blocks))
	for _, b := range blocks {
		cp := deepCopy(b)
		cp.VersionID = versionID
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	s.layers[versionID] = out
	return nil
}

func (s *Store) ListLayerBlocks(_ context.Context, versionID string) ([]*domain.LayerBlock, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return deepCopyAll(s.layers[versionID]), nil
}

func (s *Store) UpsertFootprint(_ context.Context, f *domain.Footprint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := deepCopy(f)
	list := s.footprints[f.VersionID]
	for i, ex := range list {
		if ex.Scenario == f.Scenario {
			list[i] = cp
			return nil
		}
	}
	list = append(list, cp)
	sort.Slice(list, func(i, j int) bool { return list[i].Scenario < list[j].Scenario })
	s.footprints[f.VersionID] = list
	return nil
}

func (s *Store) ListFootprints(_ context.Context, versionID string) ([]*domain.Footprint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return deepCopyAll(s.footprints[versionID]), nil
}

func (s *Store) CreateEvaluation(_ context.Context, e *domain.Evaluation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evaluations[e.VersionID] = append(s.evaluations[e.VersionID], deepCopy(e))
	return nil
}

func (s *Store) ListEvaluations(_ context.Context, versionID string) ([]*domain.Evaluation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := deepCopyAll(s.evaluations[versionID])
	// Newest run first, matching the sqlstore ordering.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RunAt != out[j].RunAt {
			return out[i].RunAt > out[j].RunAt
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out, nil
}
