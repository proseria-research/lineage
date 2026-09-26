package sqlstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/proseria-research/lineage/internal/domain"
)

// AttestationStore (§19.5) — the sealer's window scan, the verifier's recompute, and the
// inclusion-proof lookup.

const auditCols = "id,at,actor,action,subject_type,subject_id,summary,data,epoch"

// SealableEpochs is the set difference "epochs with attested rows" minus "epochs already
// sealed", bounded above by the first window that might still take a commit.
//
// One query rather than list-then-check: the sealer runs this every interval forever, and
// the database can do the anti-join in a single index scan of idx_audit_epoch.
func (s *Store) SealableEpochs(ctx context.Context, notAfter int64, limit int) ([]int64, error) {
	const q = `
		SELECT DISTINCT e.epoch FROM audit_event e
		WHERE e.epoch IS NOT NULL AND e.epoch < ?
		  AND NOT EXISTS (SELECT 1 FROM audit_epoch a WHERE a.epoch = e.epoch)
		ORDER BY e.epoch ASC
		LIMIT ?`
	rows, err := s.q.QueryContext(ctx, s.rb(q), notAfter, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var e int64
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AuditEventsInEpoch returns the window's rows unordered — SortAuditEvents owns leaf order,
// so no root ever depends on an engine's collation (§19.5.1).
func (s *Store) AuditEventsInEpoch(ctx context.Context, epoch int64) ([]*domain.AuditEvent, error) {
	rows, err := s.q.QueryContext(ctx, s.rb(
		`SELECT `+auditCols+` FROM audit_event WHERE epoch = ?`), epoch)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []*domain.AuditEvent{}
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AppendEpoch inserts a seal. The PK does the work: a second seal for a window is either a
// duplicated sealer or a rewrite, and the unique violation surfaces it instead of letting an
// UPSERT quietly replace a root someone may already have cited.
func (s *Store) AppendEpoch(ctx context.Context, e *domain.AuditEpoch) error {
	_, err := s.q.ExecContext(ctx, s.rb(
		`INSERT INTO audit_epoch (epoch,root,prev_root,leaf_count,interval_ms,sealed_at)
		 VALUES (?,?,?,?,?,?)`),
		e.Epoch, e.Root, nullStr(e.PrevRoot), e.LeafCount, e.IntervalMillis, e.SealedAt)
	if s.d.IsUniqueViolation(err) {
		return domain.Exists("epoch already sealed")
	}
	return err
}

func (s *Store) LatestSealedEpoch(ctx context.Context) (*domain.AuditEpoch, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT epoch,root,prev_root,leaf_count,interval_ms,sealed_at FROM audit_epoch ORDER BY epoch DESC LIMIT 1`)
	e, err := scanEpoch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("nothing has been sealed yet")
	}
	return e, err
}

func (s *Store) ListEpochs(ctx context.Context, from, to int64) ([]*domain.AuditEpoch, error) {
	q := `SELECT epoch,root,prev_root,leaf_count,interval_ms,sealed_at FROM audit_epoch WHERE epoch >= ?`
	args := []any{from}
	if to > 0 {
		q += ` AND epoch <= ?`
		args = append(args, to)
	}
	q += ` ORDER BY epoch ASC`
	rows, err := s.q.QueryContext(ctx, s.rb(q), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []*domain.AuditEpoch{}
	for rows.Next() {
		e, err := scanEpoch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) GetAuditEvent(ctx context.Context, id string) (*domain.AuditEvent, error) {
	row := s.q.QueryRowContext(ctx, s.rb(`SELECT `+auditCols+` FROM audit_event WHERE id = ?`), id)
	e, err := scanAudit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("audit event '" + id + "' not found")
	}
	return e, err
}

func (s *Store) FirstAttestedEpoch(ctx context.Context) (int64, bool, error) {
	var min sql.NullInt64
	if err := s.q.QueryRowContext(ctx,
		`SELECT MIN(epoch) FROM audit_event WHERE epoch IS NOT NULL`).Scan(&min); err != nil {
		return 0, false, err
	}
	return min.Int64, min.Valid, nil
}

func scanEpoch(sc scanner) (*domain.AuditEpoch, error) {
	var e domain.AuditEpoch
	var prev sql.NullString
	if err := sc.Scan(&e.Epoch, &e.Root, &prev, &e.LeafCount, &e.IntervalMillis, &e.SealedAt); err != nil {
		return nil, err
	}
	e.PrevRoot = prev.String
	return &e, nil
}

// epochArg keeps a nil epoch NULL. Writing 0 instead would enrol an unattested row in the
// first window of 1970 and hand the sealer something to seal that nobody asked it to.
func epochArg(e *int64) any {
	if e == nil {
		return nil
	}
	return *e
}

// nullStr keeps an absent prev_root NULL rather than ” — the first epoch in a chain has no
// predecessor, and ” would read as "chained to a root that hashes to nothing".
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
