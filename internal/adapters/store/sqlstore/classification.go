package sqlstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/proseria-research/lineage/internal/domain"
)

// Risk-classification persistence (§16.7.1). One row per (model, regime), replaced whole on
// write. The store holds what a person declared and nothing derived from it — staleness is
// computed on read (§16.5), so there is no state column here to fall out of date.

const classificationCols = `model_id,regime,eu_gpai_tier,eu_system_risk_class,` +
	`intended_purpose,basis,classified_at,classified_by,review_due_at`

// PutClassification replaces this model's row for this regime (§16.8: PUT, not PATCH).
//
// Delete-then-insert inside one transaction rather than ON CONFLICT DO UPDATE, matching
// UpsertInsight: it is the same one code path on both engines, and it gives replace-whole
// semantics for free — an omitted basis is *absent* from the new row rather than surviving
// an update list someone forgot to extend.
//
// Scoped to (model_id, regime), so a write under one regime cannot touch another's row or
// its classified_at anchor (§16.3.2).
func (s *Store) PutClassification(ctx context.Context, c *domain.RiskClassification) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, s.rb(
		`DELETE FROM classification WHERE model_id=? AND regime=?`), c.ModelID, string(c.Regime)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.rb(
		`INSERT INTO classification (`+classificationCols+`) VALUES (?,?,?,?,?,?,?,?,?)`),
		c.ModelID, string(c.Regime), nullEnum(string(c.EUGpaiTier)), nullEnum(string(c.EUSystemRiskClass)),
		c.IntendedPurpose, c.Basis, c.ClassifiedAt, c.ClassifiedBy, c.ReviewDueAt,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetClassification(ctx context.Context, modelID string, regime domain.Regime) (*domain.RiskClassification, error) {
	row := s.db.QueryRowContext(ctx, s.rb(
		`SELECT `+classificationCols+` FROM classification WHERE model_id=? AND regime=?`),
		modelID, string(regime))
	c, err := scanClassification(row)
	if errors.Is(err, sql.ErrNoRows) {
		// Absent is the `unclassified` state (§16.4), which the core reads rather than
		// surfaces as a failure.
		return nil, domain.NotFound("no " + string(regime) + " classification recorded for this model")
	}
	return c, err
}

func (s *Store) ListClassifications(ctx context.Context, modelID string) ([]*domain.RiskClassification, error) {
	rows, err := s.db.QueryContext(ctx, s.rb(
		`SELECT `+classificationCols+` FROM classification WHERE model_id=? ORDER BY regime`), modelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []*domain.RiskClassification{}
	for rows.Next() {
		c, err := scanClassification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanClassification(sc interface{ Scan(...any) error }) (*domain.RiskClassification, error) {
	var (
		c           domain.RiskClassification
		regime      string
		tier, class sql.NullString
		reviewDueAt sql.NullInt64
	)
	if err := sc.Scan(&c.ModelID, &regime, &tier, &class,
		&c.IntendedPurpose, &c.Basis, &c.ClassifiedAt, &c.ClassifiedBy, &reviewDueAt); err != nil {
		return nil, err
	}
	c.Regime = domain.Regime(regime)
	c.EUGpaiTier = domain.EUGpaiTier(tier.String)
	c.EUSystemRiskClass = domain.EUSystemRiskClass(class.String)
	if reviewDueAt.Valid {
		v := reviewDueAt.Int64
		c.ReviewDueAt = &v
	}
	return &c, nil
}

// nullEnum keeps a regime's unused enum columns NULL rather than empty-string, which is what
// the §16.7.1 CHECK reads to tell one regime's group from another's. An empty string would
// satisfy IS NOT NULL and quietly defeat the constraint.
func nullEnum(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// DriftFactsFor gathers the §16.5 aggregates in one pass over the model's versions. Both
// MAXes come from the same scan, which the (model_id, stage) index from §02.6 already
// supports — §16.5's claim that staleness stays a list query rather than a per-row audit
// scan rests on this staying one statement.
//
// COALESCE to 0 means "no such row", which the predicate reads as an absent fact.
func (s *Store) DriftFactsFor(ctx context.Context, modelID string) (domain.DriftFacts, error) {
	var f domain.DriftFacts
	err := s.db.QueryRowContext(ctx, s.rb(`
		SELECT COALESCE(MAX(created_at), 0),
		       COALESCE(MAX(CASE WHEN stage = 'production' THEN updated_at END), 0)
		FROM model_version WHERE model_id = ?`), modelID,
	).Scan(&f.LatestVersionCreatedAt, &f.LatestProductionUpdatedAt)
	if err != nil {
		return domain.DriftFacts{}, err
	}
	// LatestOpenReviewCreatedAt stays zero until M16 adds modification_review (`17.4`).
	// Zero is also the honest answer for an install with no open reviews.
	return f, nil
}
