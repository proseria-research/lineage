package memstore

import (
	"context"
	"sort"

	"github.com/proseria-research/lineage/internal/domain"
)

// AttestationStore (§19.5) over the in-memory audit slice, mirroring the sqlstore semantics:
// a nil epoch is "not attested", an epoch is sealed at most once, and epochs are returned in
// ascending order.

func (s *Store) SealableEpochs(_ context.Context, notAfter int64, limit int) ([]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[int64]bool{}
	for _, e := range s.audit {
		if e.Epoch == nil || *e.Epoch >= notAfter {
			continue
		}
		if _, sealed := s.epochs[*e.Epoch]; sealed {
			continue
		}
		seen[*e.Epoch] = true
	}
	out := make([]int64, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) AuditEventsInEpoch(_ context.Context, epoch int64) ([]*domain.AuditEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*domain.AuditEvent{}
	for _, e := range s.audit {
		if e.Epoch != nil && *e.Epoch == epoch {
			out = append(out, deepCopy(e))
		}
	}
	return out, nil
}

func (s *Store) AppendEpoch(_ context.Context, e *domain.AuditEpoch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Append-only: a second seal for a window is either a duplicated sealer or a rewrite.
	if _, ok := s.epochs[e.Epoch]; ok {
		return domain.Exists("epoch already sealed")
	}
	s.epochs[e.Epoch] = deepCopy(e)
	return nil
}

func (s *Store) LatestSealedEpoch(_ context.Context) (*domain.AuditEpoch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *domain.AuditEpoch
	for _, e := range s.epochs {
		if best == nil || e.Epoch > best.Epoch {
			best = e
		}
	}
	if best == nil {
		return nil, domain.NotFound("nothing has been sealed yet")
	}
	return deepCopy(best), nil
}

func (s *Store) ListEpochs(_ context.Context, from, to int64) ([]*domain.AuditEpoch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*domain.AuditEpoch{}
	for _, e := range s.epochs {
		if e.Epoch < from || (to > 0 && e.Epoch > to) {
			continue
		}
		out = append(out, deepCopy(e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Epoch < out[j].Epoch })
	return out, nil
}

func (s *Store) GetAuditEvent(_ context.Context, id string) (*domain.AuditEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.audit {
		if e.ID == id {
			return deepCopy(e), nil
		}
	}
	return nil, domain.NotFound("audit event '" + id + "' not found")
}

func (s *Store) FirstAttestedEpoch(_ context.Context) (int64, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var min int64
	found := false
	for _, e := range s.audit {
		if e.Epoch == nil {
			continue
		}
		if !found || *e.Epoch < min {
			min, found = *e.Epoch, true
		}
	}
	return min, found, nil
}
