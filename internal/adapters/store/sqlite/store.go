// Package sqlitestore is the SQLite MetadataStore adapter — the zero-dependency
// dev/small/edge tier (§02.7). It supplies the SQLite dialect to the shared sqlstore and
// opens a cgo-free modernc.org/sqlite connection with foreign keys enabled. Single-writer,
// so the pool is capped at one connection (which also serializes the singleton invariant).
package sqlitestore

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/proseria-research/lineage/internal/adapters/store/sqlstore"
)

type dialect struct{}

func (dialect) Name() string                     { return "sqlite" }
func (dialect) Rebind(q string) string           { return q }  // SQLite uses ? placeholders
func (dialect) LockModelByVersionSQL() string    { return "" } // single-writer; no row lock
func (dialect) JSONContainsClause(string) string { return "" } // no push-down; filter in Go

func (dialect) IsUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// New opens the SQLite database at path and returns a migrated MetadataStore.
func New(path string) (*sqlstore.Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single-writer (§02.7)
	return sqlstore.Open(db, dialect{})
}
