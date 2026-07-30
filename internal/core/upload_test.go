package core_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

func newSvc(t *testing.T) *core.Service {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	return core.New(
		memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend},
		backend.Name(),
		memcache.New(),
		events.New(),
	)
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Full stream-through upload against a non-signing backend (fs): initiate → uploadContent
// → finalize. Verifies digest/size are computed and recorded, and that resolve returns them.
func TestUploadStreamThroughHappyPath(t *testing.T) {
	ctx := context.Background()
	s := newSvc(t)

	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "fraud-detector"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "fraud-detector", core.PublishVersionInput{Name: "1.4.0"}); err != nil {
		t.Fatal(err)
	}

	ticket, err := s.InitiateUpload(ctx, "me", "fraud-detector", "1.4.0", core.InitiateUploadInput{
		Name: "model.onnx", Kind: domain.KindModel,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ticket.StreamThrough || ticket.ContentURL == "" {
		t.Fatalf("fs backend should yield a stream-through ticket, got %+v", ticket)
	}

	payload := []byte("fake onnx bytes \x00\x01\x02")
	if err := s.UploadContent(ctx, ticket.UploadID, strings.NewReader(string(payload)), int64(len(payload))); err != nil {
		t.Fatal(err)
	}

	art, err := s.FinalizeUpload(ctx, "me", "fraud-detector", "1.4.0", ticket.UploadID, digestOf(payload), nil)
	if err != nil {
		t.Fatal(err)
	}
	if art.Digest != digestOf(payload) {
		t.Fatalf("digest = %s, want %s", art.Digest, digestOf(payload))
	}
	if art.SizeBytes != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", art.SizeBytes, len(payload))
	}
	if !strings.HasPrefix(art.URI, "file://") {
		t.Fatalf("uri = %s, want file:// scheme", art.URI)
	}

	// The artifact must be resolvable with its digest surfaced.
	if _, err := s.Transition(ctx, "me", "fraud-detector", "1.4.0", domain.StageStaging, ""); err != nil {
		t.Fatal(err)
	}
	res, err := s.Resolve(ctx, "fraud-detector", domain.Selector{Version: "1.4.0"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Digest != digestOf(payload) || len(res.Artifacts) != 1 {
		t.Fatalf("resolve did not surface uploaded artifact: %+v", res)
	}
}

// A declared digest that doesn't match the uploaded bytes must fail finalize (§05.6),
// and no artifact row may be created.
func TestUploadDigestMismatchRejected(t *testing.T) {
	ctx := context.Background()
	s := newSvc(t)
	mustSetup(t, s)

	ticket, err := s.InitiateUpload(ctx, "me", "m", "1.0.0", core.InitiateUploadInput{Name: "a.bin"})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello")
	if err := s.UploadContent(ctx, ticket.UploadID, strings.NewReader(string(payload)), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	_, err = s.FinalizeUpload(ctx, "me", "m", "1.0.0", ticket.UploadID, "sha256:deadbeef", nil)
	if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeUnprocessable {
		t.Fatalf("want unprocessable digest-mismatch error, got %v", err)
	}
}

// After a successful upload, re-initiating the same artifact name is rejected — artifacts
// are write-once/immutable (§05.5).
func TestUploadImmutableName(t *testing.T) {
	ctx := context.Background()
	s := newSvc(t)
	mustSetup(t, s)

	t1, _ := s.InitiateUpload(ctx, "me", "m", "1.0.0", core.InitiateUploadInput{Name: "a.bin"})
	p := []byte("x")
	if err := s.UploadContent(ctx, t1.UploadID, strings.NewReader(string(p)), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinalizeUpload(ctx, "me", "m", "1.0.0", t1.UploadID, digestOf(p), nil); err != nil {
		t.Fatal(err)
	}
	_, err := s.InitiateUpload(ctx, "me", "m", "1.0.0", core.InitiateUploadInput{Name: "a.bin"})
	if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeAlreadyExists {
		t.Fatalf("want already-exists on duplicate artifact name, got %v", err)
	}
}

func mustSetup(t *testing.T, s *core.Service) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
}
