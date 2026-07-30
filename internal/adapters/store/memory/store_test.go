package memstore_test

import (
	"testing"

	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/adapters/store/storetest"
)

func TestMemoryStore(t *testing.T) {
	storetest.Run(t, memstore.New())
}
