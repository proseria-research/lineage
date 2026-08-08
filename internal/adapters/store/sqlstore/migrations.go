package sqlstore

import "context"

// migrations is the ordered, forward-only DDL. Index+1 = schema version. The base schema
// is the portable subset shared by both engines (§02.7); dialect-specific additions
// (JSONB/GIN on Postgres) can be appended per-dialect later. One statement per entry.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS model (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		description TEXT NOT NULL DEFAULT '',
		owner TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL,
		labels TEXT NOT NULL DEFAULT '{}',
		custom_properties TEXT,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS model_version (
		id TEXT PRIMARY KEY,
		model_id TEXT NOT NULL REFERENCES model(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		author TEXT NOT NULL DEFAULT '',
		stage TEXT NOT NULL,
		labels TEXT NOT NULL DEFAULT '{}',
		custom_properties TEXT,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		UNIQUE (model_id, name)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_version_model_stage ON model_version (model_id, stage)`,
	`CREATE TABLE IF NOT EXISTS artifact (
		id TEXT PRIMARY KEY,
		version_id TEXT NOT NULL REFERENCES model_version(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		uri TEXT NOT NULL,
		storage_backend TEXT NOT NULL DEFAULT '',
		storage_path TEXT NOT NULL DEFAULT '',
		size_bytes BIGINT NOT NULL DEFAULT 0,
		digest TEXT NOT NULL DEFAULT '',
		media_type TEXT NOT NULL DEFAULT '',
		model_format_name TEXT NOT NULL DEFAULT '',
		model_format_version TEXT NOT NULL DEFAULT '',
		service_account TEXT NOT NULL DEFAULT '',
		custom_properties TEXT,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		UNIQUE (version_id, name)
	)`,
	`CREATE TABLE IF NOT EXISTS lineage_edge (
		id TEXT PRIMARY KEY,
		src_type TEXT NOT NULL,
		src_id TEXT NOT NULL,
		relation TEXT NOT NULL,
		dst_type TEXT NOT NULL DEFAULT '',
		dst_id TEXT NOT NULL DEFAULT '',
		dst_ref TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_lineage_src ON lineage_edge (src_type, src_id)`,
	`CREATE TABLE IF NOT EXISTS audit_event (
		id TEXT PRIMARY KEY,
		at BIGINT NOT NULL,
		actor TEXT NOT NULL DEFAULT '',
		action TEXT NOT NULL,
		subject_type TEXT NOT NULL,
		subject_id TEXT NOT NULL,
		summary TEXT NOT NULL DEFAULT '',
		data TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_subject ON audit_event (subject_type, subject_id, at)`,
	`CREATE TABLE IF NOT EXISTS deployment (
		id TEXT PRIMARY KEY,
		version_id TEXT NOT NULL REFERENCES model_version(id) ON DELETE CASCADE,
		environment TEXT NOT NULL,
		endpoint_uri TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		external_ref TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_deployment_version ON deployment (version_id)`,

	// ---- Model insights (§11) ----
	// Edge properties record *how* a relation came about (e.g. derived_from + quantize)
	// without extending the relation enum (§11.3.6).
	`ALTER TABLE lineage_edge ADD COLUMN properties TEXT`,
	`CREATE TABLE IF NOT EXISTS version_insight (
		version_id TEXT PRIMARY KEY REFERENCES model_version(id) ON DELETE CASCADE,
		framework_name TEXT NOT NULL DEFAULT '',
		framework_version TEXT NOT NULL DEFAULT '',
		producer_name TEXT NOT NULL DEFAULT '',
		producer_version TEXT NOT NULL DEFAULT '',
		param_count_total BIGINT,
		param_count_trainable BIGINT,
		param_count_method TEXT NOT NULL DEFAULT '',
		tensor_count BIGINT,
		dtype_dominant TEXT NOT NULL DEFAULT '',
		quant_method TEXT NOT NULL DEFAULT '',
		quant_scope TEXT,
		disk_bytes BIGINT,
		weights_bytes BIGINT,
		topology_hash TEXT NOT NULL DEFAULT '',
		shape_hash TEXT NOT NULL DEFAULT '',
		dtype_hash TEXT NOT NULL DEFAULT '',
		weights_hash TEXT NOT NULL DEFAULT '',
		arch_doc TEXT,
		source TEXT NOT NULL,
		field_sources TEXT,
		reporter_name TEXT NOT NULL DEFAULT '',
		reporter_version TEXT NOT NULL DEFAULT '',
		coverage TEXT,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_insight_topology ON version_insight (topology_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_insight_shape ON version_insight (shape_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_insight_weights ON version_insight (weights_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_insight_params ON version_insight (param_count_total)`,
	`CREATE TABLE IF NOT EXISTS layer_block (
		version_id TEXT NOT NULL REFERENCES model_version(id) ON DELETE CASCADE,
		ordinal BIGINT NOT NULL,
		path TEXT NOT NULL,
		op_type TEXT NOT NULL DEFAULT '',
		repeat_count BIGINT NOT NULL DEFAULT 1,
		shape_signature TEXT NOT NULL DEFAULT '',
		dtype TEXT NOT NULL DEFAULT '',
		param_count BIGINT,
		bytes BIGINT,
		PRIMARY KEY (version_id, ordinal)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_layer_op ON layer_block (op_type)`,
	`CREATE TABLE IF NOT EXISTS footprint (
		id TEXT PRIMARY KEY,
		version_id TEXT NOT NULL REFERENCES model_version(id) ON DELETE CASCADE,
		scenario TEXT NOT NULL,
		device_class TEXT NOT NULL DEFAULT '',
		batch BIGINT,
		seq_len BIGINT,
		weights_bytes BIGINT,
		kv_cache_bytes BIGINT,
		activation_bytes BIGINT,
		runtime_overhead_bytes BIGINT,
		total_bytes BIGINT,
		source TEXT NOT NULL,
		basis TEXT,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		UNIQUE (version_id, scenario)
	)`,
	`CREATE TABLE IF NOT EXISTS evaluation (
		id TEXT PRIMARY KEY,
		version_id TEXT NOT NULL REFERENCES model_version(id) ON DELETE CASCADE,
		suite TEXT NOT NULL,
		metric TEXT NOT NULL,
		split TEXT NOT NULL DEFAULT '',
		value DOUBLE PRECISION NOT NULL,
		higher_is_better BOOLEAN NOT NULL,
		n_samples BIGINT,
		harness_name TEXT NOT NULL DEFAULT '',
		harness_version TEXT NOT NULL DEFAULT '',
		params TEXT,
		evidence_artifact_id TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL,
		run_at BIGINT NOT NULL DEFAULT 0,
		created_at BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_eval_lookup ON evaluation (version_id, suite, metric, split)`,

	// ---- Risk classification (§16.7.1) ----
	// One row per model per regime. The composite PK is not tidiness: every §16.5 drift
	// clause measures against this row's classified_at, so a second regime sharing the row
	// would let an MRM write silently clear an EU staleness (§16.3.2).
	//
	// The CHECK ties each enum group to the discriminator, which is what keeps a sparse
	// column set honest rather than merely wide. Today there is one branch, so it also
	// rejects any regime this build does not define; M19 adds an `mrm` branch that asserts
	// the EU group is null on those rows, and vice versa (`20.8.1`).
	`CREATE TABLE IF NOT EXISTS classification (
		model_id TEXT NOT NULL REFERENCES model(id) ON DELETE CASCADE,
		regime TEXT NOT NULL,
		eu_gpai_tier TEXT,
		eu_system_risk_class TEXT,
		intended_purpose TEXT NOT NULL DEFAULT '',
		basis TEXT NOT NULL DEFAULT '',
		classified_at BIGINT NOT NULL,
		classified_by TEXT NOT NULL DEFAULT '',
		review_due_at BIGINT,
		PRIMARY KEY (model_id, regime),
		CHECK (
			(regime = 'eu_ai_act' AND eu_system_risk_class IS NOT NULL AND eu_gpai_tier IS NOT NULL)
		)
	)`,
	// Every index leads with regime, so one regime's queries never scan another's rows (§16.7.3).
	`CREATE INDEX IF NOT EXISTS idx_classification_eu_class ON classification (regime, eu_system_risk_class)`,
	`CREATE INDEX IF NOT EXISTS idx_classification_eu_tier ON classification (regime, eu_gpai_tier)`,
	`CREATE INDEX IF NOT EXISTS idx_classification_review ON classification (regime, review_due_at)`,
}

// migrate applies pending migrations in a forward-only fashion, one per transaction.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var cur int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&cur); err != nil {
		return err
	}
	for i := cur; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, s.rb(`INSERT INTO schema_version (version) VALUES (?)`), i+1); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
