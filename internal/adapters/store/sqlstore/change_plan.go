package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/proseria-research/lineage/internal/domain"
)

// Change-control-plan persistence (§22.6) and the edge scan §22.4 is computed from. The store
// gathers facts; ConformanceOf decides — no conformance column here to fall out of step.

const changePlanCols = `id,model_id,ref,summary,allowed_verdicts,allowed_methods,protocol_artifact_id,` +
	`effective_from,effective_to,declared_by,declared_at`

// CreateChangePlan appends a plan, closing the superseded one in the same transaction. The
// only UPDATE of change_plan in this file is that stamp.
//
// The model row is taken first with a no-op UPDATE: a portable write lock — a row lock on
// Postgres, the database write lock on SQLite — so two declarations for one model serialise
// and CheckPlanDeclaration reads a list nobody else is appending to.
func (s *Store) CreateChangePlan(ctx context.Context, p *domain.ChangePlan, supersedes string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, s.rb(`UPDATE model SET updated_at = updated_at WHERE id=?`), p.ModelID)
	if err := affected(res, err, "model"); err != nil {
		return err
	}
	existing, err := s.listChangePlans(ctx, tx, p.ModelID)
	if err != nil {
		return err
	}
	old, err := domain.CheckPlanDeclaration(existing, p, supersedes)
	if err != nil {
		return err
	}
	if old != nil {
		if _, err := tx.ExecContext(ctx, s.rb(
			`UPDATE change_plan SET effective_to=? WHERE id=? AND effective_to IS NULL`),
			p.EffectiveFrom, old.ID); err != nil {
			return err
		}
	}
	verdicts, _ := json.Marshal(p.AllowedVerdicts)
	var methods any
	if p.AllowedMethods != nil {
		b, _ := json.Marshal(p.AllowedMethods)
		methods = string(b)
	}
	if _, err := tx.ExecContext(ctx, s.rb(
		`INSERT INTO change_plan (`+changePlanCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`),
		p.ID, p.ModelID, p.Ref, p.Summary, string(verdicts), methods, p.ProtocolArtifactID,
		p.EffectiveFrom, p.EffectiveTo, p.DeclaredBy, p.DeclaredAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListChangePlans(ctx context.Context, modelID string) ([]*domain.ChangePlan, error) {
	return s.listChangePlans(ctx, s.db, modelID)
}

// querier is what *sql.DB and *sql.Tx share, so the declaration's in-transaction read and the
// plain read are one statement.
type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

func (s *Store) listChangePlans(ctx context.Context, db querier, modelID string) ([]*domain.ChangePlan, error) {
	q := `SELECT ` + changePlanCols + ` FROM change_plan`
	var args []any
	if modelID != "" {
		q += ` WHERE model_id=?`
		args = append(args, modelID)
	}
	q += ` ORDER BY effective_from DESC, id DESC`
	rows, err := db.QueryContext(ctx, s.rb(q), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []*domain.ChangePlan{}
	for rows.Next() {
		var (
			p        domain.ChangePlan
			verdicts string
			methods  sql.NullString
			to       sql.NullInt64
		)
		if err := rows.Scan(&p.ID, &p.ModelID, &p.Ref, &p.Summary, &verdicts, &methods,
			&p.ProtocolArtifactID, &p.EffectiveFrom, &to, &p.DeclaredBy, &p.DeclaredAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(verdicts), &p.AllowedVerdicts); err != nil {
			return nil, err
		}
		if methods.Valid {
			if err := json.Unmarshal([]byte(methods.String), &p.AllowedMethods); err != nil {
				return nil, err
			}
		}
		p.EffectiveTo = nullInt(to)
		out = append(out, &p)
	}
	return out, rows.Err()
}

// ListPlanDerivations gathers `derived_from` edges with both hash ladders (§22.4).
//
// The parent side is LEFT-joined through dst_id, as in ListDerivations: an edge to an external
// ref has no hashes, classifies `unknown`, and reads `undetermined` — queued, not passed. The
// plan-in-force is **not** joined here. It is chosen by domain.PlanInForce over the plans
// list, so the per-version read and the queue cannot choose differently.
func (s *Store) ListPlanDerivations(ctx context.Context, modelID, versionID string) ([]*domain.PlanDerivationRow, error) {
	args := []any{string(domain.RelDerivedFrom)}
	q := `SELECT m.id, m.name, v.id, v.name, v.created_at,
	             e.id, e.src_type, e.src_id, e.relation, e.dst_type, e.dst_id, e.dst_ref,
	             e.properties, e.created_at,
	             pm.name, pv.name,
	             pi.topology_hash, pi.shape_hash, pi.dtype_hash, pi.weights_hash,
	             ti.topology_hash, ti.shape_hash, ti.dtype_hash, ti.weights_hash
	      FROM lineage_edge e
	      JOIN model_version v ON v.id = e.src_id
	      JOIN model m ON m.id = v.model_id
	      LEFT JOIN model_version pv ON pv.id = e.dst_id
	      LEFT JOIN model pm ON pm.id = pv.model_id
	      LEFT JOIN version_insight pi ON pi.version_id = pv.id
	      LEFT JOIN version_insight ti ON ti.version_id = v.id
	      WHERE e.relation = ? AND e.src_type = 'model_version'`
	if modelID == "" {
		q += ` AND EXISTS (SELECT 1 FROM change_plan p WHERE p.model_id = m.id)`
	} else {
		q += ` AND m.id = ?`
		args = append(args, modelID)
	}
	if versionID != "" {
		q += ` AND v.id = ?`
		args = append(args, versionID)
	}
	q += ` ORDER BY v.created_at DESC, v.id DESC, e.id DESC`

	rows, err := s.db.QueryContext(ctx, s.rb(q), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []*domain.PlanDerivationRow{}
	for rows.Next() {
		var (
			d      domain.PlanDerivationRow
			e      domain.LineageEdge
			rel    string
			props  sql.NullString
			pm, pv sql.NullString
			fh, th [4]sql.NullString
		)
		if err := rows.Scan(
			&d.ModelID, &d.Model, &d.VersionID, &d.Version, &d.PublishedAt,
			&e.ID, &e.SrcType, &e.SrcID, &rel, &e.DstType, &e.DstID, &e.DstRef, &props, &e.CreatedAt,
			&pm, &pv,
			&fh[0], &fh[1], &fh[2], &fh[3],
			&th[0], &th[1], &th[2], &th[3],
		); err != nil {
			return nil, err
		}
		e.Relation = domain.LineageRelation(rel)
		e.Properties = fromNull(props)
		d.Edge = &e
		d.ParentModel, d.ParentVersion = pm.String, pv.String
		d.FromHashes = domain.Hashes{Topology: fh[0].String, Shape: fh[1].String, Dtype: fh[2].String, Weights: fh[3].String}
		d.ToHashes = domain.Hashes{Topology: th[0].String, Shape: th[1].String, Dtype: th[2].String, Weights: th[3].String}
		out = append(out, &d)
	}
	return out, rows.Err()
}
