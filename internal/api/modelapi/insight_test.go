package modelapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// seedVersions creates one model with two versions so insight endpoints have subjects.
func seedVersions(t *testing.T) *httptest.Server {
	t.Helper()
	srv := apiServer(t)
	if code, _ := do(t, srv, "POST", "/v1/models", `{"name":"m"}`, nil); code != http.StatusCreated {
		t.Fatalf("create model: %d", code)
	}
	for _, v := range []string{"a", "b"} {
		if code, _ := do(t, srv, "POST", "/v1/models/m/versions", `{"name":"`+v+`"}`, nil); code != http.StatusCreated {
			t.Fatalf("publish %s: %d", v, code)
		}
	}
	return srv
}

const patchPath = "/v1/models/m/versions/a/insight"

// Two producers PATCH disjoint facts over HTTP; neither may lose the other's work.
func TestInsightPatchMergeOverHTTP(t *testing.T) {
	srv := seedVersions(t)

	code, _ := do(t, srv, "PATCH", patchPath, `{
		"schemaVersion":"1","source":"derived","reporter":"scanner","reporterVersion":"0.3.1",
		"facts":{"hashes":{"topology":"t1","shape":"s1","dtype":"d1"},"tensorCount":291}
	}`, nil)
	if code != http.StatusOK {
		t.Fatalf("first patch: %d", code)
	}

	code, _ = do(t, srv, "PATCH", patchPath, `{
		"schemaVersion":"1","source":"declared","reporter":"release-bot",
		"facts":{"quantMethod":"gptq","hashes":{"weights":"w1"}}
	}`, nil)
	if code != http.StatusOK {
		t.Fatalf("second patch: %d", code)
	}

	code, body := do(t, srv, "GET", patchPath+"?include=sources", "", nil)
	if code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	hashes, _ := body["hashes"].(map[string]any)
	if hashes["topology"] != "t1" || hashes["weights"] != "w1" {
		t.Fatalf("hash levels should merge across producers: %v", hashes)
	}
	if body["tensorCount"] != float64(291) || body["quantMethod"] != "gptq" {
		t.Fatalf("facts lost across producers: %v", body)
	}
	sources, _ := body["fieldSources"].(map[string]any)
	qm, _ := sources["quantMethod"].(map[string]any)
	if qm["reporter"] != "release-bot" || qm["source"] != "declared" {
		t.Fatalf("per-field attribution missing: %v", sources)
	}

	// Without ?include=sources the attribution is omitted, not fabricated.
	if _, plain := do(t, srv, "GET", patchPath, "", nil); plain["fieldSources"] != nil {
		t.Fatalf("fieldSources should be opt-in: %v", plain["fieldSources"])
	}
}

func TestInsightSchemaVersionRejected(t *testing.T) {
	srv := seedVersions(t)
	code, body := do(t, srv, "PATCH", patchPath,
		`{"schemaVersion":"99","source":"derived","facts":{"tensorCount":1}}`, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("unknown schemaVersion should be 400, got %d (%v)", code, body)
	}
	// An unknown field is rejected rather than dropped, so a producer can tell it is
	// talking to an older registry (§11.6.1).
	code, _ = do(t, srv, "PATCH", patchPath,
		`{"schemaVersion":"1","source":"derived","facts":{"paramCount":1}}`, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("unknown fact field should be 400, got %d", code)
	}
	// Unknown *envelope* keys are rejected by the shared strict decoder.
	code, _ = do(t, srv, "PATCH", patchPath,
		`{"schemaVersion":"1","source":"derived","reportr":"typo"}`, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("unknown envelope key should be 400, got %d", code)
	}
}

// A contradicting weights hash is a 409: the artifact content of a published version is
// immutable (§11.7).
func TestInsightWeightsConflictOverHTTP(t *testing.T) {
	srv := seedVersions(t)
	first := `{"schemaVersion":"1","source":"derived","facts":{"hashes":{"weights":"w1"}}}`
	second := `{"schemaVersion":"1","source":"derived","facts":{"hashes":{"weights":"w2"}}}`

	if code, _ := do(t, srv, "PATCH", patchPath, first, nil); code != http.StatusOK {
		t.Fatalf("first write: %d", code)
	}
	code, body := do(t, srv, "PATCH", patchPath, second, nil)
	if code != http.StatusConflict {
		t.Fatalf("conflicting weights hash should be 409, got %d (%v)", code, body)
	}
	if code, _ := do(t, srv, "PATCH", patchPath+"?force=true", second, nil); code != http.StatusOK {
		t.Fatalf("force should override, got %d", code)
	}
}

func TestPutReplacesOverHTTP(t *testing.T) {
	srv := seedVersions(t)
	do(t, srv, "PATCH", patchPath,
		`{"schemaVersion":"1","source":"derived","facts":{"tensorCount":291,"quantMethod":"gptq"}}`, nil)
	code, _ := do(t, srv, "PUT", patchPath,
		`{"schemaVersion":"1","source":"declared","facts":{"tensorCount":300}}`, nil)
	if code != http.StatusOK {
		t.Fatalf("put: %d", code)
	}
	_, body := do(t, srv, "GET", patchPath, "", nil)
	if body["quantMethod"] != nil {
		t.Fatalf("PUT should replace the document: %v", body)
	}
}

func TestEvaluationsAndFootprints(t *testing.T) {
	srv := seedVersions(t)

	code, _ := do(t, srv, "POST", "/v1/models/m/versions/a/evaluations",
		`{"suite":"mmlu","metric":"acc","split":"5shot","value":0.712,"higherIsBetter":true,"harnessVersion":"0.4.2"}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("add evaluation: %d", code)
	}
	// Direction is required — inferring it from the metric name is how a regression gets
	// reported as an improvement.
	code, _ = do(t, srv, "POST", "/v1/models/m/versions/a/evaluations",
		`{"suite":"mmlu","metric":"acc","value":0.5}`, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("missing higherIsBetter should be 400, got %d", code)
	}

	code, body := do(t, srv, "GET", "/v1/models/m/versions/a/evaluations", "", nil)
	items, _ := body["items"].([]any)
	if code != http.StatusOK || len(items) != 1 {
		t.Fatalf("list evaluations: %d %v", code, body)
	}

	// An estimate must carry its basis.
	code, _ = do(t, srv, "PUT", "/v1/models/m/versions/a/footprints/bs1-2k",
		`{"source":"estimated","totalBytes":21474836480}`, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("estimate without basis should be 400, got %d", code)
	}
	code, _ = do(t, srv, "PUT", "/v1/models/m/versions/a/footprints/bs1-2k",
		`{"source":"estimated","totalBytes":21474836480,"batch":1,"seqLen":2048,"basis":{"kvDtype":"fp16"}}`, nil)
	if code != http.StatusOK {
		t.Fatalf("put footprint: %d", code)
	}
	// Same scenario upserts rather than appending.
	do(t, srv, "PUT", "/v1/models/m/versions/a/footprints/bs1-2k",
		`{"source":"measured","totalBytes":11811160064}`, nil)
	_, body = do(t, srv, "GET", "/v1/models/m/versions/a/footprints", "", nil)
	items, _ = body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("same scenario should upsert, got %d rows", len(items))
	}
}

func TestDiffEndpoints(t *testing.T) {
	srv := seedVersions(t)
	do(t, srv, "PATCH", "/v1/models/m/versions/a/insight",
		`{"schemaVersion":"1","source":"derived","facts":{"hashes":{"topology":"t","shape":"s","dtype":"d16","weights":"w1"}}}`, nil)
	do(t, srv, "PATCH", "/v1/models/m/versions/b/insight",
		`{"schemaVersion":"1","source":"derived","facts":{"hashes":{"topology":"t","shape":"s","dtype":"d8","weights":"w2"}}}`, nil)

	code, body := do(t, srv, "GET", "/v1/models/m/diff?from=a&to=b", "", nil)
	if code != http.StatusOK || body["verdict"] != "recast" {
		t.Fatalf("model diff: %d %v", code, body)
	}
	if code, _ := do(t, srv, "GET", "/v1/models/m/diff?from=a", "", nil); code != http.StatusBadRequest {
		t.Fatalf("diff without ?to should be 400, got %d", code)
	}

	code, body = do(t, srv, "GET", "/v1/diff?from=m@a&to=m@b", "", nil)
	if code != http.StatusOK || body["verdict"] != "recast" {
		t.Fatalf("global diff: %d %v", code, body)
	}
	if code, _ := do(t, srv, "GET", "/v1/diff?from=m&to=m@b", "", nil); code != http.StatusBadRequest {
		t.Fatalf("malformed ref should be 400, got %d", code)
	}
}

// The compact block rides along on resolve only when asked for, so the hot path stays
// small and the two shapes never share a cache entry (§11.6.3).
func TestResolveIncludeInsight(t *testing.T) {
	srv := seedVersions(t)
	do(t, srv, "PATCH", patchPath,
		`{"schemaVersion":"1","source":"derived","facts":{"paramCountTotal":7000000000,"dtypeDominant":"bf16"}}`, nil)
	do(t, srv, "PUT", "/v1/models/m/versions/a/footprints/bs1-2k",
		`{"source":"measured","totalBytes":11811160064}`, nil)
	do(t, srv, "PUT", "/v1/models/m/versions/a/footprints/bs32-8k",
		`{"source":"measured","totalBytes":64424509440}`, nil)

	_, plain := do(t, srv, "GET", "/v1/models/m/resolve?version=a", "", nil)
	if plain["insight"] != nil {
		t.Fatalf("insight must be opt-in on resolve: %v", plain["insight"])
	}

	_, withIns := do(t, srv, "GET", "/v1/models/m/resolve?version=a&include=insight", "", nil)
	ins, _ := withIns["insight"].(map[string]any)
	if ins == nil {
		t.Fatalf("expected an insight block: %v", withIns)
	}
	if ins["paramCount"] != float64(7000000000) || ins["dtype"] != "bf16" {
		t.Fatalf("compact block contents: %v", ins)
	}
	// The smallest recorded scenario is the one a scheduler can fit into, and it is named
	// so the number is interpretable.
	if ins["minDeviceMemoryBytes"] != float64(11811160064) || ins["minDeviceMemoryScenario"] != "bs1-2k" {
		t.Fatalf("min device memory should be the smallest named scenario: %v", ins)
	}
}

// A version nobody reported on has no insight, which is a 404 on the insight resource and
// an `unknown` verdict on a diff — not a fabricated empty record.
func TestAbsentInsight(t *testing.T) {
	srv := seedVersions(t)
	if code, _ := do(t, srv, "GET", patchPath, "", nil); code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unreported version, got %d", code)
	}
	_, body := do(t, srv, "GET", "/v1/models/m/diff?from=a&to=b", "", nil)
	if body["verdict"] != "unknown" {
		t.Fatalf("expected unknown verdict, got %v", body["verdict"])
	}
	missing, _ := body["missing"].([]any)
	if len(missing) == 0 || missing[0] != "topology" {
		t.Fatalf("diff should name the missing input: %v", body)
	}
}
