package modelapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The §22.7 surface over HTTP: the contract shapes, the refusals' statuses, and the M18
// acceptance criterion end to end.

const plansPath = "/v1/models/m/change-plans"

const k243117 = `{
	"ref": "K243117",
	"summary": "Periodic retraining on additional de-identified sites. Architecture frozen.",
	"allowedVerdicts": ["identical", "reweighted"],
	"allowedMethods": ["retrain", "fine_tune"],
	"effectiveFrom": 0
}`

// ladderFor writes a full hash ladder for m@version.
func ladderFor(t *testing.T, srv *httptest.Server, version, shape, weights string) {
	t.Helper()
	body := `{"schemaVersion":"1","source":"derived","facts":{"hashes":{"topology":"t","shape":"` + shape +
		`","dtype":"d","weights":"` + weights + `"}}}`
	if code, b := do(t, srv, "PATCH", "/v1/models/m/versions/"+version+"/insight", body, nil); code != http.StatusOK {
		t.Fatalf("insight %s: %d %v", version, code, b)
	}
}

func publishDerived(t *testing.T, srv *httptest.Server, version, method string) {
	t.Helper()
	if code, b := do(t, srv, "POST", "/v1/models/m/versions", `{"name":"`+version+`"}`, nil); code != http.StatusCreated {
		t.Fatalf("publish %s must not be blocked: %d %v", version, code, b)
	}
	if code, b := do(t, srv, "POST", "/v1/models/m/versions/"+version+"/lineage",
		`{"relation":"derived_from","to":{"version":"1.0.0"},"properties":{"method":"`+method+`"}}`, nil); code != http.StatusCreated {
		t.Fatalf("lineage %s: %d %v", version, code, b)
	}
}

// M18 acceptance: declare a plan allowing ["identical","reweighted"], publish a version whose
// verdict is `rescaled`; it appears in the outside_plan queue with its basis, and the publish
// was not blocked.
func TestChangePlanAcceptanceOverHTTP(t *testing.T) {
	srv := classifiableModel(t)
	if code, _ := do(t, srv, "POST", "/v1/models/m/versions", `{"name":"1.0.0"}`, nil); code != http.StatusCreated {
		t.Fatal("publish base")
	}
	ladderFor(t, srv, "1.0.0", "sh", "w1")

	code, plan := do(t, srv, "POST", plansPath, k243117, map[string]string{"X-Lineage-Actor": "ra@acme.example"})
	if code != http.StatusCreated {
		t.Fatalf("declare: %d %v", code, plan)
	}
	if plan["declaredBy"] != "ra@acme.example" || plan["id"] == "" || plan["effectiveTo"] != nil {
		t.Fatalf("plan: %v", plan)
	}

	publishDerived(t, srv, "3.0.0", "distill")
	ladderFor(t, srv, "3.0.0", "sh-wider", "w2")
	publishDerived(t, srv, "3.1.0", "retrain")
	ladderFor(t, srv, "3.1.0", "sh", "w3")

	code, body := do(t, srv, "GET", "/v1/change-plans/conformance?status=outside_plan", "", nil)
	if code != http.StatusOK {
		t.Fatalf("queue: %d %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("outside_plan items = %d: %v", len(items), body)
	}
	it, _ := items[0].(map[string]any)
	if it["model"] != "m" || it["version"] != "3.0.0" || it["conformance"] != "outside_plan" ||
		it["verdict"] != "rescaled" || it["declaredMethod"] != "distill" {
		t.Fatalf("item: %v", it)
	}
	if p, _ := it["plan"].(map[string]any); p["id"] != plan["id"] || p["ref"] != "K243117" {
		t.Fatalf("plan ref: %v", it["plan"])
	}
	if av, _ := it["allowedVerdicts"].([]any); len(av) != 2 || av[0] != "identical" {
		t.Fatalf("allowedVerdicts: %v", it["allowedVerdicts"])
	}
	basis, _ := it["basis"].(map[string]any)
	if basis == nil || len(basis["fromHashes"].([]any)) != 4 || len(basis["toHashes"].([]any)) != 4 {
		t.Fatalf("basis: %v", it["basis"])
	}
	shape, _ := it["hashes"].(map[string]any)["shape"].(map[string]any)
	if shape["changed"] != true {
		t.Fatalf("the shape hash decided it: %v", it["hashes"])
	}

	// The same row, per version; and the conformant sibling.
	_, body = do(t, srv, "GET", "/v1/models/m/versions/3.1.0/conformance", "", nil)
	if items, _ := body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["conformance"] != "within_plan" {
		t.Fatalf("3.1.0: %v", body)
	}
	_, body = do(t, srv, "GET", "/v1/change-plans/conformance?status=outside_plan,within_plan", "", nil)
	if items, _ := body["items"].([]any); len(items) != 2 {
		t.Fatalf("comma-separated statuses: %v", body)
	}

	// And nothing downstream is gated either.
	for _, to := range []string{"staging", "production"} {
		if code, b := do(t, srv, "POST", "/v1/models/m/versions/3.0.0:transition", `{"to":"`+to+`"}`, nil); code != http.StatusOK {
			t.Fatalf("promote outside_plan to %s: %d %v", to, code, b)
		}
	}
}

func TestChangePlanRefusalsOverHTTP(t *testing.T) {
	srv := classifiableModel(t)
	if code, _ := do(t, srv, "POST", "/v1/models/m/versions", `{"name":"1.0.0"}`, nil); code != http.StatusCreated {
		t.Fatal("publish base")
	}

	for _, tc := range []struct {
		name  string
		body  string
		field string
	}{
		{"free text envelope", `{"summary":"s","allowedVerdicts":["minor retraining only"]}`, "allowedVerdicts"},
		{"empty envelope", `{"summary":"s","allowedVerdicts":[]}`, "allowedVerdicts"},
		{"unknown is not plannable", `{"summary":"s","allowedVerdicts":["unknown"]}`, "allowedVerdicts"},
		{"empty methods", `{"summary":"s","allowedVerdicts":["reweighted"],"allowedMethods":[]}`, "allowedMethods"},
		{"client declaredBy", `{"summary":"s","allowedVerdicts":["reweighted"],"declaredBy":"someone"}`, ""},
		{"client effectiveTo", `{"summary":"s","allowedVerdicts":["reweighted"],"effectiveTo":1}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := do(t, srv, "POST", plansPath, tc.body, nil)
			if code != http.StatusBadRequest || body["code"] != "invalid_argument" {
				t.Fatalf("want 400 invalid_argument, got %d %v", code, body)
			}
			if tc.field != "" && details(t, body)["field"] != tc.field {
				t.Fatalf("details = %v", body["details"])
			}
		})
	}
	code, body := do(t, srv, "POST", plansPath, `{"summary":"s","allowedVerdicts":["minor"]}`, nil)
	if allowed, _ := details(t, body)["allowedValues"].([]any); code != http.StatusBadRequest || len(allowed) != 5 {
		t.Fatalf("allowedValues: %d %v", code, body)
	}

	code, first := do(t, srv, "POST", plansPath, k243117, nil)
	if code != http.StatusCreated {
		t.Fatalf("declare: %d %v", code, first)
	}
	code, body = do(t, srv, "POST", plansPath, `{"summary":"second","allowedVerdicts":["rescaled"]}`, nil)
	if code != http.StatusConflict || details(t, body)["reason"] != "plan_overlap" || details(t, body)["planId"] != first["id"] {
		t.Fatalf("overlap: %d %v", code, body)
	}
	code, second := do(t, srv, "POST", plansPath,
		`{"summary":"second","allowedVerdicts":["rescaled"],"supersedes":"`+first["id"].(string)+`"}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("supersede: %d %v", code, second)
	}
	_, body = do(t, srv, "GET", plansPath, "", nil)
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("history kept: %v", body)
	}
	if old, _ := items[1].(map[string]any); old["id"] != first["id"] || old["effectiveTo"] != second["effectiveFrom"] {
		t.Fatalf("superseded plan closed at the new one's start: %v", items[1])
	}

	code, body = do(t, srv, "GET", "/v1/models/m/versions/1.0.0/conformance", "", nil)
	if code != http.StatusConflict || details(t, body)["reason"] != "no_predecessor" {
		t.Fatalf("no_predecessor: %d %v", code, body)
	}
	if code, body := do(t, srv, "GET", "/v1/change-plans/conformance?status=violating", "", nil); code != http.StatusBadRequest {
		t.Fatalf("unknown status: %d %v", code, body)
	}
	if code, _ := do(t, srv, "GET", "/v1/models/nope/change-plans", "", nil); code != http.StatusNotFound {
		t.Fatalf("unknown model: %d", code)
	}
}
