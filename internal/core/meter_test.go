package core_test

import (
	"context"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

type fakeMeter struct {
	hit, miss, published, transitions, demotions, finalized, mismatch, signed int
}

func (m *fakeMeter) ResolveServed(hit bool) {
	if hit {
		m.hit++
	} else {
		m.miss++
	}
}
func (m *fakeMeter) VersionPublished() { m.published++ }
func (m *fakeMeter) StageTransitioned(_ domain.Stage, demoted bool) {
	m.transitions++
	if demoted {
		m.demotions++
	}
}
func (m *fakeMeter) UploadFinalized(_ float64, mismatch bool) {
	m.finalized++
	if mismatch {
		m.mismatch++
	}
}
func (m *fakeMeter) SignedURLMinted() { m.signed++ }

func TestMeterHooksFire(t *testing.T) {
	ctx := context.Background()
	fm := &fakeMeter{}
	fb := newFakeBackend() // signing backend so resolve mints signed URLs
	svc := core.New(memstore.New(), map[string]domain.StorageBackend{fb.Name(): fb}, fb.Name(),
		memcache.New(), events.New(), core.WithMeter(fm))

	if _, err := svc.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	// Publish two versions with an artifact each.
	for _, v := range []string{"1.0.0", "2.0.0"} {
		if _, _, err := svc.PublishVersion(ctx, "me", "m", core.PublishVersionInput{
			Name:      v,
			Artifacts: []core.ArtifactInput{{Name: "model.bin", URI: "s3://bucket/m/" + v, Digest: "sha256:abc", SizeBytes: 4}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if fm.published != 2 {
		t.Fatalf("VersionPublished fired %d times, want 2", fm.published)
	}

	// Promote 1.0.0, then 2.0.0 → the second demotes the incumbent.
	for _, v := range []string{"1.0.0", "2.0.0"} {
		if _, err := svc.Transition(ctx, "me", "m", v, domain.StageStaging, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Transition(ctx, "me", "m", v, domain.StageProduction, ""); err != nil {
			t.Fatal(err)
		}
	}
	if fm.transitions != 4 {
		t.Fatalf("StageTransitioned fired %d, want 4", fm.transitions)
	}
	if fm.demotions != 1 {
		t.Fatalf("singleton demotions = %d, want 1", fm.demotions)
	}

	// Resolve twice: miss then hit; each mints a signed URL for the one artifact.
	if _, err := svc.Resolve(ctx, "m", domain.Selector{Version: "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Resolve(ctx, "m", domain.Selector{Version: "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	if fm.miss != 1 || fm.hit != 1 {
		t.Fatalf("resolve cache hits/misses = %d/%d, want 1/1", fm.hit, fm.miss)
	}
	if fm.signed != 2 {
		t.Fatalf("signed URLs minted = %d, want 2", fm.signed)
	}
}
