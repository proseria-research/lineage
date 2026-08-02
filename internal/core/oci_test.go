package core_test

import (
	"context"
	"strings"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/oci"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// These cover what the OCI driver changes about the *core*, not about the registry protocol
// (that is the driver's own suite): the resolve response gains a pullable image reference,
// uploads fall back to stream-through because the registry has no presignable write target,
// and the GC sweeper treats a backend it cannot enumerate as a no-op rather than an error.

func newOCIService(t *testing.T) *core.Service {
	t.Helper()
	backend, err := oci.New("default", oci.Config{Registry: "registry.test:5000", Repository: "lineage", PlainHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	return core.New(
		memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend},
		backend.Name(),
		memcache.New(),
		events.New(),
	)
}

// A version whose MODEL artifacts share one manifest resolves to that image, which is what a
// modelcars InferenceService needs in storageUri (§04.6).
func TestResolveReportsOCIImage(t *testing.T) {
	ctx := context.Background()
	s := newOCIService(t)

	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "fraud"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "fraud", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	const image = "oci://registry.test:5000/lineage/fraud:1.0.0"
	for _, name := range []string{"model.onnx", "config.json"} {
		if _, err := s.RegisterArtifact(ctx, "me", "fraud", "1.0.0", core.ArtifactInput{
			Kind: domain.KindModel, Name: name, URI: image + "#" + name,
			Digest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, to := range []domain.Stage{domain.StageStaging, domain.StageProduction} {
		if _, err := s.Transition(ctx, "me", "fraud", "1.0.0", to, "ship it"); err != nil {
			t.Fatal(err)
		}
	}

	r, err := s.Resolve(ctx, "fraud", domain.Selector{})
	if err != nil {
		t.Fatal(err)
	}
	if r.OCIImage != image {
		t.Fatalf("ociImage = %q, want %q", r.OCIImage, image)
	}
	// The per-artifact pointer keeps its fragment: it addresses a file, the image does not.
	for _, a := range r.Artifacts {
		if want := image + "#" + a.Name; a.StorageURI != want {
			t.Fatalf("storageUri = %q, want the layer-qualified ref %q", a.StorageURI, want)
		}
	}
}

// Artifacts spread across two images have no single pullable reference, and claiming one
// would tell a consumer that pulling it yields the whole model. Better to omit the field.
func TestResolveOmitsOCIImageWhenSplit(t *testing.T) {
	ctx := context.Background()
	s := newOCIService(t)

	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "fraud"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "fraud", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	for uri, name := range map[string]string{
		"oci://registry.test:5000/lineage/fraud:1.0.0#model.onnx": "model.onnx",
		"oci://registry.test:5000/other/weights:v9#extra.bin":     "extra.bin",
	} {
		if _, err := s.RegisterArtifact(ctx, "me", "fraud", "1.0.0", core.ArtifactInput{
			Kind: domain.KindModel, Name: name, URI: uri,
		}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Resolve(ctx, "fraud", domain.Selector{Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if r.OCIImage != "" {
		t.Fatalf("ociImage = %q, want empty when the artifacts span two images", r.OCIImage)
	}
}

// A blob backend must not grow an ociImage — the field is absent, not empty-string noise.
func TestResolveOmitsOCIImageForBlobBackend(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvcWithBackend(t)

	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	ticket, err := s.InitiateUpload(ctx, "me", "m", "1.0.0", core.InitiateUploadInput{Name: "model.bin"})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("weights")
	if err := s.UploadContent(ctx, ticket.UploadID, strings.NewReader(string(body)), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinalizeUpload(ctx, "me", "m", "1.0.0", ticket.UploadID, digestOf(body), nil); err != nil {
		t.Fatal(err)
	}
	r, err := s.Resolve(ctx, "m", domain.Selector{Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if r.OCIImage != "" {
		t.Fatalf("ociImage = %q, want empty for an fs backend", r.OCIImage)
	}
}

// The registry can sign reads but not writes, so an upload takes the stream-through path even
// though Capabilities().Signing is true — the distinction SignPut exists to make (§05.6).
func TestOCIUploadUsesStreamThrough(t *testing.T) {
	ctx := context.Background()
	s := newOCIService(t)

	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "fraud"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "fraud", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	// Large enough that a multipart-capable backend would have offered a part plan.
	ticket, err := s.InitiateUpload(ctx, "me", "fraud", "1.0.0", core.InitiateUploadInput{
		Name: "model.onnx", SizeBytes: 512 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ticket.StreamThrough || ticket.URL != "" || ticket.Multipart {
		t.Fatalf("ticket = %+v, want stream-through with no signed or multipart target", ticket)
	}
}

// Bytes that fail verification are taken back out of the backend. On `oci` this matters more
// than elsewhere: a rejected layer would sit inside the version's manifest, visible to anyone
// pulling the image, with no artifact row and no sweeper that could ever reap it (§05.8).
func TestRejectedUploadDiscardsBytes(t *testing.T) {
	ctx := context.Background()
	s, backend := newSvcWithBackend(t)

	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	ticket, err := s.InitiateUpload(ctx, "me", "m", "1.0.0", core.InitiateUploadInput{Name: "model.bin"})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("weights")
	if err := s.UploadContent(ctx, ticket.UploadID, strings.NewReader(string(body)), int64(len(body))); err != nil {
		t.Fatal(err)
	}

	const wrong = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := s.FinalizeUpload(ctx, "me", "m", "1.0.0", ticket.UploadID, wrong, nil); err == nil {
		t.Fatal("finalize accepted a mismatched digest")
	}
	if info, err := backend.Stat(ctx, "file://m/1.0.0/model.bin"); err != nil || info.Exists {
		t.Fatalf("rejected bytes survived: info=%+v err=%v", info, err)
	}
}

// A backend that cannot be enumerated is skipped by the sweeper, not reported as a failure —
// registry retention is the registry's (§05.8).
func TestGCSkipsBackendThatCannotList(t *testing.T) {
	s := newOCIService(t)
	res, err := s.SweepGarbage(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("sweep over a non-enumerable backend errored: %v", err)
	}
	if res.Scanned != 0 || res.Deleted != 0 {
		t.Fatalf("sweep = %+v, want a no-op", res)
	}
}
