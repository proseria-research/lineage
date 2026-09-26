package modelapi_test

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"
)

const euPath = "/v1/models/m/classifications/eu_ai_act"

const highRiskBody = `{
	"euSystemRiskClass":"high_annex_iii",
	"euGpaiTier":"none",
	"intendedPurpose":"Scores card-not-present transactions for manual review.",
	"basis":"Annex III §5(b) — creditworthiness adjacent."
}`

// classifiableModel creates model "m" with no versions.
func classifiableModel(t *testing.T) *httptest.Server {
	t.Helper()
	srv := apiServer(t)
	if code, _ := do(t, srv, "POST", "/v1/models", `{"name":"m"}`, nil); code != http.StatusCreated {
		t.Fatalf("create model: %d", code)
	}
	return srv
}

func TestClassificationPutAndGetOverHTTP(t *testing.T) {
	srv := classifiableModel(t)

	code, body := do(t, srv, "PUT", euPath, highRiskBody, nil)
	if code != http.StatusOK {
		t.Fatalf("PUT: %d %v", code, body)
	}
	if body["euSystemRiskClass"] != "high_annex_iii" || body["regime"] != "eu_ai_act" {
		t.Fatalf("PUT body: %v", body)
	}
	if body["source"] != "declared" {
		t.Fatalf("source = %v, want declared", body["source"])
	}
	if body["state"] != "current" {
		t.Fatalf("state = %v, want current", body["state"])
	}
	// Server-set from the actor header, not from the payload.
	if body["classifiedBy"] != "tester" {
		t.Fatalf("classifiedBy = %v, want the actor header", body["classifiedBy"])
	}

	code, body = do(t, srv, "GET", euPath, "", nil)
	if code != http.StatusOK || body["basis"] == "" {
		t.Fatalf("GET: %d %v", code, body)
	}

	code, body = do(t, srv, "GET", "/v1/models/m/classifications", "", nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list items = %v", body["items"])
	}
}

// §16.6: classifiedAt and classifiedBy are server-set, and sending either is *rejected*
// rather than silently dropped — `03`'s house rule that unknown fields are refused, applied
// to a legal attribution field where a silent no-op would hide a caller's misunderstanding.
func TestClientSuppliedServerFieldsAreRejected(t *testing.T) {
	for _, field := range []string{`"classifiedAt": 1`, `"classifiedBy": "not-the-actor"`, `"source": "derived"`} {
		t.Run(field, func(t *testing.T) {
			srv := classifiableModel(t)
			code, body := do(t, srv, "PUT", euPath, `{
				"euSystemRiskClass":"minimal","euGpaiTier":"none","intendedPurpose":"p",`+field+`}`, nil)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%v)", code, body)
			}
			// And nothing was written.
			if got, _ := do(t, srv, "GET", euPath, "", nil); got != http.StatusNotFound {
				t.Fatalf("a rejected write persisted: GET = %d", got)
			}
		})
	}
}

// The fields that *are* server-set still come from the server on an accepted write.
func TestServerSetsClassifiedByFromTheActorHeader(t *testing.T) {
	srv := classifiableModel(t)
	before := time.Now().UnixMilli()

	code, body := do(t, srv, "PUT", euPath, `{"euSystemRiskClass":"minimal","euGpaiTier":"none","intendedPurpose":"p"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("PUT: %d %v", code, body)
	}
	if body["classifiedBy"] != "tester" {
		t.Fatalf("classifiedBy = %v, want the actor header", body["classifiedBy"])
	}
	if at, _ := body["classifiedAt"].(float64); int64(at) < before {
		t.Fatalf("classifiedAt = %v, want the server clock", body["classifiedAt"])
	}
	if body["source"] != "declared" {
		t.Fatalf("source = %v — there is no computed path for a legal class", body["source"])
	}
}

// The M14 acceptance criterion, over HTTP: publish, then read stale with its reason, with no
// background job having run.
func TestDriftIsVisibleOnTheNextRequest(t *testing.T) {
	srv := classifiableModel(t)
	if code, _ := do(t, srv, "PUT", euPath, highRiskBody, nil); code != http.StatusOK {
		t.Fatal("PUT failed")
	}

	// §16.5 compares strictly, so the publish must land in a later millisecond.
	time.Sleep(2 * time.Millisecond)
	if code, _ := do(t, srv, "POST", "/v1/models/m/versions", `{"name":"1.0.0"}`, nil); code != http.StatusCreated {
		t.Fatal("publish failed")
	}

	code, body := do(t, srv, "GET", euPath, "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET: %d", code)
	}
	if body["state"] != "stale" {
		t.Fatalf("state = %v, want stale", body["state"])
	}
	reasons, _ := body["staleReasons"].([]any)
	if len(reasons) != 1 || reasons[0] != "version_published_since" {
		t.Fatalf("staleReasons = %v", body["staleReasons"])
	}
}

func TestClassificationErrorsOverHTTP(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		want int
	}{{
		name: "unknown regime in the path",
		path: "/v1/models/m/classifications/uk_ai_bill",
		body: highRiskBody,
		want: http.StatusBadRequest,
	}, {
		name: "unknown class",
		path: euPath,
		body: `{"euSystemRiskClass":"high","euGpaiTier":"none"}`,
		want: http.StatusBadRequest,
	}, {
		name: "a stated class with no purpose",
		path: euPath,
		body: `{"euSystemRiskClass":"minimal","euGpaiTier":"none"}`,
		want: http.StatusUnprocessableEntity,
	}, {
		name: "high risk with no basis",
		path: euPath,
		body: `{"euSystemRiskClass":"high_annex_i","euGpaiTier":"none","intendedPurpose":"p"}`,
		want: http.StatusUnprocessableEntity,
	}, {
		name: "a review date in the past",
		path: euPath,
		body: `{"euSystemRiskClass":"minimal","euGpaiTier":"none","intendedPurpose":"p","reviewDueAt":1}`,
		want: http.StatusBadRequest,
	}, {
		name: "unknown model",
		path: "/v1/models/nope/classifications/eu_ai_act",
		body: highRiskBody,
		want: http.StatusNotFound,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := classifiableModel(t)
			code, body := do(t, srv, "PUT", tc.path, tc.body, nil)
			if code != tc.want {
				t.Fatalf("status = %d, want %d (%v)", code, tc.want, body)
			}
		})
	}
}

// An unknown regime is rejected with the regimes this install actually has, so a client can
// discover them without reading the spec.
func TestUnknownRegimeNamesTheAllowedValues(t *testing.T) {
	srv := classifiableModel(t)
	code, body := do(t, srv, "GET", "/v1/models/m/classifications/uk_ai_bill", "", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	details, _ := body["details"].(map[string]any)
	allowed, _ := details["allowedValues"].([]any)
	if len(allowed) != 2 || allowed[0] != "eu_ai_act" || allowed[1] != "mrm" {
		t.Fatalf("allowedValues = %v (body %v)", details["allowedValues"], body)
	}
}

// §16.4: never classified is a 404 on this endpoint, and an empty list on the collection —
// not an error, and not an invented `unclassified` row.
func TestNeverClassifiedOverHTTP(t *testing.T) {
	srv := classifiableModel(t)

	if code, _ := do(t, srv, "GET", euPath, "", nil); code != http.StatusNotFound {
		t.Fatalf("GET on an unclassified model = %d, want 404", code)
	}
	code, body := do(t, srv, "GET", "/v1/models/m/classifications", "", nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d", code)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("items = %v, want an empty array", body["items"])
	}
}

// The write shows up in the model's audit feed with the regime in structured data (§16.8).
func TestClassificationAppearsInTheAuditFeed(t *testing.T) {
	srv := classifiableModel(t)
	if code, _ := do(t, srv, "PUT", euPath, highRiskBody, nil); code != http.StatusOK {
		t.Fatal("PUT failed")
	}

	_, body := do(t, srv, "GET", "/v1/models/m/audit", "", nil)
	items, _ := body["items"].([]any)
	for _, it := range items {
		e, _ := it.(map[string]any)
		if e["action"] != "classification.set" {
			continue
		}
		data, _ := e["data"].(map[string]any)
		if data["regime"] != "eu_ai_act" {
			t.Fatalf("audit data lacks the regime: %v", data)
		}
		if e["actor"] != "tester" {
			t.Fatalf("actor = %v", e["actor"])
		}
		return
	}
	t.Fatalf("no classification.set event in %v", body["items"])
}

// ---- Inventory (§16.8.2) ----

// inventory seeds one high-risk model, one minimal, one never classified.
func inventory(t *testing.T) *httptest.Server {
	t.Helper()
	srv := apiServer(t)
	for _, n := range []string{"inv-high", "inv-minimal", "inv-bare"} {
		if code, _ := do(t, srv, "POST", "/v1/models", `{"name":"`+n+`"}`, nil); code != http.StatusCreated {
			t.Fatalf("create %s: %d", n, code)
		}
	}
	put := func(model, class string) {
		code, body := do(t, srv, "PUT", "/v1/models/"+model+"/classifications/eu_ai_act",
			`{"euSystemRiskClass":"`+class+`","euGpaiTier":"none","intendedPurpose":"p","basis":"b"}`, nil)
		if code != http.StatusOK {
			t.Fatalf("classify %s: %d %v", model, code, body)
		}
	}
	put("inv-high", "high_annex_iii")
	put("inv-minimal", "minimal")
	return srv
}

func names(t *testing.T, body map[string]any) []string {
	t.Helper()
	items, _ := body["items"].([]any)
	out := []string{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		out = append(out, m["name"].(string))
	}
	sort.Strings(out)
	return out
}

// Without a classification parameter, GET /v1/models is exactly what it always was — no
// classification object, and none of the join cost.
func TestModelListUnchangedWithoutAClassificationParam(t *testing.T) {
	srv := inventory(t)
	_, body := do(t, srv, "GET", "/v1/models", "", nil)
	items, _ := body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	for _, it := range items {
		if _, present := it.(map[string]any)["classification"]; present {
			t.Fatalf("classification leaked into a plain model list: %v", it)
		}
	}
}

// ?include=classification asks for the column without narrowing on it.
func TestIncludeClassification(t *testing.T) {
	srv := inventory(t)
	_, body := do(t, srv, "GET", "/v1/models?include=classification", "", nil)
	if got := names(t, body); len(got) != 3 {
		t.Fatalf("names = %v, want all three", got)
	}
	items, _ := body["items"].([]any)
	var withRow, withoutRow int
	for _, it := range items {
		m, _ := it.(map[string]any)
		c, present := m["classification"]
		if !present {
			withoutRow++
			continue
		}
		withRow++
		cm, _ := c.(map[string]any)
		if cm["state"] == nil || cm["source"] != "declared" {
			t.Fatalf("classification object: %v", cm)
		}
	}
	if withRow != 2 || withoutRow != 1 {
		t.Fatalf("with=%d without=%d, want 2/1 — absence is the unclassified state", withRow, withoutRow)
	}
}

func TestInventoryEnumFilters(t *testing.T) {
	srv := inventory(t)

	_, body := do(t, srv, "GET", "/v1/models?euSystemRiskClass=high_annex_iii", "", nil)
	if got := names(t, body); len(got) != 1 || got[0] != "inv-high" {
		t.Fatalf("class filter = %v, want [inv-high]", got)
	}
	// Filtering on a stored enum cannot also mean "and everything nobody classified".
	_, body = do(t, srv, "GET", "/v1/models?euGpaiTier=none", "", nil)
	if got := names(t, body); len(got) != 2 {
		t.Fatalf("tier filter = %v, want the two classified models", got)
	}
	_, body = do(t, srv, "GET", "/v1/models?euSystemRiskClass=prohibited", "", nil)
	if got := names(t, body); len(got) != 0 {
		t.Fatalf("filter matching nothing = %v", got)
	}
}

// The §16.8.2 headline: which high-risk models have gone stale?
func TestInventoryStateFilter(t *testing.T) {
	srv := inventory(t)

	// Everything is current to begin with.
	_, body := do(t, srv, "GET", "/v1/models?classificationState=current", "", nil)
	if got := names(t, body); len(got) != 2 {
		t.Fatalf("current = %v, want the two classified models", got)
	}
	// `unclassified` is its own state, not a kind of stale (§16.4).
	_, body = do(t, srv, "GET", "/v1/models?classificationState=unclassified", "", nil)
	if got := names(t, body); len(got) != 1 || got[0] != "inv-bare" {
		t.Fatalf("unclassified = %v, want [inv-bare]", got)
	}
	_, body = do(t, srv, "GET", "/v1/models?classificationState=stale", "", nil)
	if got := names(t, body); len(got) != 0 {
		t.Fatalf("stale = %v, want none yet", got)
	}

	time.Sleep(2 * time.Millisecond)
	if code, _ := do(t, srv, "POST", "/v1/models/inv-high/versions", `{"name":"1.0.0"}`, nil); code != http.StatusCreated {
		t.Fatal("publish failed")
	}

	// The combined query from §16.8.2, answered without a background job.
	_, body = do(t, srv, "GET", "/v1/models?euSystemRiskClass=high_annex_iii&classificationState=stale", "", nil)
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("high+stale = %v, want [inv-high]", names(t, body))
	}
	m, _ := items[0].(map[string]any)
	c, _ := m["classification"].(map[string]any)
	reasons, _ := c["staleReasons"].([]any)
	if c["state"] != "stale" || len(reasons) != 1 || reasons[0] != "version_published_since" {
		t.Fatalf("classification = %v", c)
	}
	// And the model that did not change is still current.
	_, body = do(t, srv, "GET", "/v1/models?classificationState=current", "", nil)
	if got := names(t, body); len(got) != 1 || got[0] != "inv-minimal" {
		t.Fatalf("current after publish = %v, want [inv-minimal]", got)
	}
}

// Paging happens after the computed-state filter, so a page is never padded with models
// that do not match.
func TestInventoryPagingIsAppliedAfterTheStateFilter(t *testing.T) {
	srv := inventory(t)
	_, body := do(t, srv, "GET", "/v1/models?classificationState=unclassified&pageSize=10", "", nil)
	if got := names(t, body); len(got) != 1 || got[0] != "inv-bare" {
		t.Fatalf("page = %v, want exactly the one match", got)
	}
	// Empty string, not null: that is what every /v1 list endpoint returns for "no more"
	// (§16.8.2's example shows null, but the wire format predates M14 and is uniform).
	if tok, _ := body["nextPageToken"].(string); tok != "" {
		t.Fatalf("nextPageToken = %q, want empty", tok)
	}
}

func TestInventoryRejectsUnknownFilterValues(t *testing.T) {
	for _, q := range []string{
		"euSystemRiskClass=high", "euGpaiTier=frontier",
		"classificationState=fresh", "regime=uk_ai_bill",
	} {
		t.Run(q, func(t *testing.T) {
			srv := inventory(t)
			code, body := do(t, srv, "GET", "/v1/models?"+q, "", nil)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%v)", code, body)
			}
			details, _ := body["details"].(map[string]any)
			if details["allowedValues"] == nil {
				t.Fatalf("no allowedValues in %v", body)
			}
		})
	}
}
