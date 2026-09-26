package modelapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const (
	mrmPath  = "/v1/models/m/classifications/mrm"
	valsPath = "/v1/models/m/versions/1.0.0/validations"
	tier1    = `{"mrmTier":"tier_1","basis":"Drives automated card-not-present declines above $500."}`
)

// mrmModel is model "m" with 1.0.0 (author dev@acme.example) promoted to production and
// tiered tier_1.
func mrmModel(t *testing.T) *httptest.Server {
	t.Helper()
	srv := classifiableModel(t)
	if code, body := do(t, srv, "POST", "/v1/models/m/versions", `{"name":"1.0.0","author":"dev@acme.example"}`, nil); code != http.StatusCreated {
		t.Fatalf("publish: %d %v", code, body)
	}
	for _, to := range []string{"staging", "production"} {
		if code, body := do(t, srv, "POST", "/v1/models/m/versions/1.0.0:transition", `{"to":"`+to+`"}`, nil); code != http.StatusOK {
			t.Fatalf("transition %s: %d %v", to, code, body)
		}
	}
	if code, body := do(t, srv, "PUT", mrmPath, tier1, nil); code != http.StatusOK {
		t.Fatalf("PUT mrm: %d %v", code, body)
	}
	return srv
}

// The M17 acceptance, over HTTP.
func TestMRMInventoryAcceptance(t *testing.T) {
	srv := mrmModel(t)
	if code, body := do(t, srv, "POST", valsPath, `{"outcome":"approved"}`,
		map[string]string{"X-Lineage-Actor": "mrm@acme.example"}); code != http.StatusCreated {
		t.Fatalf("validate: %d %v", code, body)
	}

	code, body := do(t, srv, "GET", "/v1/models?mrmTier=tier_1&mrmState=stale", "", nil)
	if code != http.StatusOK {
		t.Fatalf("inventory: %d %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	item := items[0].(map[string]any)
	m, _ := item["mrm"].(map[string]any)
	if m == nil || m["mrmTier"] != "tier_1" || m["state"] != "stale" || m["source"] != "declared" {
		t.Fatalf("mrm = %v", item["mrm"])
	}
	reasons, _ := m["staleReasons"].([]any)
	if len(reasons) != 1 || reasons[0] != "unmonitored_in_production" {
		t.Fatalf("staleReasons = %v", reasons)
	}
	latest, _ := m["latestValidation"].(map[string]any)
	if latest["outcome"] != "approved" || latest["validatedBy"] != "mrm@acme.example" || latest["independenceEvidenced"] != true {
		t.Fatalf("latestValidation = %v", latest)
	}
	if m["version"] != "1.0.0" {
		t.Fatalf("version = %v", m["version"])
	}
	// Only the lens asked for.
	if _, ok := item["classification"]; ok {
		t.Fatal("the EU lens rode along uninvited")
	}
}

func TestMRMClassificationOverHTTP(t *testing.T) {
	srv := mrmModel(t)
	code, body := do(t, srv, "GET", mrmPath, "", nil)
	if code != http.StatusOK || body["regime"] != "mrm" || body["mrmTier"] != "tier_1" || body["state"] != "unvalidated" {
		t.Fatalf("GET mrm: %d %v", code, body)
	}
	if _, ok := body["euSystemRiskClass"]; ok {
		t.Fatalf("mrm row carries an EU field: %v", body)
	}

	for _, tc := range []struct {
		name, body string
		code       int
		field      string
	}{
		{"tier_1 without basis", `{"mrmTier":"tier_1"}`, http.StatusUnprocessableEntity, "basis"},
		{"out_of_scope without basis", `{"mrmTier":"out_of_scope"}`, http.StatusUnprocessableEntity, "basis"},
		{"unknown tier", `{"mrmTier":"tier_9","basis":"b"}`, http.StatusBadRequest, "mrmTier"},
		{"EU field on the mrm path", `{"mrmTier":"tier_2","euSystemRiskClass":"minimal"}`, http.StatusBadRequest, "euSystemRiskClass"},
		{"classifiedAt from the client", `{"mrmTier":"tier_2","classifiedAt":1}`, http.StatusBadRequest, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := do(t, srv, "PUT", mrmPath, tc.body, nil)
			if code != tc.code {
				t.Fatalf("status = %d, want %d (%v)", code, tc.code, body)
			}
			if tc.field != "" && details(t, body)["field"] != tc.field {
				t.Fatalf("details = %v", body["details"])
			}
		})
	}
	// Rejected writes left the row alone.
	if _, body := do(t, srv, "GET", mrmPath, "", nil); body["mrmTier"] != "tier_1" {
		t.Fatalf("a rejected write changed the row: %v", body)
	}
	// And mrmTier on the EU path.
	if code, _ := do(t, srv, "PUT", euPath, `{"euSystemRiskClass":"minimal","intendedPurpose":"p","mrmTier":"tier_1"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("mrmTier on eu_ai_act: %d", code)
	}
}

// Writing mrm over HTTP leaves the EU row's anchor and staleness where they were.
func TestRegimeIsolationOverHTTP(t *testing.T) {
	srv := classifiableModel(t)
	_, eu := do(t, srv, "PUT", euPath, highRiskBody, nil)
	time.Sleep(2 * time.Millisecond)
	do(t, srv, "POST", "/v1/models/m/versions", `{"name":"1.0.0"}`, nil)
	_, before := do(t, srv, "GET", euPath, "", nil)
	if before["state"] != "stale" {
		t.Fatalf("EU state before = %v", before["state"])
	}

	time.Sleep(2 * time.Millisecond)
	if code, body := do(t, srv, "PUT", mrmPath, tier1, nil); code != http.StatusOK {
		t.Fatalf("PUT mrm: %d %v", code, body)
	}
	_, after := do(t, srv, "GET", euPath, "", nil)
	if after["classifiedAt"] != eu["classifiedAt"] || after["state"] != "stale" {
		t.Fatalf("the mrm write moved the EU row: before %v, after %v", before, after)
	}
}

func TestValidationsOverHTTP(t *testing.T) {
	srv := mrmModel(t)

	// validatedBy / validatedAt are server-set: supplying either is 400, and nothing is stored.
	for _, field := range []string{`"validatedBy":"someone"`, `"validatedAt":1`, `"independenceEvidenced":true`} {
		if code, body := do(t, srv, "POST", valsPath, `{"outcome":"approved",`+field+`}`, nil); code != http.StatusBadRequest {
			t.Fatalf("%s: status %d (%v)", field, code, body)
		}
	}
	if code, body := do(t, srv, "POST", valsPath, `{"outcome":"conditional"}`, nil); code != http.StatusUnprocessableEntity ||
		details(t, body)["field"] != "conditions" {
		t.Fatalf("conditional without conditions: %d %v", code, body)
	}
	if code, body := do(t, srv, "POST", valsPath, `{"outcome":"approved","validUntil":1}`, nil); code != http.StatusBadRequest {
		t.Fatalf("past validUntil: %d %v", code, body)
	}
	if _, list := do(t, srv, "GET", valsPath, "", nil); len(list["items"].([]any)) != 0 {
		t.Fatalf("rejected writes left rows: %v", list)
	}

	// The author validating their own version: recorded, flagged.
	code, self := do(t, srv, "POST", valsPath, `{"outcome":"approved"}`, map[string]string{"X-Lineage-Actor": "dev@acme.example"})
	if code != http.StatusCreated || self["independenceEvidenced"] != false || self["validatedBy"] != "dev@acme.example" {
		t.Fatalf("self-validation: %d %v", code, self)
	}

	// Let the clock move so "newest" is decided by validatedAt, not by the id tiebreak.
	time.Sleep(2 * time.Millisecond)
	code, cond := do(t, srv, "POST", valsPath, `{"outcome":"conditional","conditions":"Re-measure PSI monthly.",
		"scope":"CNP only","findings":"PSI elevated on travel MCC"}`, map[string]string{"X-Lineage-Actor": "mrm@acme.example"})
	if code != http.StatusCreated || cond["independenceEvidenced"] != true {
		t.Fatalf("conditional: %d %v", code, cond)
	}

	code, list := do(t, srv, "GET", valsPath, "", nil)
	items, _ := list["items"].([]any)
	if code != http.StatusOK || len(items) != 2 || items[0].(map[string]any)["id"] != cond["id"] {
		t.Fatalf("list: %d %v", code, list)
	}
	// Unmonitored and conditions outstanding, both reported.
	if r, _ := list["staleReasons"].([]any); list["state"] != "stale" || len(r) != 2 {
		t.Fatalf("version state: %v %v", list["state"], list["staleReasons"])
	}

	id := cond["id"].(string)
	clear := valsPath + "/" + id + ":clearConditions"
	if code, body := do(t, srv, "POST", clear, "", nil); code != http.StatusOK || body["conditionsClearedAt"] == nil {
		t.Fatalf("clear: %d %v", code, body)
	}
	if code, body := do(t, srv, "POST", clear, "", nil); code != http.StatusConflict || details(t, body)["reason"] != "already_cleared" {
		t.Fatalf("second clear: %d %v", code, body)
	}
	if code, body := do(t, srv, "POST", valsPath+"/"+self["id"].(string)+":clearConditions", "", nil); code != http.StatusConflict ||
		details(t, body)["reason"] != "not_conditional" {
		t.Fatalf("clear an approval: %d %v", code, body)
	}
	if code, _ := do(t, srv, "POST", valsPath+"/"+id+":bless", "", nil); code != http.StatusBadRequest {
		t.Fatalf("unknown action: %d", code)
	}

	// The audit feed carries both actions.
	_, feed := do(t, srv, "GET", "/v1/audit", "", nil)
	seen := map[string]bool{}
	for _, e := range feed["items"].([]any) {
		seen[e.(map[string]any)["action"].(string)] = true
	}
	if !seen["validation.record"] || !seen["validation.conditions_cleared"] {
		t.Fatalf("audit actions: %v", seen)
	}
}

func TestMRMInventoryFilters(t *testing.T) {
	srv := mrmModel(t)
	do(t, srv, "POST", "/v1/models", `{"name":"untiered"}`, nil)

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"mrmTier=tier_1", []string{"m"}},
		{"mrmTier=tier_2", nil},
		{"mrmState=untiered", []string{"untiered"}},
		{"mrmState=unvalidated", []string{"m"}},
		{"include=mrm", []string{"m", "untiered"}},
		{"regime=mrm", []string{"m", "untiered"}},
		// Both lenses: the models that pass both. m has no EU row, so it is unclassified there.
		{"mrmTier=tier_1&classificationState=unclassified", []string{"m"}},
		{"mrmTier=tier_1&euSystemRiskClass=minimal", nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			code, body := do(t, srv, "GET", "/v1/models?"+tc.query, "", nil)
			if code != http.StatusOK {
				t.Fatalf("%d %v", code, body)
			}
			got := names(t, body)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}

	for _, q := range []string{"mrmTier=tier_9", "mrmState=unclassified"} {
		code, body := do(t, srv, "GET", "/v1/models?"+q, "", nil)
		if code != http.StatusBadRequest || details(t, body)["allowedValues"] == nil {
			t.Fatalf("%s: %d %v", q, code, body)
		}
	}
}
