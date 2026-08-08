package modelapi_test

import (
	"net/http"
	"net/http/httptest"
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

// The M13 acceptance criterion, over HTTP: publish, then read stale with its reason, with no
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
	if len(allowed) != 1 || allowed[0] != "eu_ai_act" {
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
