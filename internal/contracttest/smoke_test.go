package contracttest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	"github.com/proseria-research/lineage/internal/adapters/storage/s3"
	"github.com/proseria-research/lineage/internal/contracttest"
	"github.com/proseria-research/lineage/internal/domain"
)

const smokeBucket = "lineage-smoke"

// fakeS3 serves one object, and only to a SigV4-signed request: a presigned URL (query
// signature, the consumer path) or an Authorization header (the server's stream-through).
// It checks that a signature is present and addressed at the right object — the SigV4 maths
// itself is pinned by the s3 package's test vector and live MinIO runs.
func fakeS3(t *testing.T, key string, payload []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signed := r.URL.Query().Get("X-Amz-Signature") != "" ||
			strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ")
		switch {
		case !signed:
			http.Error(w, "unsigned", http.StatusForbidden)
		case r.Method != http.MethodGet || r.URL.Path != "/"+smokeBucket+"/"+key:
			http.NotFound(w, r)
		default:
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// M19 core smoke: the default build (no tags) on Postgres resolves, fetches, signs URLs and
// seals an audit epoch. The default backend is a filesystem (no signing: stream-through, the
// path an unsigned backend always takes); S3 beside it, against an in-process endpoint, signs
// its own artifacts — resolve signs each artifact with the backend it lives on.
func TestCoreSmokeOnPostgres(t *testing.T) {
	weights := []byte("smoke-model-weights-0123456789")
	readme := []byte("# smoke\n")
	const key = "smoke/1.0.0/model.bin"
	s3srv := fakeS3(t, key, weights)

	sb, err := s3.New("s3", s3.Config{Bucket: smokeBucket, Region: "us-east-1", Endpoint: s3srv.URL,
		AccessKey: "AKIDSMOKE", SecretKey: "smoke-secret", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	fsRoot := t.TempDir()
	fb := fs.New("fs", fsRoot)
	if err := os.WriteFile(filepath.Join(fsRoot, "README.md"), readme, 0o644); err != nil {
		t.Fatal(err)
	}
	reg := newRegistry(t, "postgres", registryOpts{
		backends:   map[string]domain.StorageBackend{sb.Name(): sb, fb.Name(): fb},
		defBackend: fb.Name(),
	})
	c := (&contracttest.Client{Base: reg.ModelAPI.URL}).As("ci@acme.example")
	must := func(want int, method, path, body string) map[string]any {
		t.Helper()
		out, err := c.Must(want, method, path, body)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	sum := sha256.Sum256(weights)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	must(http.StatusCreated, "POST", "/v1/models", `{"name":"smoke"}`)
	must(http.StatusCreated, "POST", "/v1/models/smoke/versions", `{"name":"1.0.0","artifacts":[{"kind":"MODEL",
		"name":"model.bin","uri":"s3://`+smokeBucket+`/`+key+`","storageBackend":"s3",
		"digest":"`+digest+`","sizeBytes":`+strconv.Itoa(len(weights))+`}]}`)
	// Registered by reference; the fs backend's Stat fills in digest and size (§05.5).
	doc := must(http.StatusCreated, "POST", "/v1/models/smoke/versions/1.0.0/artifacts",
		`{"kind":"DOC","name":"README.md","uri":"file://`+filepath.Join(fsRoot, "README.md")+`","storageBackend":"fs"}`)
	rsum := sha256.Sum256(readme)
	if doc["digest"] != "sha256:"+hex.EncodeToString(rsum[:]) || doc["sizeBytes"] != json.Number(strconv.Itoa(len(readme))) {
		t.Fatalf("fs Stat did not fill digest and size: %v", doc)
	}
	for _, to := range []string{"staging", "production"} {
		must(http.StatusOK, "POST", "/v1/models/smoke/versions/1.0.0:transition", `{"to":"`+to+`"}`)
	}

	// Resolve: the production version, its digest, and a signed URL for the weights from S3,
	// the non-default backend; the fs README gets none.
	res := must(http.StatusOK, "GET", "/v1/models/smoke/resolve", "")
	if res["version"] != "1.0.0" || res["stage"] != "production" || res["digest"] != digest {
		t.Fatalf("resolve: %v", res)
	}
	var signed string
	for _, a := range res["artifacts"].([]any) {
		switch a := a.(map[string]any); a["name"] {
		case "model.bin":
			signed, _ = a["signedUrl"].(string)
		case "README.md":
			if a["signedUrl"] != nil {
				t.Fatalf("fs artifact got a signedUrl: %v", a["signedUrl"])
			}
		}
	}
	if !strings.HasPrefix(signed, s3srv.URL+"/"+smokeBucket+"/"+key+"?") || !strings.Contains(signed, "X-Amz-Signature=") {
		t.Fatalf("signedUrl = %q", signed)
	}
	if got := get(t, http.DefaultClient, signed, http.StatusOK); string(got) != string(weights) {
		t.Fatalf("signed URL served %q", got)
	}

	// Fetch: a 302 to a fresh signed URL by default, stream-through on request, and
	// stream-through always for a backend that cannot sign.
	content := reg.ModelAPI.URL + "/v1/models/smoke/versions/1.0.0/artifacts/"
	resp, err := noRedirect.Get(content + "model.bin/content")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || !strings.Contains(loc, "X-Amz-Signature=") {
		t.Fatalf("fetch: %d Location=%q", resp.StatusCode, loc)
	}
	if got := get(t, http.DefaultClient, content+"model.bin/content", http.StatusOK); string(got) != string(weights) {
		t.Fatalf("fetch via redirect served %q", got)
	}
	if got := get(t, noRedirect, content+"model.bin/content?mode=stream", http.StatusOK); string(got) != string(weights) {
		t.Fatalf("fetch ?mode=stream served %q", got)
	}
	if got := get(t, noRedirect, content+"README.md/content", http.StatusOK); string(got) != string(readme) {
		t.Fatalf("fs stream-through served %q", got)
	}

	// Seal. The sealer's ticker only ever calls SealDue; driving it with a `now` past this
	// epoch's close plus grace seals every row written above, deterministically.
	att := domain.DefaultAttestation
	sealed, err := reg.Svc.SealDue(context.Background(), domain.NowMillis()+att.IntervalMillis()+att.GraceMillis()+1)
	if err != nil {
		t.Fatal(err)
	}
	_, all, err := c.GetAll("/v1/audit?pageSize=500")
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Sealed < 1 || sealed.Leaves != int64(len(all)) {
		t.Fatalf("sealed %+v, want every one of the %d audit events", sealed, len(all))
	}
	// And the seal is visible from outside, as a verifier would check it.
	v := must(http.StatusOK, "GET", "/v1/audit:verify", "")
	if v["ok"] != true || v["epochsChecked"] == json.Number("0") || v["firstBreak"] != nil {
		t.Fatalf("verify: %v", v)
	}
	_, events, err := c.GetAll("/v1/models/smoke/audit")
	if err != nil || len(events) == 0 {
		t.Fatalf("audit: %v %v", events, err)
	}
	id := events[0].(map[string]any)["id"].(string)
	if p := must(http.StatusOK, "GET", "/v1/audit/"+id+":proof", ""); p["id"] != id {
		t.Fatalf("proof: %v", p)
	}
}

func get(t *testing.T, hc *http.Client, url string, want int) []byte {
	t.Helper()
	resp, err := hc.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("GET %s: %d %s", url, resp.StatusCode, body)
	}
	return body
}
