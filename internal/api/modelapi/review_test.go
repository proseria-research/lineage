package modelapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The §17.6 surface over HTTP. What is worth asserting here rather than in core is the
// contract shape: which status a refusal carries, and that a client cannot supply the one
// field the whole table exists to protect.

// derived builds the queue's subject over HTTP: model "m" classified high_annex_iii, a parent
// 1.0.0 and a child 1.1.0 derived from it, each with a hash ladder that makes the change a
// `reweighted`. It returns the server and the edge id.
func derived(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := classifiableModel(t)
	for _, v := range []string{"1.0.0", "1.1.0"} {
		if code, _ := do(t, srv, "POST", "/v1/models/m/versions", `{"name":"`+v+`"}`, nil); code != http.StatusCreated {
			t.Fatalf("publish %s: %d", v, code)
		}
	}
	writeHashes(t, srv, "1.0.0", "w1")
	writeHashes(t, srv, "1.1.0", "w2")

	code, body := do(t, srv, "POST", "/v1/models/m/versions/1.1.0/lineage",
		`{"relation":"derived_from","to":{"version":"1.0.0"},"properties":{"method":"quantize"}}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("add lineage: %d %v", code, body)
	}
	edgeID, _ := body["id"].(string)
	if edgeID == "" {
		t.Fatalf("no edge id in %v", body)
	}
	if code, _ := do(t, srv, "PUT", euPath, highRiskBody, nil); code != http.StatusOK {
		t.Fatalf("classify: %d", code)
	}
	return srv, edgeID
}

func writeHashes(t *testing.T, srv *httptest.Server, version, weights string) {
	t.Helper()
	body := `{"schemaVersion":"1","source":"derived","facts":{"hashes":{"topology":"t","shape":"sh","dtype":"d","weights":"` + weights + `"}}}`
	if code, b := do(t, srv, "PATCH", "/v1/models/m/versions/"+version+"/insight", body, nil); code != http.StatusOK {
		t.Fatalf("insight %s: %d %v", version, code, b)
	}
}

func TestReviewQueueOverHTTP(t *testing.T) {
	srv, edgeID := derived(t)

	code, body := do(t, srv, "GET", "/v1/reviews?status=open", "", nil)
	if code != http.StatusOK {
		t.Fatalf("queue: %d %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("open items = %d: %v", len(items), body)
	}
	it, _ := items[0].(map[string]any)
	if it["verdict"] != "reweighted" || it["declaredMethod"] != "quantize" || it["status"] != "open" {
		t.Fatalf("item: %v", it)
	}
	if it["euSystemRiskClass"] != "high_annex_iii" {
		t.Fatalf("class: %v", it["euSystemRiskClass"])
	}
	basis, _ := it["basis"].(map[string]any)
	if basis == nil || len(basis["fromHashes"].([]any)) != 4 {
		t.Fatalf("basis: %v", it["basis"])
	}

	// Record a review; the item closes and carries what was concluded.
	code, rev := do(t, srv, "POST", "/v1/models/m/versions/1.1.0/reviews",
		`{"edgeId":"`+edgeID+`","outcome":"not_substantial","note":"envelope unchanged"}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("record: %d %v", code, rev)
	}
	if rev["verdictAtReview"] != "reweighted" || rev["reviewedBy"] != "tester" {
		t.Fatalf("server-set fields: %v", rev)
	}

	_, body = do(t, srv, "GET", "/v1/reviews?status=open", "", nil)
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Fatalf("still open: %v", items)
	}
	_, body = do(t, srv, "GET", "/v1/reviews?status=closed", "", nil)
	items, _ = body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("closed items = %d", len(items))
	}
	if r, _ := items[0].(map[string]any)["review"].(map[string]any); r == nil || r["outcome"] != "not_substantial" {
		t.Fatalf("closed item carries its review: %v", items[0])
	}

	_, body = do(t, srv, "GET", "/v1/models/m/versions/1.1.0/reviews", "", nil)
	if items, _ := body["items"].([]any); len(items) != 1 {
		t.Fatalf("version reviews = %v", body)
	}
}

// §17.6.1: the frozen verdict is the registry's witness, not a client's claim. ReviewInput has
// no field for it and the shared decoder refuses unknown fields, so this is a 400 rather than
// a silently ignored key.
func TestReviewRejectsClientSuppliedVerdict(t *testing.T) {
	srv, edgeID := derived(t)
	code, body := do(t, srv, "POST", "/v1/models/m/versions/1.1.0/reviews",
		`{"edgeId":"`+edgeID+`","outcome":"not_substantial","verdictAtReview":"identical"}`, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %v", code, body)
	}
	if body["code"] != "invalid_argument" {
		t.Fatalf("code = %v", body["code"])
	}
	// And nothing was recorded — a rejected write must not half-happen.
	_, list := do(t, srv, "GET", "/v1/models/m/versions/1.1.0/reviews", "", nil)
	if items, _ := list["items"].([]any); len(items) != 0 {
		t.Fatalf("a rejected review must not be stored: %v", items)
	}
}

func TestReviewErrorsOverHTTP(t *testing.T) {
	srv, edgeID := derived(t)

	cases := []struct {
		name, path, body string
		want             int
	}{
		{"unknown outcome", "/v1/models/m/versions/1.1.0/reviews",
			`{"edgeId":"` + edgeID + `","outcome":"pending"}`, http.StatusBadRequest},
		{"unknown edge", "/v1/models/m/versions/1.1.0/reviews",
			`{"edgeId":"01JZZZ","outcome":"substantial"}`, http.StatusBadRequest},
		{"unknown version", "/v1/models/m/versions/9.9.9/reviews",
			`{"edgeId":"` + edgeID + `","outcome":"substantial"}`, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, body := do(t, srv, "POST", c.path, c.body, nil); code != c.want {
				t.Fatalf("got %d want %d: %v", code, c.want, body)
			}
		})
	}

	if code, _ := do(t, srv, "GET", "/v1/reviews?status=pending", "", nil); code != http.StatusBadRequest {
		t.Fatalf("unknown status = %d", code)
	}
}

// The queue is a list, not a gate (§17.1). An open item must leave every other operation
// alone — this is the assertion that would catch someone later "helpfully" wiring it up.
func TestOpenReviewBlocksNothing(t *testing.T) {
	srv, _ := derived(t)
	if code, body := do(t, srv, "GET", "/v1/reviews?status=open", "", nil); code != http.StatusOK {
		t.Fatalf("queue: %d %v", code, body)
	}

	if code, b := do(t, srv, "POST", "/v1/models/m/versions/1.1.0:transition",
		`{"to":"staging","reason":"ship it"}`, nil); code != http.StatusOK {
		t.Fatalf("transition must not be blocked: %d %v", code, b)
	}
	if code, _ := do(t, srv, "POST", "/v1/models/m/versions", `{"name":"1.2.0"}`, nil); code != http.StatusCreated {
		t.Fatalf("publish must not be blocked: %d", code)
	}
	if code, b := do(t, srv, "DELETE", "/v1/models/m/versions/1.2.0", "", nil); code != http.StatusNoContent {
		t.Fatalf("delete must not be blocked: %d %v", code, b)
	}
}

// Drift clause 4 reaches the inventory query: a derivation opened after the classification
// makes the model stale, with its own reason (§16.5, `17.4`).
func TestInventoryStaleFromDerivation(t *testing.T) {
	srv, _ := derived(t)

	// The fixture classifies after the edge, so nothing has happened since it.
	_, body := do(t, srv, "GET", "/v1/models?classificationState=stale", "", nil)
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Fatalf("should start current: %v", items)
	}

	// §16.5 compares strictly, so the edge has to land in a later millisecond than the
	// classification for it to have happened *since* it.
	time.Sleep(2 * time.Millisecond)
	if code, _ := do(t, srv, "POST", "/v1/models/m/versions/1.1.0/lineage",
		`{"relation":"derived_from","to":{"uri":"hf://acme/base-7b"}}`, nil); code != http.StatusCreated {
		t.Fatal("add external derivation")
	}

	_, body = do(t, srv, "GET", "/v1/models?classificationState=stale", "", nil)
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("stale items = %d: %v", len(items), body)
	}
	c, _ := items[0].(map[string]any)["classification"].(map[string]any)
	reasons, _ := c["staleReasons"].([]any)
	var found bool
	for _, r := range reasons {
		if r == "derivation_since" {
			found = true
		}
	}
	if !found {
		t.Fatalf("staleReasons = %v", reasons)
	}
}
