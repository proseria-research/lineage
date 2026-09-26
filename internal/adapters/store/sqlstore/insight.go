package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/proseria-research/lineage/internal/domain"
)

// Model-insight persistence (§11.3). The store only holds what producers submitted; it
// derives nothing. Nullable numeric columns map to *int64 so a merge can distinguish an
// absent fact from an explicit zero (§11.6.1).

const insightCols = `version_id,framework_name,framework_version,producer_name,producer_version,` +
	`param_count_total,param_count_trainable,param_count_method,tensor_count,dtype_dominant,` +
	`quant_method,quant_scope,disk_bytes,weights_bytes,topology_hash,shape_hash,dtype_hash,` +
	`weights_hash,arch_doc,source,field_sources,reporter_name,reporter_version,coverage,` +
	`created_at,updated_at`

func (s *Store) GetInsight(ctx context.Context, versionID string) (*domain.VersionInsight, error) {
	row := s.q.QueryRowContext(ctx, s.rb(
		`SELECT `+insightCols+` FROM version_insight WHERE version_id=?`), versionID)
	in, err := scanInsight(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("no insight recorded for this version")
	}
	return in, err
}

// UpsertInsight writes the whole row. The core has already merged field-by-field, so the
// value handed here is the intended final state (§11.6.1).
func (s *Store) UpsertInsight(ctx context.Context, in *domain.VersionInsight) error {
	fw, pr := in.Framework, in.Producer
	if fw == nil {
		fw = &domain.NameVersion{}
	}
	if pr == nil {
		pr = &domain.NameVersion{}
	}
	args := []any{
		in.VersionID, fw.Name, fw.Version, pr.Name, pr.Version,
		in.ParamCountTotal, in.ParamCountTrainable, string(in.ParamCountMethod), in.TensorCount,
		string(in.DtypeDominant), in.QuantMethod, jsonText(in.QuantScope), in.DiskBytes, in.WeightsBytes,
		in.Hashes.Topology, in.Hashes.Shape, in.Hashes.Dtype, in.Hashes.Weights,
		jsonText(in.ArchDoc), string(in.Source), jsonText(marshalJSON(in.FieldSources)),
		in.ReporterName, in.ReporterVersion, jsonText(marshalJSON(in.Coverage)),
		in.CreatedAt, in.UpdatedAt,
	}
	// No ON CONFLICT: the syntax is portable but the update list is long and both engines
	// serialize writes for a single version, so delete-then-insert keeps one code path.
	// Joins a caller's unit of work when there is one (InTx), so this commits with it.
	return s.inTx(ctx, func(t *Store) error {
		tx := t.q
		if _, err := tx.ExecContext(ctx, s.rb(`DELETE FROM version_insight WHERE version_id=?`), in.VersionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.rb(
			`INSERT INTO version_insight (`+insightCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
			args...); err != nil {
			return err
		}
		return nil
	})
}

const layerCols = "version_id,ordinal,path,op_type,repeat_count,shape_signature,dtype,param_count,bytes"

// ReplaceLayerBlocks swaps the whole set in one transaction — the breakdown is a list, so
// there is no per-element merge (§11.6.1).
func (s *Store) ReplaceLayerBlocks(ctx context.Context, versionID string, blocks []*domain.LayerBlock) error {
	// Joins a caller's unit of work when there is one (InTx), so this commits with it.
	return s.inTx(ctx, func(t *Store) error {
		tx := t.q
		if _, err := tx.ExecContext(ctx, s.rb(`DELETE FROM layer_block WHERE version_id=?`), versionID); err != nil {
			return err
		}
		for _, b := range blocks {
			if _, err := tx.ExecContext(ctx, s.rb(
				`INSERT INTO layer_block (`+layerCols+`) VALUES (?,?,?,?,?,?,?,?,?)`),
				versionID, b.Ordinal, b.Path, b.OpType, b.RepeatCount, b.ShapeSignature, b.Dtype,
				b.ParamCount, b.Bytes); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ListLayerBlocks(ctx context.Context, versionID string) ([]*domain.LayerBlock, error) {
	rows, err := s.q.QueryContext(ctx, s.rb(
		`SELECT `+layerCols+` FROM layer_block WHERE version_id=? ORDER BY ordinal`), versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.LayerBlock
	for rows.Next() {
		var b domain.LayerBlock
		if err := rows.Scan(&b.VersionID, &b.Ordinal, &b.Path, &b.OpType, &b.RepeatCount,
			&b.ShapeSignature, &b.Dtype, &b.ParamCount, &b.Bytes); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

const footprintCols = `id,version_id,scenario,device_class,batch,seq_len,weights_bytes,kv_cache_bytes,` +
	`activation_bytes,runtime_overhead_bytes,total_bytes,source,basis,created_at,updated_at`

// UpsertFootprint replaces the row for (version, scenario): a re-measurement of the same
// scenario supersedes the previous one, unlike evaluations which append (§11.6.1).
func (s *Store) UpsertFootprint(ctx context.Context, f *domain.Footprint) error {
	// Joins a caller's unit of work when there is one (InTx), so this commits with it.
	return s.inTx(ctx, func(t *Store) error {
		tx := t.q
		if _, err := tx.ExecContext(ctx, s.rb(
			`DELETE FROM footprint WHERE version_id=? AND scenario=?`), f.VersionID, f.Scenario); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.rb(
			`INSERT INTO footprint (`+footprintCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
			f.ID, f.VersionID, f.Scenario, f.DeviceClass, f.Batch, f.SeqLen, f.WeightsBytes,
			f.KVCacheBytes, f.ActivationBytes, f.RuntimeOverheadBytes, f.TotalBytes,
			string(f.Source), jsonText(f.Basis), f.CreatedAt, f.UpdatedAt); err != nil {
			return err
		}
		return nil
	})
}

func (s *Store) ListFootprints(ctx context.Context, versionID string) ([]*domain.Footprint, error) {
	rows, err := s.q.QueryContext(ctx, s.rb(
		`SELECT `+footprintCols+` FROM footprint WHERE version_id=? ORDER BY scenario`), versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Footprint
	for rows.Next() {
		var f domain.Footprint
		var src string
		var basis sql.NullString
		if err := rows.Scan(&f.ID, &f.VersionID, &f.Scenario, &f.DeviceClass, &f.Batch, &f.SeqLen,
			&f.WeightsBytes, &f.KVCacheBytes, &f.ActivationBytes, &f.RuntimeOverheadBytes,
			&f.TotalBytes, &src, &basis, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.Source = domain.FootprintSource(src)
		f.Basis = fromNull(basis)
		out = append(out, &f)
	}
	return out, rows.Err()
}

const evalCols = `id,version_id,suite,metric,split,value,higher_is_better,n_samples,harness_name,` +
	`harness_version,params,evidence_artifact_id,source,run_at,created_at`

func (s *Store) CreateEvaluation(ctx context.Context, e *domain.Evaluation) error {
	_, err := s.q.ExecContext(ctx, s.rb(
		`INSERT INTO evaluation (`+evalCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		e.ID, e.VersionID, e.Suite, e.Metric, e.Split, e.Value, e.HigherIsBetter, e.NSamples,
		e.HarnessName, e.HarnessVersion, jsonText(e.Params), e.EvidenceArtifactID,
		string(e.Source), e.RunAt, e.CreatedAt)
	return err
}

func (s *Store) ListEvaluations(ctx context.Context, versionID string) ([]*domain.Evaluation, error) {
	rows, err := s.q.QueryContext(ctx, s.rb(
		`SELECT `+evalCols+` FROM evaluation WHERE version_id=? ORDER BY run_at DESC, created_at DESC, id DESC`),
		versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Evaluation
	for rows.Next() {
		var e domain.Evaluation
		var src string
		var params sql.NullString
		if err := rows.Scan(&e.ID, &e.VersionID, &e.Suite, &e.Metric, &e.Split, &e.Value,
			&e.HigherIsBetter, &e.NSamples, &e.HarnessName, &e.HarnessVersion, &params,
			&e.EvidenceArtifactID, &src, &e.RunAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Source = domain.FactSource(src)
		e.Params = fromNull(params)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// ---- scan helpers ----

func scanInsight(sc scanner) (*domain.VersionInsight, error) {
	var in domain.VersionInsight
	var fw, pr domain.NameVersion
	var method, dtype, src string
	var quantScope, archDoc, fieldSources, coverage sql.NullString
	if err := sc.Scan(&in.VersionID, &fw.Name, &fw.Version, &pr.Name, &pr.Version,
		&in.ParamCountTotal, &in.ParamCountTrainable, &method, &in.TensorCount, &dtype,
		&in.QuantMethod, &quantScope, &in.DiskBytes, &in.WeightsBytes,
		&in.Hashes.Topology, &in.Hashes.Shape, &in.Hashes.Dtype, &in.Hashes.Weights,
		&archDoc, &src, &fieldSources, &in.ReporterName, &in.ReporterVersion, &coverage,
		&in.CreatedAt, &in.UpdatedAt); err != nil {
		return nil, err
	}
	if fw.Name != "" || fw.Version != "" {
		in.Framework = &fw
	}
	if pr.Name != "" || pr.Version != "" {
		in.Producer = &pr
	}
	in.ParamCountMethod = domain.ParamCountMethod(method)
	in.DtypeDominant = domain.Dtype(dtype)
	in.Source = domain.FactSource(src)
	in.QuantScope = fromNull(quantScope)
	in.ArchDoc = fromNull(archDoc)
	if fs := fromNull(fieldSources); fs != nil {
		_ = json.Unmarshal(fs, &in.FieldSources)
	}
	if c := fromNull(coverage); c != nil {
		_ = json.Unmarshal(c, &in.Coverage)
	}
	return &in, nil
}

// marshalJSON renders a map to JSON, or nil when empty so the column stays NULL.
func marshalJSON(v any) json.RawMessage {
	switch m := v.(type) {
	case map[string]string:
		if len(m) == 0 {
			return nil
		}
	case map[string]domain.FieldSource:
		if len(m) == 0 {
			return nil
		}
	case nil:
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
