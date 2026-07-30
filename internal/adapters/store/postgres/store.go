// Package pgstore is the Postgres MetadataStore adapter — the full-power HA/prod tier
// (§02.7). Per-dialect: its own SQL and migrations, free to use JSONB filtering,
// SELECT ... FOR UPDATE for the singleton invariant, GIN, and pgvector. TODO: implement
// against a Postgres driver (e.g. jackc/pgx). Kept as a stub so the seam is explicit.
package pgstore

// New would connect to dsn, run Postgres migrations, and return a domain.MetadataStore.
// Left unimplemented in the scaffold; the memory store stands in.
func New(dsn string) error {
	_ = dsn
	return nil
}
