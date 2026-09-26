package modelapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/api/modelapi"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// holdableServer creates model "m" with version "1.0.0".
func holdableServer(t *testing.T, srv *httptest.Server) *httptest.Server {
	t.Helper()
	if code, _ := do(t, srv, "POST", "/v1/models", `{"name":"m"}`, nil); code != http.StatusCreated {
		t.Fatal("create model")
	}
	if code, _ := do(t, srv, "POST", "/v1/models/m/versions", `{"name":"1.0.0"}`, nil); code != http.StatusCreated {
		t.Fatal("publish version")
	}
	return srv
}

// floorServer is apiServer with a configured retention floor (§19.4).
func floorServer(t *testing.T, days int) *httptest.Server {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New(),
		core.WithRetention(domain.RetentionConfig{MinArchivedVersionDays: days}))
	srv := httptest.NewServer(modelapi.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)
	return srv
}

// details reaches into the problem+json body (§03.9): the code stays failed_precondition for
// every refusal and `details.reason` carries the specificity.
func details(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	d, ok := body["details"].(map[string]any)
	if !ok {
		t.Fatalf("no details in the problem body: %v", body)
	}
	return d
}

func TestHoldAndReleaseOverHTTP(t *testing.T) {
	srv := holdableServer(t, apiServer(t))

	code, body := do(t, srv, "POST", "/v1/models/m:hold", `{"reason":"Regulator inquiry REF-2026-118"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("hold: %d %v", code, body)
	}
	h, ok := body["legalHold"].(map[string]any)
	if !ok || h["heldBy"] != "tester" {
		t.Fatalf("legalHold on the response: %v", body)
	}

	// The hold rides on the entity, so a plain GET already carries it — no second call, which
	// is what the console relies on (§19.8).
	if _, got := do(t, srv, "GET", "/v1/models/m", "", nil); got["legalHold"] == nil {
		t.Fatalf("GET must carry the hold: %v", got)
	}

	// Not held ⇒ nothing to release (§19.7.4).
	code, body = do(t, srv, "POST", "/v1/models/m/versions/1.0.0:release", `{"reason":"x"}`, nil)
	if code != http.StatusConflict || details(t, body)["reason"] != domain.RefusedNotHeld {
		t.Fatalf("release an unheld version: %d %v", code, body)
	}

	code, body = do(t, srv, "POST", "/v1/models/m:release", `{"reason":"Matter closed"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("release: %d %v", code, body)
	}
	if body["legalHold"] != nil {
		t.Fatalf("release must clear the hold: %v", body)
	}
}

func TestHoldRequiresAReasonOverHTTP(t *testing.T) {
	srv := holdableServer(t, apiServer(t))
	if code, _ := do(t, srv, "POST", "/v1/models/m:hold", `{}`, nil); code != http.StatusBadRequest {
		t.Fatalf("hold without a reason = %d, want 400", code)
	}
}

func TestDeleteRefusedByHoldOverHTTP(t *testing.T) {
	srv := holdableServer(t, apiServer(t))
	if code, _ := do(t, srv, "POST", "/v1/models/m:hold", `{"reason":"inquiry"}`, nil); code != http.StatusOK {
		t.Fatal("hold")
	}

	// force does not clear a hold — that is the whole point of one.
	for _, path := range []string{"/v1/models/m", "/v1/models/m?force=true"} {
		code, body := do(t, srv, "DELETE", path, "", nil)
		if code != http.StatusConflict {
			t.Fatalf("DELETE %s = %d, want 409", path, code)
		}
		d := details(t, body)
		if d["reason"] != domain.RefusedLegalHold {
			t.Fatalf("reason = %v", d["reason"])
		}
		if d["heldBy"] != "tester" || d["heldSince"] == nil {
			t.Fatalf("the refusal must carry provenance: %v", d)
		}
	}

	// Downward: the version under it, naming the model so the caller knows what to release.
	code, body := do(t, srv, "DELETE", "/v1/models/m/versions/1.0.0", "", nil)
	if code != http.StatusConflict || details(t, body)["heldSubject"] != "model/m" {
		t.Fatalf("version under a held model: %d %v", code, body)
	}
}

func TestDeleteRefusedByFloorOverHTTP(t *testing.T) {
	srv := holdableServer(t, floorServer(t, 3650))
	code, body := do(t, srv, "DELETE", "/v1/models/m/versions/1.0.0?force=true", "", nil)
	if code != http.StatusConflict {
		t.Fatalf("DELETE = %d, want 409", code)
	}
	d := details(t, body)
	if d["reason"] != domain.RefusedRetentionFloor {
		t.Fatalf("reason = %v", d["reason"])
	}
	if d["floorDays"] != float64(3650) {
		t.Fatalf("floorDays = %v", d["floorDays"])
	}
}

// TestRetentionEndpoint: the floor is on /v1, not only /healthz. A filing cites this number,
// and every fact must be reachable through the public API — a client reading over HTTP has
// no access to the ops port.
func TestRetentionEndpoint(t *testing.T) {
	srv := floorServer(t, 3650)
	code, body := do(t, srv, "GET", "/v1/retention", "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/retention = %d", code)
	}
	if body["minArchivedVersionDays"] != float64(3650) {
		t.Fatalf("body = %v", body)
	}

	// An unconfigured install reports 0 rather than omitting the field: "no floor" is a real
	// answer, and a missing key would read as "unknown" (§19.4).
	_, body = do(t, apiServer(t), "GET", "/v1/retention", "", nil)
	if body["minArchivedVersionDays"] != float64(0) {
		t.Fatalf("unconfigured retention = %v", body)
	}
}

// TestDeleteStillWorksWithoutAFloor guards the non-breaking promise: nothing about M15
// changes an install that never places a hold and never configures a floor.
func TestDeleteStillWorksWithoutAFloor(t *testing.T) {
	srv := holdableServer(t, apiServer(t))
	if code, _ := do(t, srv, "DELETE", "/v1/models/m/versions/1.0.0", "", nil); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", code)
	}
}

// ---- Attestation over HTTP (§19.7.2, §19.7.3) ----

// sealingServer runs with attestation on and hands back the store, so a test can seal on a
// driven clock and tamper the way someone with database access would.
func sealingServer(t *testing.T) (*httptest.Server, *core.Service, *memstore.Store) {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	store := memstore.New()
	svc := core.New(store,
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New(), core.WithAttestation(domain.DefaultAttestation))
	srv := httptest.NewServer(modelapi.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)
	return srv, svc, store
}

func TestVerifyOverHTTP(t *testing.T) {
	srv, svc, store := sealingServer(t)
	ctx := context.Background()
	if code, _ := do(t, srv, "POST", "/v1/models", `{"name":"m"}`, nil); code != http.StatusCreated {
		t.Fatal("create model")
	}
	evs, _, err := svc.ListAudit(ctx, "", "", domain.ListOptions{PageSize: 10})
	if err != nil || len(evs) == 0 {
		t.Fatalf("ListAudit: %v", err)
	}
	e := evs[0]
	cfg := domain.DefaultAttestation
	if _, err := svc.SealDue(ctx, (*e.Epoch+1)*cfg.IntervalMillis()+cfg.GraceMillis()+1); err != nil {
		t.Fatal(err)
	}

	code, body := do(t, srv, "GET", "/v1/audit:verify", "", nil)
	if code != http.StatusOK || body["ok"] != true {
		t.Fatalf("verify a clean log: %d %v", code, body)
	}
	// The report always states what it did not cover (§19.5.3).
	if body["openEpochSince"] == nil || body["attestationStartedAt"] == nil {
		t.Fatalf("verify must report its own coverage: %v", body)
	}

	// Tamper, then ask again. A break is 200 with ok:false — the request succeeded and the
	// answer is bad news, which a 4xx would make indistinguishable from a broken endpoint.
	if err := store.TamperAuditSummaryForTest(e.ID, "nothing happened"); err != nil {
		t.Fatal(err)
	}
	code, body = do(t, srv, "GET", "/v1/audit:verify", "", nil)
	if code != http.StatusOK {
		t.Fatalf("a detected break must still be 200, got %d", code)
	}
	if body["ok"] != false {
		t.Fatalf("verify after tampering: %v", body)
	}
	br, ok := body["firstBreak"].(map[string]any)
	if !ok || br["kind"] != domain.BreakRootMismatch {
		t.Fatalf("firstBreak = %v", body["firstBreak"])
	}
}

func TestProofOverHTTP(t *testing.T) {
	srv, svc, _ := sealingServer(t)
	ctx := context.Background()
	if code, _ := do(t, srv, "POST", "/v1/models", `{"name":"m"}`, nil); code != http.StatusCreated {
		t.Fatal("create model")
	}
	evs, _, _ := svc.ListAudit(ctx, "", "", domain.ListOptions{PageSize: 10})
	e := evs[0]
	cfg := domain.DefaultAttestation

	// Inside the open window the proof does not exist yet, and the refusal says when it will.
	code, body := do(t, srv, "GET", "/v1/audit/"+e.ID+":proof", "", nil)
	if code != http.StatusConflict {
		t.Fatalf("proof in the open epoch = %d, want 409", code)
	}
	d := details(t, body)
	if d["reason"] != "epoch_unsealed" || d["sealsAt"] == nil {
		t.Fatalf("details = %v", d)
	}

	if _, err := svc.SealDue(ctx, (*e.Epoch+1)*cfg.IntervalMillis()+cfg.GraceMillis()+1); err != nil {
		t.Fatal(err)
	}
	code, body = do(t, srv, "GET", "/v1/audit/"+e.ID+":proof", "", nil)
	if code != http.StatusOK {
		t.Fatalf("proof: %d %v", code, body)
	}
	if body["root"] == nil || body["leafHash"] == nil || body["leafCount"] == nil {
		t.Fatalf("proof body must carry the root, the leaf hash and the count: %v", body)
	}

	if code, _ := do(t, srv, "GET", "/v1/audit/no-such-id:proof", "", nil); code != http.StatusNotFound {
		t.Fatalf("proof for an unknown id = %d, want 404", code)
	}
	if code, _ := do(t, srv, "GET", "/v1/audit/"+e.ID+":nonsense", "", nil); code != http.StatusBadRequest {
		t.Fatalf("unknown audit action = %d, want 400", code)
	}
}

func TestAttestationDisabledOverHTTP(t *testing.T) {
	// apiServer builds a Service with no WithAttestation, so sealing is off.
	srv := apiServer(t)
	code, body := do(t, srv, "GET", "/v1/audit:verify", "", nil)
	if code != http.StatusConflict || details(t, body)["reason"] != "attestation_disabled" {
		t.Fatalf("verify with attestation off: %d %v", code, body)
	}
}
