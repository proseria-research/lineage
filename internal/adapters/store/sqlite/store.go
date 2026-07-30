// Package sqlitestore is the SQLite MetadataStore adapter — the zero-dependency
// dev/small/edge tier (§02.7). Per-dialect: it owns its own SQL and migrations and is
// free to use SQLite's portable subset. TODO: implement against a SQLite driver
// (e.g. modernc.org/sqlite, cgo-free). Kept as a stub so the seam is explicit.
package sqlitestore

// New would open the DB at path, run SQLite migrations, and return a
// domain.MetadataStore. Left unimplemented in the scaffold; the memory store stands in.
func New(path string) error {
	_ = path
	return nil
}
