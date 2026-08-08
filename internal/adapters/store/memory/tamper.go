package memstore

import "github.com/proseria-research/lineage/internal/domain"

// Tampering, on purpose.
//
// Merkle sealing (§19.5) exists to detect changes that no API can make: someone editing or
// deleting rows in the database directly. There is deliberately no port method that can do
// any of this — AuditStore is append-only and AttestationStore has no update or delete — so
// a test that wants to prove tampering is *detected* has no legitimate way to commit it.
//
// These four functions are that door, and they are the only ones in the codebase. They are
// exported because the tests that need them live in other packages (core, api); everything
// about the naming is intended to make a production call site obvious in review. Against a
// SQL store the equivalent is a raw UPDATE or DELETE through Store.DB(), which is precisely
// the threat model.

// TamperAuditEpochForTest moves a row into another window. Also used, less maliciously, to
// place rows in specific epochs without sleeping through real intervals.
func (s *Store) TamperAuditEpochForTest(id string, epoch int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.audit {
		if e.ID == id {
			v := epoch
			e.Epoch = &v
			return nil
		}
	}
	return domain.NotFound("audit event '" + id + "' not found")
}

// TamperAuditSummaryForTest rewrites what a row says happened — the change §19.5.1's field
// list would have left undetectable, and the reason the leaf hashes the whole row.
func (s *Store) TamperAuditSummaryForTest(id, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.audit {
		if e.ID == id {
			e.Summary = summary
			return nil
		}
	}
	return domain.NotFound("audit event '" + id + "' not found")
}

// TamperDeleteAuditForTest removes a row from a sealed window.
func (s *Store) TamperDeleteAuditForTest(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.audit {
		if e.ID == id {
			s.audit = append(s.audit[:i], s.audit[i+1:]...)
			return nil
		}
	}
	return domain.NotFound("audit event '" + id + "' not found")
}

// TamperDeleteEpochForTest removes a whole seal, the way someone hiding a window's worth of
// history would. The chain is what catches it: the next epoch's prev_root no longer matches
// the epoch before it.
func (s *Store) TamperDeleteEpochForTest(epoch int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.epochs[epoch]; !ok {
		return domain.NotFound("epoch not sealed")
	}
	delete(s.epochs, epoch)
	return nil
}
