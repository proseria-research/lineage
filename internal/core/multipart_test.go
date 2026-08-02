package core_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// fakeSigningBackend is a signing + multipart-capable StorageBackend for exercising the core
// multipart orchestration offline (no real S3). It records what the core asked it to do.
type fakeSigningBackend struct {
	objects       map[string]domain.ObjectInfo
	completedWith []domain.MultipartPart
	initiated     bool
}

func newFakeBackend() *fakeSigningBackend {
	return &fakeSigningBackend{objects: map[string]domain.ObjectInfo{}}
}

func (f *fakeSigningBackend) Name() string { return "default" }
func (f *fakeSigningBackend) Capabilities() domain.StorageCapabilities {
	return domain.StorageCapabilities{Signing: true, SignPut: true, Ranges: true, Multipart: true}
}
func (f *fakeSigningBackend) Stat(_ context.Context, uri string) (domain.ObjectInfo, error) {
	if o, ok := f.objects[uri]; ok {
		return o, nil
	}
	return domain.ObjectInfo{Exists: false}, nil
}
func (f *fakeSigningBackend) SignGet(context.Context, string, time.Duration) (string, error) {
	return "https://signed/get", nil
}
func (f *fakeSigningBackend) SignPut(context.Context, string, time.Duration) (domain.SignedRequest, error) {
	return domain.SignedRequest{URL: "https://signed/put", Method: "PUT"}, nil
}
func (f *fakeSigningBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (f *fakeSigningBackend) Put(_ context.Context, path string, r io.Reader, size int64, _ string) (string, error) {
	_, _ = io.Copy(io.Discard, r)
	uri := f.URIFor(path)
	f.objects[uri] = domain.ObjectInfo{Exists: true, SizeBytes: size}
	return uri, nil
}
func (f *fakeSigningBackend) URIFor(path string) string { return "s3://bucket/" + path }
func (f *fakeSigningBackend) Delete(_ context.Context, uri string) error {
	delete(f.objects, uri)
	return nil
}
func (f *fakeSigningBackend) InitiateMultipart(_ context.Context, _ string, parts int, partSize int64, _ time.Duration) (domain.MultipartPlan, error) {
	f.initiated = true
	plan := domain.MultipartPlan{UploadID: "mpu-1", PartSize: partSize, Parts: make([]domain.MultipartPart, parts)}
	for i := range parts {
		plan.Parts[i] = domain.MultipartPart{PartNumber: i + 1, URL: "https://signed/part"}
	}
	return plan, nil
}
func (f *fakeSigningBackend) CompleteMultipart(_ context.Context, path, uploadID string, parts []domain.MultipartPart) (string, error) {
	f.completedWith = parts
	uri := f.URIFor(path)
	// Completing "assembles" the object; record it so finalize's Stat can verify.
	f.objects[uri] = domain.ObjectInfo{Exists: true, SizeBytes: 100 << 20, Digest: "sha256:abc123"}
	return uri, nil
}
func (f *fakeSigningBackend) AbortMultipart(context.Context, string, string) error { return nil }
func (f *fakeSigningBackend) ListObjects(context.Context, string) ([]domain.ObjectRef, error) {
	return nil, nil
}

func newFakeSvc(b domain.StorageBackend) *core.Service {
	return core.New(memstore.New(), map[string]domain.StorageBackend{b.Name(): b}, b.Name(), memcache.New(), events.New())
}

// A large file on a signing+multipart backend must yield a multipart ticket, and finalize
// must complete the multipart upload with the client ETags before recording the artifact.
func TestMultipartUploadFlow(t *testing.T) {
	ctx := context.Background()
	fb := newFakeBackend()
	s := newFakeSvc(fb)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "big"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "big", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}

	ticket, err := s.InitiateUpload(ctx, "me", "big", "1.0.0", core.InitiateUploadInput{
		Name: "weights.safetensors", SizeBytes: 100 << 20, // 100 MiB → multipart
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ticket.Multipart || !fb.initiated || len(ticket.Parts) == 0 {
		t.Fatalf("expected a multipart ticket with parts, got %+v", ticket)
	}
	if ticket.Parts[0].URL == "" || ticket.Parts[0].PartNumber != 1 {
		t.Fatalf("bad part descriptor: %+v", ticket.Parts[0])
	}

	// Client uploaded each part and reports ETags.
	reported := []domain.MultipartPart{{PartNumber: 1, ETag: "etag-1"}, {PartNumber: 2, ETag: "etag-2"}}
	art, err := s.FinalizeUpload(ctx, "me", "big", "1.0.0", ticket.UploadID, "sha256:abc123", reported)
	if err != nil {
		t.Fatal(err)
	}
	if len(fb.completedWith) != 2 {
		t.Fatalf("CompleteMultipart should receive the 2 reported parts, got %v", fb.completedWith)
	}
	if art.URI != "s3://bucket/big/1.0.0/weights.safetensors" || art.Digest != "sha256:abc123" {
		t.Fatalf("unexpected artifact: %+v", art)
	}
}

// Multipart finalize without part ETags is rejected (and must abort the upload).
func TestMultipartFinalizeRequiresParts(t *testing.T) {
	ctx := context.Background()
	fb := newFakeBackend()
	s := newFakeSvc(fb)
	_, _ = s.CreateModel(ctx, "me", core.CreateModelInput{Name: "big"})
	_, _, _ = s.PublishVersion(ctx, "me", "big", core.PublishVersionInput{Name: "1.0.0"})
	ticket, err := s.InitiateUpload(ctx, "me", "big", "1.0.0", core.InitiateUploadInput{Name: "w.bin", SizeBytes: 100 << 20})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.FinalizeUpload(ctx, "me", "big", "1.0.0", ticket.UploadID, "sha256:abc123", nil)
	if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeInvalidArgument {
		t.Fatalf("want invalid-argument when parts missing, got %v", err)
	}
}
