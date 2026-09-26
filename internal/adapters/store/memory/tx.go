package memstore

import (
	"context"
	"maps"

	"github.com/proseria-research/lineage/internal/domain"
)

// The unit of work (domain.Transactor) for the in-memory adapter.
//
// InTx takes the store's write lock for the whole of fn, runs fn against a deep copy of the
// state, and swaps the copy in only when fn returns nil. Serialising every unit of work is the
// memory adapter's analogue of SQLite's single writer, and copy-then-swap makes rollback
// structural: a failed or panicking fn leaves the live state untouched because it never
// touched it. The copy is O(store) per unit of work, which is the right trade for a dev/test
// adapter — the alternative, an undo log per method, is a second implementation of every
// write that could quietly disagree with the first.

// rwLocker is sync.RWMutex's method set, so a transaction's view can carry a no-op in its
// place: every method keeps its own locking, and inside InTx that locking is already done.
type rwLocker interface {
	Lock()
	Unlock()
	RLock()
	RUnlock()
}

type nopLock struct{}

func (nopLock) Lock()    {}
func (nopLock) Unlock()  {}
func (nopLock) RLock()   {}
func (nopLock) RUnlock() {}

// InTx runs fn as one unit of work. On a transaction's view it joins: fn runs on the same
// working copy, and only the outermost InTx swaps it in.
func (s *Store) InTx(_ context.Context, fn func(tx domain.MetadataStore) error) error {
	if s.inTx {
		return fn(s)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	work := s.state.clone()
	if err := fn(&Store{mu: nopLock{}, inTx: true, state: work}); err != nil {
		return err
	}
	*s.state = *work
	return nil
}

// clone deep-copies the state. Rows are copied as well as containers because several write
// methods update a live row in place (SetStage, ClearValidationConditions), and a shared row
// would carry a rolled-back write into the committed state.
func (st *state) clone() *state {
	c := &state{
		models:      cloneRows(st.models),
		modelByNm:   maps.Clone(st.modelByNm),
		versions:    cloneRows(st.versions),
		artifacts:   cloneRows(st.artifacts),
		lineage:     cloneRows(st.lineage),
		deployments: cloneRows(st.deployments),
		audit:       deepCopyAll(st.audit),

		insights:    cloneRows(st.insights),
		layers:      cloneLists(st.layers),
		footprints:  cloneLists(st.footprints),
		evaluations: cloneLists(st.evaluations),

		classifications: make(map[string]map[domain.Regime]*domain.RiskClassification, len(st.classifications)),

		reviews:     deepCopyAll(st.reviews),
		validations: deepCopyAll(st.validations),
		changePlans: deepCopyAll(st.changePlans),

		epochs: cloneRows(st.epochs),
	}
	for id, byRegime := range st.classifications {
		c.classifications[id] = cloneRows(byRegime)
	}
	return c
}

func cloneRows[K comparable, T any](m map[K]*T) map[K]*T {
	out := make(map[K]*T, len(m))
	for k, v := range m {
		out[k] = deepCopy(v)
	}
	return out
}

func cloneLists[K comparable, T any](m map[K][]*T) map[K][]*T {
	out := make(map[K][]*T, len(m))
	for k, v := range m {
		out[k] = deepCopyAll(v)
	}
	return out
}
