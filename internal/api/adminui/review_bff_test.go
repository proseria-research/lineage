package adminui_test

import (
	"context"
	"encoding/json"
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

// The console draws §17.7's side-by-side fingerprints straight off this payload, so the two
// things it cannot compute for itself — the per-level hash values and which of them changed —
// have to be in the JSON.

func reviewSetup(t *testing.T) (*httptest.Server, string) {
	t.Helper()
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
	e, err := svc.AddLineage(ctx, "ml@acme.example", "m", "1.1.0", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetClassification(ctx, "risk@acme.example", "m", domain.RegimeEUAIAct, core.ClassificationInput{
		EUSystemRiskClass: domain.EUClassHighAnnexIII, EUGpaiTier: domain.EUGpaiNone,
		IntendedPurpose: "Card-not-present scoring.", Basis: "Annex III §5(b).",
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(adminui.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)
	return srv, e.ID
}

func writeLadder(t *testing.T, svc *core.Service, version, dtype, weights string) {
	t.Helper()
	w := core.InsightWrite{
		SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived,
		Facts: map[string]json.RawMessage{
			"hashes": json.RawMessage(`{"topology":"t","shape":"sh","dtype":"` + dtype + `","weights":"` + weights + `"}`),
		},
	}
	if _, err := svc.WriteInsight(context.Background(), "ci", "m", version, w, false, false); err != nil {
		t.Fatalf("WriteInsight %s: %v", version, err)
	}
}

func TestReviewQueueBFF(t *testing.T) {
	srv, edgeID := reviewSetup(t)

	var page struct {
		Items []struct {
			Model, Version, EdgeID string
			Verdict, Status        string
			DeclaredMethod         string
			Hashes                 map[string]struct {
				From, To string
				Changed  *bool
				Present  bool
			}
			Review *map[string]any
		}
	}
	getJSONInto(t, srv.URL+"/api/reviews", &page)
	if len(page.Items) != 1 {
		t.Fatalf("items = %d", len(page.Items))
	}
	it := page.Items[0]
	if it.Status != "open" || it.Verdict != "recast" || it.DeclaredMethod != "quantize" {
		t.Fatalf("item: %+v", it)
	}
	if it.Review != nil {
		t.Fatalf("an open item carries no review: %v", it.Review)
	}
	// The values, not just a summary: the console draws two marks from them.
	if it.Hashes["dtype"].From != "d1" || it.Hashes["dtype"].To != "d2" {
		t.Fatalf("hash values: %+v", it.Hashes)
	}
	// And which rings to emphasise. topology matched, dtype did not.
	if it.Hashes["topology"].Changed == nil || *it.Hashes["topology"].Changed {
		t.Fatalf("topology should be present and unchanged: %+v", it.Hashes["topology"])
	}
	if it.Hashes["dtype"].Changed == nil || !*it.Hashes["dtype"].Changed {
		t.Fatalf("dtype should be present and changed: %+v", it.Hashes["dtype"])
	}

	// The console's write goes through the same core operation, frozen verdict included.
	body := `{"edgeId":"` + edgeID + `","outcome":"not_substantial","note":"precision only"}`
	resp, err := http.Post(srv.URL+"/api/models/m/versions/1.1.0/reviews", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record review: %d", resp.StatusCode)
	}
	var rev map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rev); err != nil {
		t.Fatal(err)
	}
	if rev["verdictAtReview"] != "recast" {
		t.Fatalf("verdictAtReview = %v", rev["verdictAtReview"])
	}

	getJSONInto(t, srv.URL+"/api/reviews?status=open", &page)
	if len(page.Items) != 0 {
		t.Fatalf("still open: %+v", page.Items)
	}
	getJSONInto(t, srv.URL+"/api/reviews", &page)
	if len(page.Items) != 1 || page.Items[0].Status != "closed" || page.Items[0].Review == nil {
		t.Fatalf("unfiltered must still show it, closed: %+v", page.Items)
	}
}

func getJSONInto(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
