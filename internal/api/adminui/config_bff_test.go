package adminui_test

import (
	"net/http/httptest"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/api/adminui"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

func bareSvc() *core.Service {
	b := fs.New("default", "")
	return core.New(memstore.New(), map[string]domain.StorageBackend{b.Name(): b}, b.Name(), memcache.New(), events.New())
}

// The console's "Use this model" snippets read the public Model API address and the docs link
// from /api/config; an unset address reports the port so the console can build one.
func TestConfigEndpoint(t *testing.T) {
	srv := setup(t)
	if got := getJSON(t, srv, "/api/config"); len(got) != 0 {
		t.Fatalf("unconfigured /api/config = %v, want {}", got)
	}

	svcSrv := httptest.NewServer(adminui.New(bareSvc(), "X-Lineage-Actor").WithClientInfo(adminui.ClientInfo{
		ModelAPIURL: "https://models.acme.example", ModelAPIPort: "8081", DocsURL: "https://docs.example",
	}).Handler())
	t.Cleanup(svcSrv.Close)
	got := getJSON(t, svcSrv, "/api/config")
	if got["modelApiUrl"] != "https://models.acme.example" || got["modelApiPort"] != "8081" || got["docsUrl"] != "https://docs.example" {
		t.Fatalf("/api/config = %v", got)
	}
}
