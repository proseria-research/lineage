package pgstore_test

import (
	"os"
	"testing"

	pgstore "github.com/proseria-research/lineage/internal/adapters/store/postgres"
	"github.com/proseria-research/lineage/internal/adapters/store/storetest"
)

// TestPostgresStore runs the shared suite against a real Postgres when LINEAGE_TEST_PG
// is set to a DSN (e.g. postgres://user:pass@localhost:5432/lineage?sslmode=disable).
// Skipped otherwise so `go test ./...` stays dependency-free.
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("LINEAGE_TEST_PG")
	if dsn == "" {
		t.Skip("set LINEAGE_TEST_PG to a Postgres DSN to run this test")
	}
	store, err := pgstore.New(dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer store.Close()
	storetest.Run(t, store)
}
