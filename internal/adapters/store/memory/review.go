package memstore

import (
	"context"
	"sort"

	"github.com/proseria-research/lineage/internal/domain"
)

// In-memory modification-review storage (§17.5.1), mirroring the sqlstore semantics:
// append-only rows, and a derivation scan gated on the model carrying a classification row.

var _ domain.ReviewStore = (*Store)(nil)

func (s *Store) CreateReview(_ context.Context, r *domain.ModificationReview) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.versions[r.VersionID]; !ok {
		return domain.NotFound("version not found")
	}
	s.reviews = append(s.reviews, deepCopy(r))
	return nil
}

func (s *Store) ListReviews(_ context.Context, versionID string) ([]*domain.ModificationReview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*domain.ModificationReview{}
	for _, r := range s.reviews {
		if r.VersionID == versionID {
			out = append(out, r)
		}
	}
	sortReviewsNewestFirst(out)
	return deepCopyAll(out), nil
}

// ListDerivations mirrors the sqlstore join (§17.4). The classification lookup is the inner
// join: a model with no row for the regime yields no rows at all.
func (s *Store) ListDerivations(_ context.Context, regime domain.Regime, modelID string) ([]*domain.DerivationRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := []*domain.DerivationRow{}
	for _, e := range s.lineage {
		if e.Relation != domain.RelDerivedFrom || e.SrcType != "model_version" {
			continue
		}
		v, ok := s.versions[e.SrcID]
		if !ok {
			continue
		}
		if modelID != "" && v.ModelID != modelID {
			continue
		}
		m, ok := s.models[v.ModelID]
		if !ok {
			continue
		}
		c, ok := s.classifications[v.ModelID][regime]
		if !ok {
			continue
		}

		d := &domain.DerivationRow{
			ModelID: m.ID, Model: m.Name, Version: v.Name, VersionID: v.ID,
			Edge:              deepCopy(e),
			ToHashes:          s.hashesFor(v.ID),
			EUSystemRiskClass: c.EUSystemRiskClass,
			EUGpaiTier:        c.EUGpaiTier,
			LatestReview:      s.latestReview(v.ID, e.ID),
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
		a, b := out[i].Edge, out[j].Edge
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt > b.CreatedAt
		}
		return a.ID > b.ID
	})
	return out, nil
}

// hashesFor returns the version's hash ladder, or the zero value when no producer has
// reported insight — which Classify already reads as `unknown` (§11.4.3).
func (s *Store) hashesFor(versionID string) domain.Hashes {
	if in, ok := s.insights[versionID]; ok {
		return in.Hashes
	}
	return domain.Hashes{}
}

func (s *Store) latestReview(versionID, edgeID string) *domain.ModificationReview {
	var match []*domain.ModificationReview
	for _, r := range s.reviews {
		if r.VersionID == versionID && r.EdgeID == edgeID {
			match = append(match, r)
		}
	}
	if len(match) == 0 {
		return nil
	}
	sortReviewsNewestFirst(match)
	return deepCopy(match[0])
}

func sortReviewsNewestFirst(rs []*domain.ModificationReview) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].ReviewedAt != rs[j].ReviewedAt {
			return rs[i].ReviewedAt > rs[j].ReviewedAt
		}
		return rs[i].ID > rs[j].ID
	})
}
