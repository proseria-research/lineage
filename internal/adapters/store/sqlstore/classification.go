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

const classificationCols = `model_id,regime,eu_gpai_tier,eu_system_risk_class,mrm_tier,` +
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
	// Joins a caller's unit of work when there is one (InTx), so this commits with it.
	return s.inTx(ctx, func(t *Store) error {
		tx := t.q

		if _, err := tx.ExecContext(ctx, s.rb(
			`DELETE FROM classification WHERE model_id=? AND regime=?`), c.ModelID, string(c.Regime)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.rb(
			`INSERT INTO classification (`+classificationCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`),
			c.ModelID, string(c.Regime), nullEnum(string(c.EUGpaiTier)), nullEnum(string(c.EUSystemRiskClass)),
			nullEnum(string(c.MRMTier)),
			c.IntendedPurpose, c.Basis, c.ClassifiedAt, c.ClassifiedBy, c.ReviewDueAt,
		); err != nil {
			return err
		}
		return nil
	})
}

func (s *Store) GetClassification(ctx context.Context, modelID string, regime domain.Regime) (*domain.RiskClassification, error) {
	row := s.q.QueryRowContext(ctx, s.rb(
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
	rows, err := s.q.QueryContext(ctx, s.rb(
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
		c                    domain.RiskClassification
		regime               string
		tier, class, mrmTier sql.NullString
		reviewDueAt          sql.NullInt64
	)
	if err := sc.Scan(&c.ModelID, &regime, &tier, &class, &mrmTier,
		&c.IntendedPurpose, &c.Basis, &c.ClassifiedAt, &c.ClassifiedBy, &reviewDueAt); err != nil {
		return nil, err
	}
	c.Regime = domain.Regime(regime)
	c.EUGpaiTier = domain.EUGpaiTier(tier.String)
	c.EUSystemRiskClass = domain.EUSystemRiskClass(class.String)
	c.MRMTier = domain.MRMTier(mrmTier.String)
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

// DriftFactsFor gathers the §16.5 aggregates in one statement: the newest version's creation
// time, when the production version entered production, and the newest artifact on it. The
// (model_id, stage) index (§02.6) and artifact's (version_id, name) key serve both parts, so
// staleness stays a list query rather than a per-row audit scan.
//
// COALESCE to 0 means "no such row", which the predicate reads as an absent fact.
func (s *Store) DriftFactsFor(ctx context.Context, modelID string) (domain.DriftFacts, error) {
	var f domain.DriftFacts
	var entered, artifact int64
	err := s.q.QueryRowContext(ctx, s.rb(`
		SELECT COALESCE(MAX(v.created_at), 0),
		       COALESCE(MAX(CASE WHEN v.stage = 'production' THEN v.stage_changed_at END), 0),
		       COALESCE((SELECT MAX(a.created_at) FROM artifact a
		                 JOIN model_version pv ON a.version_id = pv.id
		                 WHERE pv.model_id = ? AND pv.stage = 'production'), 0)
		FROM model_version v WHERE v.model_id = ?`), modelID, modelID,
	).Scan(&f.LatestVersionCreatedAt, &entered, &artifact)
	if err != nil {
		return domain.DriftFacts{}, err
	}
	f.ProductionChangedAt = max(entered, artifact)
	// LatestOpenReviewCreatedAt stays zero until M16 adds modification_review (`17.4`).
	// Zero is also the honest answer for an install with no open reviews.
	return f, nil
}

// ListInventory answers "which high-risk models do we have?" in one statement (§16.8.2).
//
// The classification join is LEFT, so unclassified models are still returned — `unclassified`
// is a state to filter *for*, not an absence to drop (§16.4). An enum filter turns the join
// inner in effect, because a null column never equals a value; that is correct, since asking
// for high_annex_iii cannot mean "and also the models nobody classified".
//
// The version aggregates come from a grouped subquery rather than correlated scalars so the
// model rows stay distinct without a DISTINCT, and each engine gets one pass over
// model_version using the (model_id, stage) index (§02.6, §16.5).
func (s *Store) ListInventory(ctx context.Context, o domain.ListOptions, f domain.ClassificationFilter) ([]*domain.ModelInventoryRow, error) {
	where, args, labelsPushed, err := s.modelWhere(o, "m")
	if err != nil {
		return nil, err
	}

	// The regime is bound first because it is inside the JOIN condition, not the WHERE:
	// moving it to WHERE would turn the LEFT JOIN inner and hide unclassified models.
	joinArgs := []any{string(f.Regime)}
	q := `SELECT ` + prefixCols(modelSel, "m") + `,
		       c.model_id, c.regime, c.eu_gpai_tier, c.eu_system_risk_class,
		       c.intended_purpose, c.basis, c.classified_at, c.classified_by, c.review_due_at,
		       COALESCE(v.latest_created, 0), COALESCE(v.prod_entered, 0), COALESCE(pa.prod_artifact, 0)
		FROM model m
		LEFT JOIN classification c ON c.model_id = m.id AND c.regime = ?
		LEFT JOIN (
			SELECT model_id,
			       MAX(created_at) AS latest_created,
			       MAX(CASE WHEN stage = 'production' THEN stage_changed_at END) AS prod_entered
			FROM model_version GROUP BY model_id
		) v ON v.model_id = m.id
		LEFT JOIN (
			SELECT pv.model_id, MAX(a.created_at) AS prod_artifact
			FROM artifact a JOIN model_version pv ON a.version_id = pv.id
			WHERE pv.stage = 'production'
			GROUP BY pv.model_id
		) pa ON pa.model_id = m.id
		WHERE 1=1` + where

	if f.EUSystemRiskClass != "" {
		q += ` AND c.eu_system_risk_class = ?`
		args = append(args, string(f.EUSystemRiskClass))
	}
	if f.EUGpaiTier != "" {
		q += ` AND c.eu_gpai_tier = ?`
		args = append(args, string(f.EUGpaiTier))
	}
	q += ` ORDER BY m.created_at DESC, m.id DESC`

	rows, err := s.q.QueryContext(ctx, s.rb(q), append(joinArgs, args...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []*domain.ModelInventoryRow{}
	for rows.Next() {
		m, c, facts, err := scanInventoryRow(rows)
		if err != nil {
			return nil, err
		}
		// Labels the dialect could not push down are still filtered here, exactly as
		// ListModels does — the shared modelWhere reports which case we are in.
		if !labelsPushed && !hasLabels(m.Labels, o.Labels) {
			continue
		}
		out = append(out, &domain.ModelInventoryRow{Model: m, Classification: c, Facts: facts})
	}
	return out, rows.Err()
}

func scanInventoryRow(rows *sql.Rows) (*domain.Model, *domain.RiskClassification, domain.DriftFacts, error) {
	var (
		m                          domain.Model
		state, labels              string
		cp                         sql.NullString
		heldSince                  sql.NullInt64
		heldBy                     sql.NullString
		cModelID, cRegime          sql.NullString
		tier, class                sql.NullString
		purpose, basis, by         sql.NullString
		classifiedAt, reviewDueAt  sql.NullInt64
		latestCreated, prodEntered int64
		prodArtifact               int64
	)
	if err := rows.Scan(
		&m.ID, &m.Name, &m.Description, &m.Owner, &state, &labels, &cp, &m.CreatedAt, &m.UpdatedAt, &heldSince, &heldBy,
		&cModelID, &cRegime, &tier, &class, &purpose, &basis, &classifiedAt, &by, &reviewDueAt,
		&latestCreated, &prodEntered, &prodArtifact,
	); err != nil {
		return nil, nil, domain.DriftFacts{}, err
	}
	m.State = domain.ModelState(state)
	m.Labels = unmarshalMap(labels)
	m.CustomProperties = fromNull(cp)
	m.LegalHold = scanHold(heldSince, heldBy)

	facts := domain.DriftFacts{
		LatestVersionCreatedAt: latestCreated,
		ProductionChangedAt:    max(prodEntered, prodArtifact),
		// LatestOpenReviewCreatedAt stays zero until M16 (`17.4`).
	}
	if !cModelID.Valid {
		return &m, nil, facts, nil // no row for this regime — unclassified (§16.4)
	}
	c := &domain.RiskClassification{
		ModelID:           cModelID.String,
		Regime:            domain.Regime(cRegime.String),
		EUGpaiTier:        domain.EUGpaiTier(tier.String),
		EUSystemRiskClass: domain.EUSystemRiskClass(class.String),
		IntendedPurpose:   purpose.String,
		Basis:             basis.String,
		ClassifiedAt:      classifiedAt.Int64,
		ClassifiedBy:      by.String,
	}
	if reviewDueAt.Valid {
		v := reviewDueAt.Int64
		c.ReviewDueAt = &v
	}
	return &m, c, facts, nil
}
