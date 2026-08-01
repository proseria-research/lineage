package modelapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/api/modelapi"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

func apiServer(t *testing.T) *httptest.Server {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New())
	srv := httptest.NewServer(modelapi.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)
	return srv
}

// do issues a request and returns status + decoded JSON (nil for empty bodies).
func do(t *testing.T, srv *httptest.Server, method, path, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Lineage-Actor", "tester")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return resp.StatusCode, m
}

func TestModelLifecycle(t *testing.T) {
	srv := apiServer(t)
	if st, _ := do(t, srv, "POST", "/v1/models", `{"name":"m1","owner":"team"}`, nil); st != 201 {
		t.Fatalf("create model = %d", st)
	}
	// PATCH mutable fields.
	st, m := do(t, srv, "PATCH", "/v1/models/m1", `{"description":"scorer","owner":"risk"}`, nil)
	if st != 200 || m["description"] != "scorer" || m["owner"] != "risk" {
		t.Fatalf("patch model = %d %+v", st, m)
	}
	// Archive → ARCHIVED, then PATCH back to ACTIVE.
	if st, m := do(t, srv, "POST", "/v1/models/m1:archive", "", nil); st != 200 || m["state"] != "ARCHIVED" {
		t.Fatalf("archive = %d %+v", st, m)
	}
	if _, m := do(t, srv, "PATCH", "/v1/models/m1", `{"state":"ACTIVE"}`, nil); m["state"] != "ACTIVE" {
		t.Fatalf("un-archive failed: %+v", m)
	}
	// Delete (no production version) → 204, then 404.
	if st, _ := do(t, srv, "DELETE", "/v1/models/m1", "", nil); st != 204 {
		t.Fatalf("delete = %d", st)
	}
	if st, _ := do(t, srv, "GET", "/v1/models/m1", "", nil); st != 404 {
		t.Fatalf("get after delete = %d", st)
	}
}

func TestDeleteGuards(t *testing.T) {
	srv := apiServer(t)
	do(t, srv, "POST", "/v1/models", `{"name":"m"}`, nil)
	do(t, srv, "POST", "/v1/models/m/versions", `{"name":"1.0.0"}`, nil)
	do(t, srv, "POST", "/v1/models/m/versions/1.0.0:transition", `{"to":"staging"}`, nil)
	do(t, srv, "POST", "/v1/models/m/versions/1.0.0:transition", `{"to":"production"}`, nil)

	// Deleting a production version is blocked without force.
	if st, _ := do(t, srv, "DELETE", "/v1/models/m/versions/1.0.0", "", nil); st != 409 {
		t.Fatalf("delete production version = %d, want 409", st)
	}
	// Deleting the model is blocked while a version is in production.
	if st, _ := do(t, srv, "DELETE", "/v1/models/m", "", nil); st != 409 {
		t.Fatalf("delete model with production = %d, want 409", st)
	}
	// force overrides.
	if st, _ := do(t, srv, "DELETE", "/v1/models/m?force=true", "", nil); st != 204 {
		t.Fatalf("force delete model = %d, want 204", st)
	}
}

func TestArtifactCrudAndImmutability(t *testing.T) {
	srv := apiServer(t)
	do(t, srv, "POST", "/v1/models", `{"name":"m"}`, nil)
	do(t, srv, "POST", "/v1/models/m/versions", `{"name":"1.0.0"}`, nil)
	if st, _ := do(t, srv, "POST", "/v1/models/m/versions/1.0.0/artifacts",
		`{"name":"model.onnx","uri":"s3://m/1.0.0/model.onnx","digest":"sha256:abc","sizeBytes":10}`, nil); st != 201 {
		t.Fatalf("register artifact = %d", st)
	}
	// List + get.
	if st, m := do(t, srv, "GET", "/v1/models/m/versions/1.0.0/artifacts", "", nil); st != 200 || len(m["items"].([]any)) != 1 {
		t.Fatalf("list artifacts = %d %+v", st, m)
	}
	if st, a := do(t, srv, "GET", "/v1/models/m/versions/1.0.0/artifacts/model.onnx", "", nil); st != 200 || a["digest"] != "sha256:abc" {
		t.Fatalf("get artifact = %d %+v", st, a)
	}
	// PATCH metadata OK.
	if st, a := do(t, srv, "PATCH", "/v1/models/m/versions/1.0.0/artifacts/model.onnx", `{"mediaType":"application/octet-stream"}`, nil); st != 200 || a["mediaType"] != "application/octet-stream" {
		t.Fatalf("patch artifact meta = %d %+v", st, a)
	}
	// PATCH content field to a changed value → 409 (immutable).
	if st, _ := do(t, srv, "PATCH", "/v1/models/m/versions/1.0.0/artifacts/model.onnx", `{"digest":"sha256:different"}`, nil); st != 409 {
		t.Fatalf("immutable digest patch = %d, want 409", st)
	}
	// DELETE.
	if st, _ := do(t, srv, "DELETE", "/v1/models/m/versions/1.0.0/artifacts/model.onnx", "", nil); st != 204 {
		t.Fatalf("delete artifact = %d", st)
	}
}

func TestLineageAndDeployments(t *testing.T) {
	srv := apiServer(t)
	do(t, srv, "POST", "/v1/models", `{"name":"m"}`, nil)
	do(t, srv, "POST", "/v1/models/m/versions", `{"name":"1.0.0"}`, nil)
	do(t, srv, "POST", "/v1/models/m/versions", `{"name":"2.0.0"}`, nil)

	// Lineage: 2.0.0 derived_from 1.0.0.
	st, e := do(t, srv, "POST", "/v1/models/m/versions/2.0.0/lineage", `{"relation":"derived_from","to":{"version":"1.0.0"}}`, nil)
	if st != 201 || e["relation"] != "derived_from" {
		t.Fatalf("add lineage = %d %+v", st, e)
	}
	edgeID := e["id"].(string)
	if _, l := do(t, srv, "GET", "/v1/models/m/versions/2.0.0/lineage", "", nil); len(l["items"].([]any)) != 1 {
		t.Fatalf("list lineage: %+v", l)
	}
	// Graph traversal: upstream from 2.0.0 reaches 1.0.0 via the edge (§07.3).
	if st, g := do(t, srv, "GET", "/v1/models/m/versions/2.0.0/lineage?direction=upstream", "", nil); st != 200 {
		t.Fatalf("lineage graph = %d", st)
	} else {
		labels := map[string]bool{}
		for _, n := range g["nodes"].([]any) {
			labels[n.(map[string]any)["label"].(string)] = true
		}
		if !labels["m@2.0.0"] || !labels["m@1.0.0"] {
			t.Fatalf("upstream graph missing nodes: %v", labels)
		}
		if len(g["edges"].([]any)) != 1 {
			t.Fatalf("upstream graph edges = %v", g["edges"])
		}
	}

	if st, _ := do(t, srv, "DELETE", "/v1/models/m/versions/2.0.0/lineage/"+edgeID, "", nil); st != 204 {
		t.Fatalf("delete lineage = %d", st)
	}

	// Deployments.
	st, d := do(t, srv, "POST", "/v1/models/m/versions/2.0.0/deployments", `{"environment":"prod","endpointUri":"https://svc"}`, nil)
	if st != 201 || d["status"] != "ACTIVE" {
		t.Fatalf("create deployment = %d %+v", st, d)
	}
	depID := d["id"].(string)
	if _, dd := do(t, srv, "PATCH", "/v1/models/m/versions/2.0.0/deployments/"+depID, `{"status":"INACTIVE"}`, nil); dd["status"] != "INACTIVE" {
		t.Fatalf("patch deployment: %+v", dd)
	}
	if st, _ := do(t, srv, "DELETE", "/v1/models/m/versions/2.0.0/deployments/"+depID, "", nil); st != 204 {
		t.Fatalf("delete deployment = %d", st)
	}
}

func TestIdempotencyKey(t *testing.T) {
	srv := apiServer(t)
	h := map[string]string{"Idempotency-Key": "abc-123"}
	st1, m1 := do(t, srv, "POST", "/v1/models", `{"name":"idem"}`, h)
	st2, m2 := do(t, srv, "POST", "/v1/models", `{"name":"idem"}`, h)
	if st1 != 201 || st2 != 201 {
		t.Fatalf("idempotent create statuses = %d %d", st1, st2)
	}
	if m1["id"] != m2["id"] {
		t.Fatalf("idempotent retry returned a different id: %v vs %v", m1["id"], m2["id"])
	}
	// Exactly one model exists.
	if _, l := do(t, srv, "GET", "/v1/models", "", nil); len(l["items"].([]any)) != 1 {
		t.Fatalf("idempotency created duplicates: %+v", l["items"])
	}
}

// custom_properties (cp.*) filtering is Postgres-only (§02.7); on the memory/SQLite engines
// it must be rejected with a clear 400 rather than silently ignored.
func TestCustomPropFilterPostgresOnly(t *testing.T) {
	srv := apiServer(t) // memory-backed
	if st, _ := do(t, srv, "GET", "/v1/models?cp.costCenter=R-42", "", nil); st != 400 {
		t.Fatalf("cp.* filter on a non-postgres engine = %d, want 400", st)
	}
	// Label filtering, by contrast, works everywhere.
	if st, _ := do(t, srv, "GET", "/v1/models?label.tier=gold", "", nil); st != 200 {
		t.Fatalf("label filter = %d, want 200", st)
	}
}

func TestCursorPagination(t *testing.T) {
	srv := apiServer(t)
	const n = 5
	for i := 0; i < n; i++ {
		do(t, srv, "POST", "/v1/models", fmt.Sprintf(`{"name":"m%d"}`, i), nil)
	}
	seen := map[string]bool{}
	path := "/v1/models?pageSize=2"
	pages := 0
	for {
		st, m := do(t, srv, "GET", path, "", nil)
		if st != 200 {
			t.Fatalf("list page = %d", st)
		}
		items := m["items"].([]any)
		for _, it := range items {
			seen[it.(map[string]any)["name"].(string)] = true
		}
		pages++
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
		tok, _ := m["nextPageToken"].(string)
		if tok == "" {
			break
		}
		path = "/v1/models?pageSize=2&pageToken=" + tok
	}
	if len(seen) != n {
		t.Fatalf("paginated over %d unique models, want %d", len(seen), n)
	}
	if pages < 3 {
		t.Fatalf("expected multiple pages for pageSize=2 over %d, got %d", n, pages)
	}
}

func TestAuditFeedAndOpenAPI(t *testing.T) {
	srv := apiServer(t)
	do(t, srv, "POST", "/v1/models", `{"name":"m"}`, nil)

	// Model audit trail has the create event.
	if st, a := do(t, srv, "GET", "/v1/models/m/audit", "", nil); st != 200 || len(a["items"].([]any)) < 1 {
		t.Fatalf("model audit = %d %+v", st, a)
	}
	// Global feed.
	if st, a := do(t, srv, "GET", "/v1/audit", "", nil); st != 200 || len(a["items"].([]any)) < 1 {
		t.Fatalf("audit feed = %d %+v", st, a)
	}

	// OpenAPI is valid and describes the resources.
	st, spec := do(t, srv, "GET", "/v1/openapi.json", "", nil)
	if st != 200 {
		t.Fatalf("openapi = %d", st)
	}
	if spec["openapi"] != "3.1.0" {
		t.Fatalf("bad openapi version: %v", spec["openapi"])
	}
	paths := spec["paths"].(map[string]any)
	for _, want := range []string{"/v1/models", "/v1/models/{model}", "/v1/models/{model}/versions/{version}/artifacts", "/v1/models/{model}/versions/{version}/deployments", "/v1/models/{model}/versions/{version}/insight", "/v1/models/{model}/versions/{version}/evaluations", "/v1/models/{model}/diff"} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("openapi missing path %q", want)
		}
	}

	// Every $ref must resolve to a declared component (catches hand-authoring typos).
	for _, ref := range collectRefs(spec) {
		if !refExists(spec, ref) {
			t.Fatalf("openapi dangling $ref: %s", ref)
		}
	}
}

func collectRefs(v any) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					out = append(out, s)
				}
				continue
			}
			out = append(out, collectRefs(val)...)
		}
	case []any:
		for _, e := range t {
			out = append(out, collectRefs(e)...)
		}
	}
	return out
}

func refExists(spec map[string]any, ref string) bool {
	parts := bytesSplit(ref) // "#/components/schemas/Model" → [components schemas Model]
	var cur any = spec
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = m[p]
		if !ok {
			return false
		}
	}
	return true
}

func bytesSplit(ref string) []string {
	var out []string
	for _, p := range bytes.Split([]byte(ref), []byte("/")) {
		s := string(p)
		if s == "" || s == "#" {
			continue
		}
		out = append(out, s)
	}
	return out
}
