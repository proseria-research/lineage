package adminui_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/api/adminui"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// The console renders the inventory view (§16.9) straight off these payloads, so the two
// states a reader must never confuse — `unclassified` and `stale` — have to be
// distinguishable in the JSON without the SPA inferring anything.
func complianceSetup(t *testing.T) *httptest.Server {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New())
	ctx := context.Background()

	for _, n := range []string{"high", "low", "bare"} {
		if _, err := svc.CreateModel(ctx, "seed", core.CreateModelInput{Name: n}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.PublishVersion(ctx, "seed", n, core.PublishVersionInput{Name: "1.0.0"}); err != nil {
			t.Fatal(err)
		}
	}
	// Classify after publishing, so nothing starts stale.
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.SetClassification(ctx, "risk@acme.example", "high", domain.RegimeEUAIAct, core.ClassificationInput{
		EUSystemRiskClass: domain.EUClassHighAnnexIII, EUGpaiTier: domain.EUGpaiNone,
		IntendedPurpose: "Card-not-present scoring.", Basis: "Annex III §5(b).",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetClassification(ctx, "risk@acme.example", "low", domain.RegimeEUAIAct, core.ClassificationInput{
		EUSystemRiskClass: domain.EUClassMinimal, EUGpaiTier: domain.EUGpaiNone,
		IntendedPurpose: "Internal only.",
	}); err != nil {
		t.Fatal(err)
	}
	// Then make "high" stale by publishing again.
	time.Sleep(2 * time.Millisecond)
	if _, _, err := svc.PublishVersion(ctx, "seed", "high", core.PublishVersionInput{Name: "2.0.0"}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(adminui.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)
	return srv
}

func getBFF(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func classificationsByModel(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	items, _ := body["items"].([]any)
	out := map[string]map[string]any{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		c, _ := m["classification"].(map[string]any)
		out[m["name"].(string)] = c
	}
	return out
}

func TestModelsBFFCarriesClassification(t *testing.T) {
	srv := complianceSetup(t)
	got := classificationsByModel(t, getBFF(t, srv, "/api/models"))

	if len(got) != 3 {
		t.Fatalf("models = %v, want 3", len(got))
	}
	// An unclassified model must arrive as an explicit null, not as an object the console
	// would have to inspect for emptiness — that is what keeps it from rendering as
	// `minimal` (§16.9).
	if got["bare"] != nil {
		t.Fatalf("unclassified model carried a classification: %v", got["bare"])
	}
	if got["high"]["euSystemRiskClass"] != "high_annex_iii" || got["high"]["state"] != "stale" {
		t.Fatalf("high: %v", got["high"])
	}
	// The reason travels with the state: a bare "stale" badge sends the reader hunting.
	reasons, _ := got["high"]["staleReasons"].([]any)
	if len(reasons) != 1 || reasons[0] != "version_published_since" {
		t.Fatalf("staleReasons = %v", got["high"]["staleReasons"])
	}
	if got["low"]["state"] != "current" {
		t.Fatalf("low: %v", got["low"])
	}
	if got["low"]["staleReasons"] != nil {
		t.Fatalf("a current classification carried reasons: %v", got["low"]["staleReasons"])
	}
}

func TestModelsBFFFilters(t *testing.T) {
	srv := complianceSetup(t)

	tests := []struct {
		query string
		want  []string
	}{
		{"?classificationState=stale", []string{"high"}},
		{"?classificationState=current", []string{"low"}},
		// Its own filter, not a flavour of stale (§16.4).
		{"?classificationState=unclassified", []string{"bare"}},
		{"?euSystemRiskClass=high_annex_iii", []string{"high"}},
		{"?euSystemRiskClass=high_annex_iii&classificationState=stale", []string{"high"}},
		{"?euSystemRiskClass=high_annex_iii&classificationState=current", nil},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			got := classificationsByModel(t, getBFF(t, srv, "/api/models"+tc.query))
			if len(got) != len(tc.want) {
				keys := make([]string, 0, len(got))
				for k := range got {
					keys = append(keys, k)
				}
				t.Fatalf("got %v, want %v", keys, tc.want)
			}
			for _, w := range tc.want {
				if _, ok := got[w]; !ok {
					t.Fatalf("missing %s", w)
				}
			}
		})
	}
}

// The Compliance panel reads the *model's* classification off the version payload (§16.9).
func TestVersionDetailBFFCarriesClassification(t *testing.T) {
	srv := complianceSetup(t)

	body := getBFF(t, srv, "/api/models/high/versions/2.0.0")
	c, _ := body["classification"].(map[string]any)
	if c == nil {
		t.Fatalf("no classification on the version payload: %v", body)
	}
	if c["state"] != "stale" || c["basis"] == "" || c["intendedPurpose"] == "" {
		t.Fatalf("classification: %v", c)
	}
	if c["classifiedBy"] != "risk@acme.example" {
		t.Fatalf("classifiedBy = %v", c["classifiedBy"])
	}

	// An unclassified model yields an explicit null, which the panel renders as "not
	// classified" rather than as a low risk class.
	body = getBFF(t, srv, "/api/models/bare/versions/1.0.0")
	if body["classification"] != nil {
		t.Fatalf("unclassified version payload carried a classification: %v", body["classification"])
	}
}

// The model page header shows the class too, and must not 404 on an unclassified model.
func TestModelDetailBFFClassification(t *testing.T) {
	srv := complianceSetup(t)

	body := getBFF(t, srv, "/api/models/high")
	m, _ := body["model"].(map[string]any)
	c, _ := m["classification"].(map[string]any)
	if c == nil || c["euSystemRiskClass"] != "high_annex_iii" {
		t.Fatalf("model detail classification: %v", m["classification"])
	}

	body = getBFF(t, srv, "/api/models/bare")
	m, _ = body["model"].(map[string]any)
	if m["classification"] != nil {
		t.Fatalf("unclassified model detail: %v", m["classification"])
	}
}

// ---- The console write (§16.9) ----

func putBFF(t *testing.T, srv *httptest.Server, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lineage-Actor", "officer@acme.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// The console can classify a model, and it goes through the same core operation the Model
// API uses — so the anchor, the audit event and the validation are identical. A read-only
// compliance page is a report nobody can act on.
func TestConsoleCanClassify(t *testing.T) {
	srv := complianceSetup(t)

	// "bare" starts with no row at all.
	if got := classificationsByModel(t, getBFF(t, srv, "/api/models")); got["bare"] != nil {
		t.Fatalf("precondition: bare should be unclassified, got %v", got["bare"])
	}

	code, body := putBFF(t, srv, "/api/models/bare/classifications/eu_ai_act", `{
		"euSystemRiskClass":"high_annex_iii","euGpaiTier":"none",
		"intendedPurpose":"Triage of inbound claims.","basis":"Annex III 5(a).","reviewDueAt":null
	}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %v", code, body)
	}
	if body["euSystemRiskClass"] != "high_annex_iii" || body["state"] != "current" {
		t.Fatalf("response: %v", body)
	}
	// Attribution comes from the actor header, not the form.
	if body["classifiedBy"] != "officer@acme.example" {
		t.Fatalf("classifiedBy = %v", body["classifiedBy"])
	}

	// And it is visible on the next read, with no reload of anything server-side.
	got := classificationsByModel(t, getBFF(t, srv, "/api/models"))
	if got["bare"] == nil || got["bare"]["euSystemRiskClass"] != "high_annex_iii" {
		t.Fatalf("after classify: %v", got["bare"])
	}
}

// Re-classifying is the only way a staleness clears (§16.9) — there is no "mark as current".
func TestConsoleReclassifyClearsStaleness(t *testing.T) {
	srv := complianceSetup(t)
	if got := classificationsByModel(t, getBFF(t, srv, "/api/models")); got["high"]["state"] != "stale" {
		t.Fatalf("precondition: high should be stale, got %v", got["high"]["state"])
	}

	time.Sleep(2 * time.Millisecond)
	code, body := putBFF(t, srv, "/api/models/high/classifications/eu_ai_act", `{
		"euSystemRiskClass":"high_annex_iii","euGpaiTier":"none",
		"intendedPurpose":"Card-not-present scoring.","basis":"Annex III 5(b); re-reviewed."
	}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %v", code, body)
	}
	if body["state"] != "current" {
		t.Fatalf("state after re-classification = %v, want current", body["state"])
	}
}

// The form mirrors §16.6 client-side, but the server is the enforcement point — a request
// that slips past the form still fails, with the coded status the console renders.
func TestConsoleClassifyValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"a stated class with no purpose", `{"euSystemRiskClass":"minimal","euGpaiTier":"none"}`, http.StatusUnprocessableEntity},
		{"high risk with no basis", `{"euSystemRiskClass":"high_annex_i","euGpaiTier":"none","intendedPurpose":"p"}`, http.StatusUnprocessableEntity},
		{"unknown class", `{"euSystemRiskClass":"severe","euGpaiTier":"none"}`, http.StatusBadRequest},
		{"review date in the past", `{"euSystemRiskClass":"minimal","euGpaiTier":"none","intendedPurpose":"p","reviewDueAt":1}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := complianceSetup(t)
			code, body := putBFF(t, srv, "/api/models/bare/classifications/eu_ai_act", tc.body)
			if code != tc.want {
				t.Fatalf("status = %d, want %d (%v)", code, tc.want, body)
			}
			if body["detail"] == nil {
				t.Fatalf("no problem+json detail for the console to show: %v", body)
			}
		})
	}
}

func TestConsoleClassifyUnknownRegime(t *testing.T) {
	srv := complianceSetup(t)
	code, _ := putBFF(t, srv, "/api/models/bare/classifications/uk_ai_bill", `{"euSystemRiskClass":"minimal","euGpaiTier":"none","intendedPurpose":"p"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}
