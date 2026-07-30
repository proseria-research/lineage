package core_test

import (
	"context"
	"strings"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// newSvcWithBackend returns a service plus the concrete fs backend, so a test can plant
// orphan objects directly for GC.
func newSvcWithBackend(t *testing.T) (*core.Service, *fs.Backend) {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	svc := core.New(
		memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend},
		backend.Name(),
		memcache.New(),
		events.New(),
	)
	return svc, backend
}

// SweepGarbage deletes backend objects that no artifact row references, and retains those
// that are still referenced (§05.8).
func TestGCSweepRetainsReferenced(t *testing.T) {
	ctx := context.Background()
	s, backend := newSvcWithBackend(t)

	// A referenced artifact (uploaded through the registry).
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	ticket, _ := s.InitiateUpload(ctx, "me", "m", "1.0.0", core.InitiateUploadInput{Name: "keep.bin"})
	keep := []byte("keep me")
	if err := s.UploadContent(ctx, ticket.UploadID, strings.NewReader(string(keep)), int64(len(keep))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinalizeUpload(ctx, "me", "m", "1.0.0", ticket.UploadID, digestOf(keep), nil); err != nil {
		t.Fatal(err)
	}

	// An orphan object with no artifact row, written directly to the backend.
	orphanURI, err := backend.Put(ctx, "orphans/junk.bin", strings.NewReader("garbage"), 7, "")
	if err != nil {
		t.Fatal(err)
	}

	// grace=0 so the just-written orphan is immediately eligible.
	res, err := s.SweepGarbage(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 {
		t.Fatalf("expected exactly 1 orphan deleted, got %d (scanned %d)", res.Deleted, res.Scanned)
	}
	// Orphan gone, referenced artifact retained.
	if info, _ := backend.Stat(ctx, orphanURI); info.Exists {
		t.Fatalf("orphan should have been swept")
	}
	if info, _ := backend.Stat(ctx, "file://m/1.0.0/keep.bin"); !info.Exists {
		t.Fatalf("referenced artifact must be retained")
	}
}

func TestPlanParts(t *testing.T) {
	// Small-ish large file: 100 MiB with a 5 MiB minimum part → 20 parts.
	if parts, size := core.PlanPartsForTest(100 << 20); parts != 20 || size != 5<<20 {
		t.Fatalf("100MiB → parts=%d size=%d, want 20 / 5MiB", parts, size)
	}
	// Enormous file must stay within the 10000-part cap by growing the part size.
	parts, size := core.PlanPartsForTest(int64(80) << 40) // 80 TiB
	if parts > 10000 {
		t.Fatalf("part count %d exceeds S3 max", parts)
	}
	if int64(parts)*size < int64(80)<<40 {
		t.Fatalf("parts*size (%d) must cover the object", int64(parts)*size)
	}
}
