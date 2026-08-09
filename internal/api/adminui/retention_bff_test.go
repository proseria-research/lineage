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

// The console renders holds straight off these payloads. The distinction that must survive
// the wire is a version held *itself* versus one held *through its model* — releasing the
// wrong subject is the mistake the shape exists to prevent (§19.3.1).
func holdSetup(t *testing.T) (*httptest.Server, *core.Service) {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New(),
		core.WithRetention(domain.RetentionConfig{MinArchivedVersionDays: 3650}),
		core.WithAttestation(domain.DefaultAttestation))
	ctx := context.Background()
	for _, n := range []string{"held", "free"} {
		if _, err := svc.CreateModel(ctx, "seed", core.CreateModelInput{Name: n}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.PublishVersion(ctx, "seed", n, core.PublishVersionInput{Name: "1.0.0"}); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(adminui.New(svc).Handler())
	t.Cleanup(srv.Close)
	return srv, svc
}

func TestHoldOnTheModelPayloads(t *testing.T) {
	srv, svc := holdSetup(t)
	ctx := context.Background()
	if _, err := svc.SetModelHold(ctx, "counsel@acme.example", "held", "Regulator inquiry"); err != nil {
		t.Fatal(err)
	}

	// The list carries it, so a table can mark held rows without a second call per row.
	body := getBFF(t, srv, "/api/models")
	items, _ := body["items"].([]any)
	seen := map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		seen[m["name"].(string)] = m["legalHold"]
	}
	h, ok := seen["held"].(map[string]any)
	if !ok || h["heldBy"] != "counsel@acme.example" || h["heldSince"] == nil {
		t.Fatalf("held model in the list: %v", seen["held"])
	}
	// Not held must be an explicit null, never an omitted key: absence has to be
	// distinguishable from "the console forgot to ask".
	if v, present := seen["free"]; !present || v != nil {
		t.Fatalf("an unheld model must serialize legalHold as null, got %v (present=%v)", v, present)
	}

	if d := getBFF(t, srv, "/api/models/held"); d["model"].(map[string]any)["legalHold"] == nil {
		t.Fatalf("model detail must carry the hold: %v", d["model"])
	}
}

func TestVersionHoldOwnVersusInherited(t *testing.T) {
	srv, svc := holdSetup(t)
	ctx := context.Background()
	if _, err := svc.SetModelHold(ctx, "counsel@acme.example", "held", "Regulator inquiry"); err != nil {
		t.Fatal(err)
	}

	// Inherited: the version is not marked, and modelHold names the subject that is.
	d := getBFF(t, srv, "/api/models/held/versions/1.0.0")
	if d["version"].(map[string]any)["legalHold"] != nil {
		t.Fatalf("inheritance must not be written onto the version row: %v", d["version"])
	}
	if d["modelHold"] == nil {
		t.Fatalf("the version page must be able to name the model's hold: %v", d)
	}

	// Its own hold: both are populated, and the page prefers the specific one.
	if _, err := svc.SetVersionHold(ctx, "ops@acme.example", "held", "1.0.0", "Same matter"); err != nil {
		t.Fatal(err)
	}
	d = getBFF(t, srv, "/api/models/held/versions/1.0.0")
	own, ok := d["version"].(map[string]any)["legalHold"].(map[string]any)
	if !ok || own["heldBy"] != "ops@acme.example" {
		t.Fatalf("the version's own hold: %v", d["version"])
	}

	// A free model's version has neither.
	d = getBFF(t, srv, "/api/models/free/versions/1.0.0")
	if d["version"].(map[string]any)["legalHold"] != nil || d["modelHold"] != nil {
		t.Fatalf("an unheld version must report both as null: %v", d)
	}
}

func TestEvidenceEndpoints(t *testing.T) {
	srv, svc := holdSetup(t)
	ctx := context.Background()

	// The cheap strip: config only, no scan.
	body := getBFF(t, srv, "/api/evidence")
	ret := body["retention"].(map[string]any)
	if ret["minArchivedVersionDays"] != float64(3650) {
		t.Fatalf("retention = %v", ret)
	}
	if att := body["attestation"].(map[string]any); att["enabled"] != true {
		t.Fatalf("attestation = %v", att)
	}
	if _, present := body["verify"]; present {
		t.Fatal("a page load must not run the recompute — it reads every audit row ever written")
	}

	// The explicit recompute.
	cfg := domain.DefaultAttestation
	if _, err := svc.SealDue(ctx, domain.NowMillis()+2*cfg.IntervalMillis()); err != nil {
		t.Fatal(err)
	}
	body = postBFF(t, srv, "/api/evidence:verify")
	v, ok := body["verify"].(map[string]any)
	if !ok || v["ok"] != true {
		t.Fatalf("verify: %v", body)
	}
	// The panel states what it did not cover, so a clean result is never read as wider than
	// it is (§19.5.3).
	if _, present := v["openEpochSince"]; !present {
		t.Fatalf("verify must report openEpochSince: %v", v)
	}
	// An ok:true over zero epochs would be vacuous — the setup has to have sealed something.
	if n, _ := v["epochsChecked"].(float64); n < 1 {
		t.Fatalf("epochsChecked = %v, want at least one sealed window", v["epochsChecked"])
	}
}

func postBFF(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s = %d", path, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestHoldWritesFromTheConsole: the console places and lifts holds through the same core
// operations the Model API does, so the rules and the audit events are identical. A surface
// that could show a hold but not place one would be a report, not a workspace (§19.8).
func TestHoldWritesFromTheConsole(t *testing.T) {
	srv, svc := holdSetup(t)
	ctx := context.Background()

	// A reason is required in both directions — it is the only durable record of the matter.
	if code := postStatus(t, srv, "/api/models/held/hold", `{}`); code != http.StatusBadRequest {
		t.Fatalf("hold without a reason = %d, want 400", code)
	}

	body := postBody(t, srv, "/api/models/held/hold", `{"reason":"Regulator inquiry"}`, http.StatusOK)
	if body["legalHold"] == nil {
		t.Fatalf("hold response: %v", body)
	}
	m, err := svc.GetModel(ctx, "held")
	if err != nil || m.LegalHold == nil {
		t.Fatalf("the hold must be persisted: %v %+v", err, m)
	}
	// Same core rules: re-holding refuses rather than refreshing the date.
	if code := postStatus(t, srv, "/api/models/held/hold", `{"reason":"again"}`); code != http.StatusConflict {
		t.Fatalf("re-hold = %d, want 409", code)
	}

	// A version can be held in its own right, independently of its model.
	postBody(t, srv, "/api/models/free/versions/1.0.0/hold", `{"reason":"Separate matter"}`, http.StatusOK)
	v, err := svc.GetVersion(ctx, "free", "1.0.0")
	if err != nil || v.LegalHold == nil {
		t.Fatalf("version hold must be persisted: %v %+v", err, v)
	}

	// Releasing is a separate, audited action — and refuses when nothing is held.
	body = postBody(t, srv, "/api/models/held/release", `{"reason":"Matter closed"}`, http.StatusOK)
	if body["legalHold"] != nil {
		t.Fatalf("release must clear the hold: %v", body)
	}
	if code := postStatus(t, srv, "/api/models/held/release", `{"reason":"again"}`); code != http.StatusConflict {
		t.Fatalf("release of an unheld model = %d, want 409", code)
	}

	actions := map[string]bool{}
	evs, _, _ := svc.ListAudit(ctx, "model", m.ID, domain.ListOptions{PageSize: 50})
	for _, e := range evs {
		actions[e.Action] = true
	}
	if !actions["hold.set"] || !actions["hold.release"] {
		t.Fatalf("console writes must produce the same audit actions: %v", actions)
	}
}

// TestTransitionKeepsTheHold guards a regression this suite already caught once: the
// transition response hand-built a versionSummary and silently dropped legalHold.
func TestTransitionKeepsTheHold(t *testing.T) {
	srv, svc := holdSetup(t)
	ctx := context.Background()
	if _, err := svc.SetVersionHold(ctx, "ops@acme.example", "free", "1.0.0", "matter"); err != nil {
		t.Fatal(err)
	}
	body := postBody(t, srv, "/api/models/free/versions/1.0.0/transition", `{"to":"staging"}`, http.StatusOK)
	if body["legalHold"] == nil {
		t.Fatalf("a transition response must still carry the hold: %v", body)
	}
}

func postBody(t *testing.T, srv *httptest.Server, path, body string, want int) map[string]any {
	t.Helper()
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("POST %s = %d, want %d", path, resp.StatusCode, want)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func postStatus(t *testing.T, srv *httptest.Server, path, body string) int {
	t.Helper()
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
