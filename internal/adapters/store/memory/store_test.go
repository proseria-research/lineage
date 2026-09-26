package memstore_test

import (
	"testing"

	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/adapters/store/storetest"
)

func TestMemoryStore(t *testing.T) {
	store := memstore.New()
	storetest.Run(t, store)
	storetest.RunInventory(t, store)
	storetest.RunRetention(t, store)
	storetest.RunReviews(t, store)
	storetest.RunMRM(t, store)
	storetest.RunChangePlans(t, store)
	storetest.RunAttestation(t, store)
}
