package adminui_test

import (
	"context"
	"encoding/json"
	"net/http"
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

// The change-control section renders the plan register and the queue from one payload, so
// the plan rows must carry their model's name and the queue rows the hashes the fingerprints
// are drawn from.
func TestChangePlansBFF(t *testing.T) {
	backend := fs.New("default", t.TempDir())
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New())
	ctx := context.Background()

	if _, err := svc.CreateModel(ctx, "seed", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"1.0.0", "1.1.0"} {
		if _, _, err := svc.PublishVersion(ctx, "seed", "m", core.PublishVersionInput{Name: v}); err != nil {
			t.Fatal(err)
		}
	}
	writeLadder(t, svc, "1.0.0", "d1", "w1")
	writeLadder(t, svc, "1.1.0", "d2", "w2")
	in := core.LineageInput{Relation: domain.RelDerivedFrom, Properties: json.RawMessage(`{"method":"quantize"}`)}
	in.To.Version = "1.0.0"
	if _, err := svc.AddLineage(ctx, "ml", "m", "1.1.0", in); err != nil {
		t.Fatal(err)
	}
	from := int64(0)
	if _, err := svc.DeclareChangePlan(ctx, "ra", "m", core.ChangePlanInput{
		Ref: "K243117", Summary: "Retraining only.", EffectiveFrom: &from,
		AllowedVerdicts: []domain.Verdict{domain.VerdictIdentical, domain.VerdictReweighted},
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(adminui.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/api/change-plans")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var page struct {
		Plans []struct {
			ID, Model, Ref  string
			AllowedVerdicts []string
		}
		Items []struct {
			Version, Conformance, Verdict string
			Reasons                       []string
			Hashes                        map[string]domain.HashCmp
			Plan                          *domain.PlanRef
		}
	}
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Plans) != 1 || page.Plans[0].Model != "m" || page.Plans[0].Ref != "K243117" || len(page.Plans[0].AllowedVerdicts) != 2 {
		t.Fatalf("plans: %+v", page.Plans)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items: %+v", page.Items)
	}
	it := page.Items[0]
	// A dtype change is `recast`, which this plan does not allow.
	if it.Version != "1.1.0" || it.Conformance != "outside_plan" || it.Verdict != "recast" || it.Plan == nil {
		t.Fatalf("item: %+v", it)
	}
	if it.Hashes["dtype"].From != "d1" || it.Hashes["dtype"].To != "d2" {
		t.Fatalf("the fingerprints need both sides' values: %+v", it.Hashes)
	}
}
