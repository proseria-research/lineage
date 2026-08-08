package sqlitestore_test

import (
	"path/filepath"
	"testing"

	sqlitestore "github.com/proseria-research/lineage/internal/adapters/store/sqlite"
	"github.com/proseria-research/lineage/internal/adapters/store/storetest"
)

func TestSQLiteStore(t *testing.T) {
	store, err := sqlitestore.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	storetest.Run(t, store)
	storetest.RunSQLConstraints(t, store)
	storetest.RunInventory(t, store)
	storetest.RunRetention(t, store)
	storetest.RunAttestation(t, store)
}
