package sqlstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/proseria-research/lineage/internal/domain"
)

// Model-risk validation persistence (§20.8.2) and the inventory join §20.7 is computed from.
// The store gathers facts; MRMStateOf decides — no `state` column here either.

const validationCols = `id,version_id,outcome,scope,findings,conditions,conditions_cleared_at,` +
	`valid_until,evidence_artifact_id,validated_by,validated_at`

// CreateValidation appends. The only UPDATE in this file is ClearValidationConditions.
func (s *Store) CreateValidation(ctx context.Context, v *domain.Validation) error {
	_, err := s.q.ExecContext(ctx, s.rb(
		`INSERT INTO validation (`+validationCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`),
		v.ID, v.VersionID, string(v.Outcome), v.Scope, v.Findings, v.Conditions,
		v.ConditionsClearedAt, v.ValidUntil, v.EvidenceArtifactID, v.ValidatedBy, v.ValidatedAt)
	return err
}

// ListValidations returns a version's validations newest-first. The id tiebreak orders two
// rows in one millisecond stably, not correctly — the same trade ListReviews makes.
func (s *Store) ListValidations(ctx context.Context, versionID string) ([]*domain.Validation, error) {
	rows, err := s.q.QueryContext(ctx, s.rb(
		`SELECT `+validationCols+` FROM validation WHERE version_id=?
		 ORDER BY validated_at DESC, id DESC`), versionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []*domain.Validation{}
	for rows.Next() {
		var (
			v              domain.Validation
			outcome        string
			cleared, until sql.NullInt64
		)
		if err := rows.Scan(&v.ID, &v.VersionID, &outcome, &v.Scope, &v.Findings, &v.Conditions,
			&cleared, &until, &v.EvidenceArtifactID, &v.ValidatedBy, &v.ValidatedAt); err != nil {
			return nil, err
		}
		v.Outcome = domain.ValidationOutcome(outcome)
		v.ConditionsClearedAt = nullInt(cleared)
		v.ValidUntil = nullInt(until)
		out = append(out, &v)
	}
	return out, rows.Err()
}

// ClearValidationConditions sets conditions_cleared_at once. The IS NULL guard is in the
// statement, not a read before it, so two concurrent clears cannot both move the timestamp.
func (s *Store) ClearValidationConditions(ctx context.Context, versionID, id string, at int64) error {
	res, err := s.q.ExecContext(ctx, s.rb(
		`UPDATE validation SET conditions_cleared_at=?
		 WHERE id=? AND version_id=? AND conditions_cleared_at IS NULL`), at, id, versionID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	// Nothing updated: either the row is not there, or it was already cleared.
	var one int
	err = s.q.QueryRowContext(ctx, s.rb(
		`SELECT 1 FROM validation WHERE id=? AND version_id=?`), id, versionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NotFound("validation '" + id + "' not found on this version")
	}
	if err != nil {
		return err
	}
	return domain.Precondition("conditions already cleared", map[string]any{"reason": "already_cleared"})
}

// ListMRMInventory answers "which tier-1 models are stale?" in one statement (§20.9.2).
//
// Shape notes:
//
//   - The classification join is LEFT, on the `mrm` regime only, so untiered models are still
//     returned and an EU row can never be mistaken for this regime's (§16.3.2).
//   - The **subject version** — production if there is one, else the newest — is a correlated
//     scalar subquery in the join, the same pattern ListDerivations uses for the latest review.
//     This statement is the one place the choice is made; the single-model read goes through
//     it too (MRMFilter.ModelID).
//   - The latest validation hangs off the subject the same way, and clause 3's evaluation
//     aggregate is a scalar MAX over idx_eval_run_at.
func (s *Store) ListMRMInventory(ctx context.Context, o domain.ListOptions, f domain.MRMFilter) ([]*domain.MRMInventoryRow, error) {
	where, args, labelsPushed, err := s.modelWhere(o, "m")
	if err != nil {
		return nil, err
	}

	joinArgs := []any{string(domain.RegimeMRM), string(domain.StageProduction)}
	q := `SELECT ` + prefixCols(modelSel, "m") + `,
		       c.model_id, c.mrm_tier, c.intended_purpose, c.basis, c.classified_at, c.classified_by, c.review_due_at,
		       sv.id, sv.name, sv.author, sv.stage, sv.stage_changed_at,
		       COALESCE(agg.latest_created, 0),
		       COALESCE((SELECT MAX(e.run_at) FROM evaluation e WHERE e.version_id = sv.id), 0),
		       val.id, val.outcome, val.scope, val.findings, val.conditions, val.conditions_cleared_at,
		       val.valid_until, val.evidence_artifact_id, val.validated_by, val.validated_at
		FROM model m
		LEFT JOIN classification c ON c.model_id = m.id AND c.regime = ?
		LEFT JOIN (
			SELECT model_id, MAX(created_at) AS latest_created FROM model_version GROUP BY model_id
		) agg ON agg.model_id = m.id
		LEFT JOIN model_version sv ON sv.id = (
			SELECT v2.id FROM model_version v2 WHERE v2.model_id = m.id
			ORDER BY CASE WHEN v2.stage = ? THEN 0 ELSE 1 END, v2.created_at DESC, v2.id DESC LIMIT 1
		)
		LEFT JOIN validation val ON val.id = (
			SELECT x.id FROM validation x WHERE x.version_id = sv.id
			ORDER BY x.validated_at DESC, x.id DESC LIMIT 1
		)
		WHERE 1=1` + where

	if f.Tier != "" {
		q += ` AND c.mrm_tier = ?`
		args = append(args, string(f.Tier))
	}
	if f.ModelID != "" {
		q += ` AND m.id = ?`
		args = append(args, f.ModelID)
	}
	q += ` ORDER BY m.created_at DESC, m.id DESC`

	rows, err := s.q.QueryContext(ctx, s.rb(q), append(joinArgs, args...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []*domain.MRMInventoryRow{}
	for rows.Next() {
		r, err := scanMRMRow(rows)
		if err != nil {
			return nil, err
		}
		if !labelsPushed && !hasLabels(r.Model.Labels, o.Labels) {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanMRMRow(rows *sql.Rows) (*domain.MRMInventoryRow, error) {
	var (
		m                           domain.Model
		state, labels               string
		cp                          sql.NullString
		heldSince                   sql.NullInt64
		heldBy                      sql.NullString
		cModelID, tier              sql.NullString
		purpose, basis, by          sql.NullString
		classifiedAt, reviewDueAt   sql.NullInt64
		svID, svName, svAuthor      sql.NullString
		svStage                     sql.NullString
		svStageChanged              sql.NullInt64
		latestCreated, latestEvalAt int64
		valID, outcome, scope       sql.NullString
		findings, conditions        sql.NullString
		cleared, until              sql.NullInt64
		evidence, validatedBy       sql.NullString
		validatedAt                 sql.NullInt64
	)
	if err := rows.Scan(
		&m.ID, &m.Name, &m.Description, &m.Owner, &state, &labels, &cp, &m.CreatedAt, &m.UpdatedAt, &heldSince, &heldBy,
		&cModelID, &tier, &purpose, &basis, &classifiedAt, &by, &reviewDueAt,
		&svID, &svName, &svAuthor, &svStage, &svStageChanged,
		&latestCreated, &latestEvalAt,
		&valID, &outcome, &scope, &findings, &conditions, &cleared, &until, &evidence, &validatedBy, &validatedAt,
	); err != nil {
		return nil, err
	}
	m.State = domain.ModelState(state)
	m.Labels = unmarshalMap(labels)
	m.CustomProperties = fromNull(cp)
	m.LegalHold = scanHold(heldSince, heldBy)

	r := &domain.MRMInventoryRow{Model: &m, Facts: domain.MRMFacts{LatestVersionCreatedAt: latestCreated}}
	if cModelID.Valid {
		r.Classification = &domain.RiskClassification{
			ModelID: cModelID.String, Regime: domain.RegimeMRM, MRMTier: domain.MRMTier(tier.String),
			IntendedPurpose: purpose.String, Basis: basis.String,
			ClassifiedAt: classifiedAt.Int64, ClassifiedBy: by.String, ReviewDueAt: nullInt(reviewDueAt),
		}
	}
	if svID.Valid {
		r.Facts.Subject = &domain.MRMSubject{
			VersionID: svID.String, Version: svName.String, Author: svAuthor.String,
			Stage: domain.Stage(svStage.String), StageChangedAt: svStageChanged.Int64,
		}
		// Only meaningful with a subject; zero otherwise, which reads as "none".
		r.Facts.LatestEvaluationRunAt = latestEvalAt
	}
	if valID.Valid {
		r.Facts.Validation = &domain.Validation{
			ID: valID.String, VersionID: svID.String, Outcome: domain.ValidationOutcome(outcome.String),
			Scope: scope.String, Findings: findings.String, Conditions: conditions.String,
			ConditionsClearedAt: nullInt(cleared), ValidUntil: nullInt(until),
			EvidenceArtifactID: evidence.String, ValidatedBy: validatedBy.String, ValidatedAt: validatedAt.Int64,
		}
	}
	return r, nil
}

// nullInt turns a nullable column into the *int64 the domain uses for "unset".
func nullInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
