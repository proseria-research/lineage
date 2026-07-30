package modelapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/api/modelapi"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// deliverySetup builds a full service on fs + memstore behind an httptest server, and
// publishes model "m" version "1.0.0" with an uploaded MODEL artifact promoted to production.
func deliverySetup(t *testing.T) (*httptest.Server, *core.Service, []byte) {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New())
	srv := httptest.NewServer(modelapi.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)

	ctx := context.Background()
	payload := []byte("model-weights-0123456789")
	mustPublish(t, svc, "m", "1.0.0", "model.bin", payload)
	if _, err := svc.Transition(ctx, "me", "m", "1.0.0", domain.StageStaging, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transition(ctx, "me", "m", "1.0.0", domain.StageProduction, ""); err != nil {
		t.Fatal(err)
	}
	return srv, svc, payload
}

func mustPublish(t *testing.T, svc *core.Service, model, version, artifact string, payload []byte) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.GetModel(ctx, model); err != nil {
		if _, err := svc.CreateModel(ctx, "me", core.CreateModelInput{Name: model}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := svc.PublishVersion(ctx, "me", model, core.PublishVersionInput{Name: version}); err != nil {
		t.Fatal(err)
	}
	tk, err := svc.InitiateUpload(ctx, "me", model, version, core.InitiateUploadInput{Name: artifact})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UploadContent(ctx, tk.UploadID, strings.NewReader(string(payload)), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	if _, err := svc.FinalizeUpload(ctx, "me", model, version, tk.UploadID, "sha256:"+hex.EncodeToString(sum[:]), nil); err != nil {
		t.Fatal(err)
	}
}

func TestResolveETagAnd304(t *testing.T) {
	srv, _, payload := deliverySetup(t)
	sum := sha256.Sum256(payload)
	wantETag := `"sha256:` + hex.EncodeToString(sum[:]) + `"`

	resp, err := http.Get(srv.URL + "/v1/models/m/resolve?stage=production")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("resolve status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("ETag"); got != wantETag {
		t.Fatalf("ETag=%q want %q", got, wantETag)
	}
	if cc := resp.Header.Get("Cache-Control"); cc == "" {
		t.Fatalf("expected Cache-Control on resolve")
	}

	// Conditional request with the matching ETag → 304, no body.
	req, _ := http.NewRequest("GET", srv.URL+"/v1/models/m/resolve?stage=production", nil)
	req.Header.Set("If-None-Match", wantETag)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotModified || len(body) != 0 {
		t.Fatalf("want 304 with empty body, got %d len=%d", resp2.StatusCode, len(body))
	}
}

func TestContentStreamRangeAnd304(t *testing.T) {
	srv, _, payload := deliverySetup(t)
	base := srv.URL + "/v1/models/m/versions/1.0.0/artifacts/model.bin/content"

	// fs can't sign → stream-through 200 with the exact bytes.
	resp, err := http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(got) != string(payload) {
		t.Fatalf("content stream: status=%d body=%q", resp.StatusCode, got)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag on content")
	}

	// Conditional → 304.
	req, _ := http.NewRequest("GET", base, nil)
	req.Header.Set("If-None-Match", etag)
	r304, _ := http.DefaultClient.Do(req)
	r304.Body.Close()
	if r304.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", r304.StatusCode)
	}

	// Range request → 206 partial content (ServeContent on the seekable fs file).
	rreq, _ := http.NewRequest("GET", base, nil)
	rreq.Header.Set("Range", "bytes=0-4")
	rr, _ := http.DefaultClient.Do(rreq)
	part, _ := io.ReadAll(rr.Body)
	rr.Body.Close()
	if rr.StatusCode != http.StatusPartialContent || string(part) != string(payload[:5]) {
		t.Fatalf("range: status=%d part=%q want %q", rr.StatusCode, part, payload[:5])
	}
}

// Promoting a new version must be reflected immediately on the next resolve — proving the
// resolve cache is invalidated by the version.stage_changed event (§04.4), not TTL-bound.
func TestResolveCacheInvalidatedByEvent(t *testing.T) {
	srv, svc, _ := deliverySetup(t)
	ctx := context.Background()

	if v := resolveVersion(t, srv.URL); v != "1.0.0" {
		t.Fatalf("initial production resolve = %s, want 1.0.0", v)
	}

	// Publish 2.0.0 and promote it — auto-demotes 1.0.0.
	mustPublish(t, svc, "m", "2.0.0", "model.bin", []byte("v2-weights"))
	if _, err := svc.Transition(ctx, "me", "m", "2.0.0", domain.StageStaging, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transition(ctx, "me", "m", "2.0.0", domain.StageProduction, ""); err != nil {
		t.Fatal(err)
	}

	if v := resolveVersion(t, srv.URL); v != "2.0.0" {
		t.Fatalf("after promotion production resolve = %s, want 2.0.0 (stale cache?)", v)
	}
}

func resolveVersion(t *testing.T, base string) string {
	t.Helper()
	resp, err := http.Get(base + "/v1/models/m/resolve?stage=production")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var r struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatal(err)
	}
	return r.Version
}
