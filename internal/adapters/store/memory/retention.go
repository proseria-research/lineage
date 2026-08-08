package memstore

import (
	"context"
	"sort"

	"github.com/proseria-research/lineage/internal/domain"
)

// RetentionStore (§19.3) over the in-memory maps. The hold lives on the entity itself, so
// there is no third map to keep in step — and no way for a hold to survive the row it holds.

func (s *Store) SetHold(_ context.Context, subjectType, subjectID string, h *domain.Hold) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch subjectType {
	case domain.SubjectModel:
		m, ok := s.models[subjectID]
		if !ok {
			return domain.NotFound("model not found")
		}
		m.LegalHold = copyHold(h)
	case domain.SubjectVersion:
		v, ok := s.versions[subjectID]
		if !ok {
			return domain.NotFound("version not found")
		}
		v.LegalHold = copyHold(h)
	default:
		return domain.Invalid("cannot hold subject type '" + subjectType + "'")
	}
	return nil
}

func (s *Store) DeleteGuardFor(_ context.Context, subjectType, subjectID string) (domain.DeleteGuard, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	switch subjectType {
	case domain.SubjectModel:
		m, ok := s.models[subjectID]
		if !ok {
			return domain.DeleteGuard{}, domain.NotFound("model not found")
		}
		g := domain.DeleteGuard{Hold: copyHold(m.LegalHold), NewestCreatedAt: m.CreatedAt}
		// The cascade destroys every version, so both facts have to account for them: a hold
		// on any one of them refuses (inheritance upward, see the port doc), and the floor
		// measures the youngest thing that would be destroyed, not the model's own age.
		for _, v := range s.versionsOf(subjectID) {
			if v.CreatedAt > g.NewestCreatedAt {
				g.NewestCreatedAt = v.CreatedAt
			}
			if g.Hold == nil && v.LegalHold != nil {
				g.Hold = copyHold(v.LegalHold)
				g.HeldSubject = "version/" + m.Name + "@" + v.Name
			}
		}
		return g, nil

	case domain.SubjectVersion:
		v, ok := s.versions[subjectID]
		if !ok {
			return domain.DeleteGuard{}, domain.NotFound("version not found")
		}
		g := domain.DeleteGuard{Hold: copyHold(v.LegalHold), NewestCreatedAt: v.CreatedAt}
		// §19.3.1, downward: a hold on the model covers its versions. The version's own hold
		// wins when both exist — it is the more specific statement, and it names a subject
		// the caller can act on directly.
		if g.Hold == nil {
			if m, ok := s.models[v.ModelID]; ok && m.LegalHold != nil {
				g.Hold = copyHold(m.LegalHold)
				g.HeldSubject = "model/" + m.Name
			}
		}
		return g, nil
	}
	return domain.DeleteGuard{}, domain.Invalid("cannot hold subject type '" + subjectType + "'")
}

// versionsOf returns a model's versions in a stable order, so which held version gets named
// in a refusal does not depend on Go's map iteration.
func (s *Store) versionsOf(modelID string) []*domain.ModelVersion {
	var out []*domain.ModelVersion
	for _, v := range s.versions {
		if v.ModelID == modelID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// copyHold deep-copies, so a caller mutating what it got back cannot reach into the store —
// the same discipline the classification adapter applies to ReviewDueAt.
func copyHold(h *domain.Hold) *domain.Hold {
	if h == nil {
		return nil
	}
	c := *h
	return &c
}
