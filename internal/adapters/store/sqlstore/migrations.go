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
