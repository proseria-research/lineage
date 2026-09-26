package pgstore_test

import (
	"context"
	"io"
	"os"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	pgstore "github.com/proseria-research/lineage/internal/adapters/store/postgres"
	"github.com/proseria-research/lineage/internal/adapters/store/sqlstore"
	"github.com/proseria-research/lineage/internal/adapters/store/storetest"
	"github.com/proseria-research/lineage/internal/domain"
)

// pgStore returns a real Postgres-backed store: LINEAGE_TEST_PG (a DSN) if set, otherwise a
// throwaway embedded Postgres. Skips the test if neither is available (e.g. fully offline).
func pgStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	if dsn := os.Getenv("LINEAGE_TEST_PG"); dsn != "" {
		store, err := pgstore.New(dsn)
		if err != nil {
			t.Fatalf("open postgres (LINEAGE_TEST_PG): %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Username("lineage").Password("lineage").Database("lineage").
		Port(15432).Logger(io.Discard))
	if err := pg.Start(); err != nil {
		t.Skipf("embedded postgres unavailable (set LINEAGE_TEST_PG to use an external one): %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })
	store, err := pgstore.New("postgres://lineage:lineage@localhost:15432/lineage?sslmode=disable")
	if err != nil {
		t.Fatalf("open embedded postgres: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestPostgresStore runs the full shared conformance suite against a real Postgres.
func TestPostgresStore(t *testing.T) {
	s := pgStore(t)
	storetest.Run(t, s)
	storetest.RunSQLConstraints(t, s)
	storetest.RunInventory(t, s)
	storetest.RunRetention(t, s)
	storetest.RunReviews(t, s)
	storetest.RunMRM(t, s)
	storetest.RunAttestation(t, s)
}

// TestPostgresJSONFilters exercises the Postgres-only JSONB label + custom_properties
// containment push-down (§02.7), which SQLite/memory cannot do.
func TestPostgresJSONFilters(t *testing.T) {
	ctx := context.Background()
	s := pgStore(t)
	now := domain.NowMillis()

	mk := func(name string, labels map[string]string, cp string) {
		m := &domain.Model{
			ID: domain.NewID(), Name: name, State: domain.StateActive,
			Labels: labels, CustomProperties: []byte(cp), CreatedAt: now, UpdatedAt: now,
		}
		if err := s.CreateModel(ctx, m); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	mk("gold-risk", map[string]string{"tier": "gold", "domain": "risk"}, `{"costCenter":"R-42"}`)
	mk("silver", map[string]string{"tier": "silver"}, `{"costCenter":"S-1"}`)

	names := func(o domain.ListOptions) []string {
		items, _, err := s.ListModels(ctx, o)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var out []string
		for _, m := range items {
			out = append(out, m.Name)
		}
		return out
	}

	// Label containment (AND across keys), pushed into SQL.
	if got := names(domain.ListOptions{Labels: map[string]string{"tier": "gold"}}); len(got) != 1 || got[0] != "gold-risk" {
		t.Fatalf("label tier=gold → %v, want [gold-risk]", got)
	}
	if got := names(domain.ListOptions{Labels: map[string]string{"tier": "gold", "domain": "risk"}}); len(got) != 1 {
		t.Fatalf("label AND tier=gold,domain=risk → %v, want 1", got)
	}
	if got := names(domain.ListOptions{Labels: map[string]string{"tier": "gold", "domain": "nope"}}); len(got) != 0 {
		t.Fatalf("non-matching AND → %v, want none", got)
	}
	// custom_properties containment (Postgres-only).
	if got := names(domain.ListOptions{CustomProps: map[string]string{"costCenter": "R-42"}}); len(got) != 1 || got[0] != "gold-risk" {
		t.Fatalf("cp costCenter=R-42 → %v, want [gold-risk]", got)
	}
	if got := names(domain.ListOptions{CustomProps: map[string]string{"costCenter": "X"}}); len(got) != 0 {
		t.Fatalf("cp costCenter=X → %v, want none", got)
	}
}
