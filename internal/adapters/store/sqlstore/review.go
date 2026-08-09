package sqlstore

import (
	"context"
	"database/sql"

	"github.com/proseria-research/lineage/internal/domain"
)

// Modification-review persistence (§17.5.1) and the join the §17.4 queue is computed from.
// The store gathers facts; ReviewEligible decides — there is no `status` column here to fall
// out of step with the verdict, exactly as there is no `state` column on classification.

const reviewCols = `id,version_id,edge_id,verdict_at_review,outcome,note,reviewed_by,reviewed_at`

// CreateReview appends. There is no update or delete path anywhere in this file (§17.4).
func (s *Store) CreateReview(ctx context.Context, r *domain.ModificationReview) error {
	_, err := s.db.ExecContext(ctx, s.rb(
		`INSERT INTO modification_review (`+reviewCols+`) VALUES (?,?,?,?,?,?,?,?)`),
		r.ID, r.VersionID, r.EdgeID, string(r.VerdictAtReview), string(r.Outcome),
		r.Note, r.ReviewedBy, r.ReviewedAt)
	return err
}

// ListReviews returns a version's reviews newest-first, across every edge. The id tiebreak
// keeps two reviews recorded in the same millisecond in a stable order — ULIDs are
// monotonic, so it agrees with insertion order rather than merely being deterministic.
func (s *Store) ListReviews(ctx context.Context, versionID string) ([]*domain.ModificationReview, error) {
	rows, err := s.db.QueryContext(ctx, s.rb(
		`SELECT `+reviewCols+` FROM modification_review WHERE version_id=?
		 ORDER BY reviewed_at DESC, id DESC`), versionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []*domain.ModificationReview{}
	for rows.Next() {
		var (
			r                domain.ModificationReview
			verdict, outcome string
		)
		if err := rows.Scan(&r.ID, &r.VersionID, &r.EdgeID, &verdict, &outcome,
			&r.Note, &r.ReviewedBy, &r.ReviewedAt); err != nil {
			return nil, err
		}
		r.VerdictAtReview, r.Outcome = domain.Verdict(verdict), domain.ReviewOutcome(outcome)
		out = append(out, &r)
	}
	return out, rows.Err()
}

// ListDerivations gathers every `derived_from` edge on a classified model, with both hash
// ladders and the latest review for the pair (§17.4).
//
// Shape notes, since this is the widest join in the tree:
//
//   - The classification join is INNER. That is condition 3 as a join rather than a filter,
//     which bounds the scan to classified models on an install where most are not.
//   - The parent side is LEFT-joined through dst_id. A derived_from edge pointing at an
//     external ref has no parent row and no hashes, so it classifies `unknown` and queues —
//     which is the fine-tune-a-third-party-model case Art. 25 is actually about.
//   - The latest review is a correlated scalar subquery rather than a window function or a
//     GROUP BY: both engines plan it off idx_review_pair, and it stays one statement.
func (s *Store) ListDerivations(ctx context.Context, regime domain.Regime, modelID string) ([]*domain.DerivationRow, error) {
	// Statement order, not logical order: the regime placeholder sits inside the JOIN
	// condition and the relation in the WHERE, and positional rebinding on Postgres numbers
	// them as they appear in the text.
	args := []any{string(regime), string(domain.RelDerivedFrom)}
	q := `SELECT m.id, m.name, v.id, v.name,
	             e.id, e.src_type, e.src_id, e.relation, e.dst_type, e.dst_id, e.dst_ref,
	             e.properties, e.created_at,
	             pm.name, pv.name,
	             pi.topology_hash, pi.shape_hash, pi.dtype_hash, pi.weights_hash,
	             ti.topology_hash, ti.shape_hash, ti.dtype_hash, ti.weights_hash,
	             c.eu_system_risk_class, c.eu_gpai_tier,
	             r.id, r.verdict_at_review, r.outcome, r.note, r.reviewed_by, r.reviewed_at
	      FROM lineage_edge e
	      JOIN model_version v ON v.id = e.src_id
	      JOIN model m ON m.id = v.model_id
	      JOIN classification c ON c.model_id = m.id AND c.regime = ?
	      LEFT JOIN model_version pv ON pv.id = e.dst_id
	      LEFT JOIN model pm ON pm.id = pv.model_id
	      LEFT JOIN version_insight pi ON pi.version_id = pv.id
	      LEFT JOIN version_insight ti ON ti.version_id = v.id
	      LEFT JOIN modification_review r ON r.id = (
	          SELECT r2.id FROM modification_review r2
	          WHERE r2.version_id = v.id AND r2.edge_id = e.id
	          ORDER BY r2.reviewed_at DESC, r2.id DESC LIMIT 1
	      )
	      WHERE e.relation = ? AND e.src_type = 'model_version'`
	if modelID != "" {
		q += ` AND m.id = ?`
		args = append(args, modelID)
	}
	q += ` ORDER BY e.created_at DESC, e.id DESC`

	rows, err := s.db.QueryContext(ctx, s.rb(q), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []*domain.DerivationRow{}
	for rows.Next() {
		d, err := scanDerivation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanDerivation(rows *sql.Rows) (*domain.DerivationRow, error) {
	var (
		d        domain.DerivationRow
		e        domain.LineageEdge
		rel      string
		props    sql.NullString
		pm, pv   sql.NullString
		fh, th   [4]sql.NullString
		class    sql.NullString
		tier     sql.NullString
		rID      sql.NullString
		rVerdict sql.NullString
		rOutcome sql.NullString
		rNote    sql.NullString
		rBy      sql.NullString
		rAt      sql.NullInt64
	)
	if err := rows.Scan(
		&d.ModelID, &d.Model, &d.VersionID, &d.Version,
		&e.ID, &e.SrcType, &e.SrcID, &rel, &e.DstType, &e.DstID, &e.DstRef, &props, &e.CreatedAt,
		&pm, &pv,
		&fh[0], &fh[1], &fh[2], &fh[3],
		&th[0], &th[1], &th[2], &th[3],
		&class, &tier,
		&rID, &rVerdict, &rOutcome, &rNote, &rBy, &rAt,
	); err != nil {
		return nil, err
	}
	e.Relation = domain.LineageRelation(rel)
	e.Properties = fromNull(props)
	d.Edge = &e
	d.ParentModel, d.ParentVersion = pm.String, pv.String
	d.FromHashes = domain.Hashes{Topology: fh[0].String, Shape: fh[1].String, Dtype: fh[2].String, Weights: fh[3].String}
	d.ToHashes = domain.Hashes{Topology: th[0].String, Shape: th[1].String, Dtype: th[2].String, Weights: th[3].String}
	d.EUSystemRiskClass = domain.EUSystemRiskClass(class.String)
	d.EUGpaiTier = domain.EUGpaiTier(tier.String)
	if rID.Valid {
		d.LatestReview = &domain.ModificationReview{
			ID: rID.String, VersionID: d.VersionID, EdgeID: e.ID,
			VerdictAtReview: domain.Verdict(rVerdict.String),
			Outcome:         domain.ReviewOutcome(rOutcome.String),
			Note:            rNote.String, ReviewedBy: rBy.String, ReviewedAt: rAt.Int64,
		}
	}
	return &d, nil
}
