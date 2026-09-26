// Package pgstore is the Postgres MetadataStore adapter — the full-power HA/prod tier
// (§02.7). It supplies the Postgres dialect ($n placeholders, 23505 detection, row
// locking via SELECT ... FOR UPDATE) to the shared sqlstore. Engine-specific features
// (JSONB filtering, GIN, pgvector) can be layered on behind this dialect later.
package pgstore

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/proseria-research/lineage/internal/adapters/store/sqlstore"
)

type dialect struct{}

func (dialect) Name() string { return "postgres" }

// Rebind converts ? placeholders to $1,$2,… (our SQL never uses a literal ?).
func (dialect) Rebind(q string) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		} else {
			b.WriteByte(q[i])
		}
	}
	return b.String()
}

func (dialect) IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// LockModelByVersionSQL locks the owning model row so concurrent promotions into a
// singleton stage serialize (§02.4). One placeholder = the version id.
func (dialect) LockModelByVersionSQL() string {
	return `SELECT id FROM model WHERE id = (SELECT model_id FROM model_version WHERE id = ?) FOR UPDATE`
}

// LockVersionRowSQL takes a shared lock on the version row. FOR SHARE conflicts with the FOR
// NO KEY UPDATE lock a promotion's UPDATE takes, so an artifact write and a promotion into
// staging cannot interleave (§00.11.19). One placeholder = the version id.
func (dialect) LockVersionRowSQL() string {
	return `SELECT id FROM model_version WHERE id = ? FOR SHARE`
}

// JSONContainsClause pushes label / custom_properties containment into JSONB (§02.7). The
// columns are TEXT holding JSON; casting to jsonb lets Postgres use the @> operator (GIN-able).
func (dialect) JSONContainsClause(column string) string {
	return column + "::jsonb @> ?::jsonb"
}

// New connects to dsn and returns a migrated MetadataStore.
func New(dsn string) (*sqlstore.Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	return sqlstore.Open(db, dialect{})
}
