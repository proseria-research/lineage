package adminui_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/api/adminui"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

func setup(t *testing.T) *httptest.Server {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New())
	ctx := context.Background()
	if _, err := svc.CreateModel(ctx, "seed", core.CreateModelInput{Name: "fraud-detector", Owner: "risk"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.PublishVersion(ctx, "seed", "fraud-detector", core.PublishVersionInput{
		Name:      "1.4.0",
		Artifacts: []core.ArtifactInput{{Name: "model.onnx", URI: "s3://m/1.4.0", Digest: "sha256:abc", SizeBytes: 2048}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transition(ctx, "seed", "fraud-detector", "1.4.0", domain.StageStaging, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transition(ctx, "seed", "fraud-detector", "1.4.0", domain.StageProduction, ""); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(adminui.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)
	return srv
}

func getJSON(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s = %d", path, resp.StatusCode)
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("%s decode: %v", path, err)
	}
	return m
}

// The embedded console is served at / and client-side routes fall back to index.html.
// The built console is not committed (only a .gitkeep placeholder), so this skips unless
// `make web` has produced real assets.
func TestServesEmbeddedConsole(t *testing.T) {
	srv := setup(t)
	probe, _ := http.Get(srv.URL + "/")
	body, _ := io.ReadAll(probe.Body)
	probe.Body.Close()
	if !strings.Contains(string(body), `<div id="root">`) {
		t.Skip("console not built — run `make web`")
	}
	for _, path := range []string{"/", "/models", "/models/fraud-detector/versions/1.4.0"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("GET %s content-type = %q", path, ct)
		}
		if !strings.Contains(string(body), `<div id="root">`) {
			t.Fatalf("GET %s did not serve the SPA index", path)
		}
	}
}

func TestOverviewAggregates(t *testing.T) {
	srv := setup(t)
	m := getJSON(t, srv, "/api/overview")
	c := m["counts"].(map[string]any)
	if c["models"].(float64) != 1 || c["versions"].(float64) != 1 || c["artifacts"].(float64) != 1 {
		t.Fatalf("overview counts = %+v", c)
	}
	stages := m["stages"].(map[string]any)
	if stages["production"].(float64) != 1 {
		t.Fatalf("expected 1 production version, got %+v", stages)
	}
	if len(m["recent"].([]any)) == 0 {
		t.Fatal("expected recent activity")
	}
}

func TestModelsRollupAndDetail(t *testing.T) {
	srv := setup(t)
	list := getJSON(t, srv, "/api/models")
	items := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("models list = %d", len(items))
	}
	row := items[0].(map[string]any)
	if row["versionCount"].(float64) != 1 || row["production"] != "1.4.0" {
		t.Fatalf("rollup = %+v", row)
	}

	detail := getJSON(t, srv, "/api/models/fraud-detector")
	if detail["production"] != "1.4.0" || len(detail["versions"].([]any)) != 1 {
		t.Fatalf("model detail = %+v", detail)
	}

	vd := getJSON(t, srv, "/api/models/fraud-detector/versions/1.4.0")
	arts := vd["artifacts"].([]any)
	if len(arts) != 1 || arts[0].(map[string]any)["name"] != "model.onnx" {
		t.Fatalf("version detail artifacts = %+v", vd["artifacts"])
	}
	// Promoted in setup, so its artifact set is locked (§00.11.19) — on both BFF shapes.
	if vd["version"].(map[string]any)["lockedAt"] == nil || detail["versions"].([]any)[0].(map[string]any)["lockedAt"] == nil {
		t.Fatalf("lockedAt missing: version=%+v versions=%+v", vd["version"], detail["versions"])
	}
	// Empty collections must serialize as [] (not null) so the SPA can map them.
	if vd["lineage"] == nil || vd["deployments"] == nil {
		t.Fatalf("empty collections should be [] not null: %+v", vd)
	}
}
