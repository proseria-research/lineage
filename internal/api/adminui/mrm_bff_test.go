package adminui_test

import (
	"context"
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

// The console renders the MRM column and panels (§20.10) straight off these payloads, so
// `untiered` has to arrive as an explicit null and a stale row has to carry its reasons.
func mrmSetup(t *testing.T) *httptest.Server {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New())
	ctx := context.Background()

	for _, n := range []string{"tiered", "bare"} {
		if _, err := svc.CreateModel(ctx, "seed", core.CreateModelInput{Name: n}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.PublishVersion(ctx, "seed", n, core.PublishVersionInput{Name: "1.0.0", Author: "dev"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, to := range []domain.Stage{domain.StageStaging, domain.StageProduction} {
		if _, err := svc.Transition(ctx, "release", "tiered", "1.0.0", to, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.SetClassification(ctx, "mrm", "tiered", domain.RegimeMRM, core.ClassificationInput{
		MRMTier: domain.MRMTier1, Basis: "Automated declines.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecordValidation(ctx, "mrm", "tiered", "1.0.0", core.ValidationInput{Outcome: domain.ValidationApproved}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(adminui.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestModelsBFFCarriesMRM(t *testing.T) {
	srv := mrmSetup(t)
	items, _ := getBFF(t, srv, "/api/models")["items"].([]any)
	byName := map[string]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if v, ok := byName["bare"]["mrm"]; !ok || v != nil {
		t.Fatalf("an untiered model must carry an explicit null: %v (present %v)", v, ok)
	}
	mrm, _ := byName["tiered"]["mrm"].(map[string]any)
	if mrm["mrmTier"] != "tier_1" || mrm["state"] != "stale" {
		t.Fatalf("tiered: %v", mrm)
	}
	if r, _ := mrm["staleReasons"].([]any); len(r) != 1 || r[0] != "unmonitored_in_production" {
		t.Fatalf("staleReasons = %v", mrm["staleReasons"])
	}

	// The URL filters narrow the same table.
	only, _ := getBFF(t, srv, "/api/models?mrmState=untiered")["items"].([]any)
	if len(only) != 1 || only[0].(map[string]any)["name"] != "bare" {
		t.Fatalf("mrmState=untiered: %v", only)
	}
}

func TestVersionBFFCarriesValidations(t *testing.T) {
	srv := mrmSetup(t)
	body := getBFF(t, srv, "/api/models/tiered/versions/1.0.0")
	if mrm, _ := body["mrm"].(map[string]any); mrm["version"] != "1.0.0" {
		t.Fatalf("mrm = %v", body["mrm"])
	}
	vals, _ := body["validations"].(map[string]any)
	items, _ := vals["items"].([]any)
	if len(items) != 1 || vals["state"] != "stale" {
		t.Fatalf("validations = %v", vals)
	}
	if items[0].(map[string]any)["independenceEvidenced"] != true {
		t.Fatalf("independence: %v", items[0])
	}

	// The model page carries the row too.
	if m, _ := getBFF(t, srv, "/api/models/tiered")["model"].(map[string]any); m["mrm"] == nil {
		t.Fatal("model detail lost the mrm row")
	}
}
