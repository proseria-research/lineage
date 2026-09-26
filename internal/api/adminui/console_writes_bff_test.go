package adminui_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The console records validations, clears their conditions and declares change plans through
// the same core operations as /v1 (§20.9, §22.7), attributed to the console's actor.

func sendBFF(t *testing.T, srv *httptest.Server, method, path, body string, want int) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lineage-Actor", "validator@acme.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d: %v", method, path, resp.StatusCode, want, out)
	}
	return out
}

func TestConsoleRecordsAndClearsAValidation(t *testing.T) {
	srv := mrmSetup(t)

	v := sendBFF(t, srv, "POST", "/api/models/tiered/versions/1.0.0/validations",
		`{"outcome":"conditional","findings":"Back-test gap","conditions":"Re-run on Q3 data"}`, http.StatusCreated)
	if v["validatedBy"] != "validator@acme.example" || v["outcome"] != "conditional" {
		t.Fatalf("recorded = %v", v)
	}
	id, _ := v["id"].(string)

	cleared := sendBFF(t, srv, "POST", "/api/models/tiered/versions/1.0.0/validations/"+id+"/clear-conditions", "", http.StatusOK)
	if cleared["conditionsClearedAt"] == nil {
		t.Fatalf("not cleared: %v", cleared)
	}
	// A second clear is refused, as on /v1.
	sendBFF(t, srv, "POST", "/api/models/tiered/versions/1.0.0/validations/"+id+"/clear-conditions", "", http.StatusConflict)

	// Conditional without conditions is refused by the same validation /v1 applies.
	sendBFF(t, srv, "POST", "/api/models/tiered/versions/1.0.0/validations", `{"outcome":"conditional"}`, http.StatusUnprocessableEntity)
}

func TestConsoleDeclaresAndSupersedesAPlan(t *testing.T) {
	srv := mrmSetup(t)

	p := sendBFF(t, srv, "POST", "/api/models/tiered/change-plans",
		`{"ref":"PCCP-1","summary":"Retrains only","allowedVerdicts":["identical","reweighted"]}`, http.StatusCreated)
	id, _ := p["id"].(string)
	from, _ := p["effectiveFrom"].(float64)
	if p["declaredBy"] != "validator@acme.example" {
		t.Fatalf("declared = %v", p)
	}
	// A second open plan without `supersedes` overlaps.
	sendBFF(t, srv, "POST", "/api/models/tiered/change-plans",
		`{"summary":"Wider","allowedVerdicts":["recast"]}`, http.StatusConflict)
	sendBFF(t, srv, "POST", "/api/models/tiered/change-plans",
		`{"ref":"PCCP-2","summary":"Wider","allowedVerdicts":["reweighted","recast"],"supersedes":"`+id+`","effectiveFrom":`+strconv.FormatInt(int64(from)+1, 10)+`}`, http.StatusCreated)

	plans, _ := getBFF(t, srv, "/api/change-plans")["plans"].([]any)
	if len(plans) != 2 {
		t.Fatalf("plans = %d, want 2 (history kept)", len(plans))
	}
}
