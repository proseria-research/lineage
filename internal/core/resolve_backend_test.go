package core_test

import (
	"context"
	"testing"
	"time"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// namedBackend is a fake backend with its own name, a switchable signing capability, and a
// signed URL that names the backend — so a test can tell which backend signed what.
type namedBackend struct {
	*fakeSigningBackend
	name  string
	signs bool
	asked []string // URIs this backend was asked to sign
}

func newNamedBackend(name string, signs bool) *namedBackend {
	return &namedBackend{fakeSigningBackend: newFakeBackend(), name: name, signs: signs}
}

func (n *namedBackend) Name() string { return n.name }
func (n *namedBackend) Capabilities() domain.StorageCapabilities {
	c := n.fakeSigningBackend.Capabilities()
	c.Signing, c.SignPut, c.Multipart = n.signs, n.signs, n.signs
	return c
}
func (n *namedBackend) SignGet(_ context.Context, uri string, _ time.Duration) (string, error) {
	n.asked = append(n.asked, uri)
	return "https://" + n.name + ".signed/" + uri, nil
}

// resolveTwoBackends publishes a version with one artifact on each named backend
// plus one that names no backend (so lives on the default), and returns the service.
func resolveTwoBackends(t *testing.T, def, other *namedBackend, fm *fakeMeter) *core.Service {
	t.Helper()
	ctx := context.Background()
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{def.Name(): def, other.Name(): other}, def.Name(),
		memcache.New(), events.New(), core.WithMeter(fm))
	if _, err := svc.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0", Artifacts: []core.ArtifactInput{
		{Kind: domain.KindModel, Name: "on-default", URI: "u://def", StorageBackend: def.Name(), Digest: "sha256:1", SizeBytes: 1},
		{Kind: domain.KindDoc, Name: "on-other", URI: "u://other", StorageBackend: other.Name(), Digest: "sha256:2", SizeBytes: 1},
		{Kind: domain.KindDoc, Name: "unnamed", URI: "u://unnamed", Digest: "sha256:3", SizeBytes: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// checkSigned resolves twice (miss, then cache hit) and checks each artifact's signed URL.
func checkSigned(t *testing.T, svc *core.Service, fm *fakeMeter, want map[string]string) {
	t.Helper()
	for pass, cache := range []string{"miss", "hit"} {
		r, err := svc.Resolve(context.Background(), "m", domain.Selector{Version: "1.0.0"})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range r.Artifacts {
			if a.SignedURL != want[a.Name] {
				t.Errorf("%s: %s signedUrl = %q, want %q", cache, a.Name, a.SignedURL, want[a.Name])
			}
			if (a.SignedURL != "") != (a.SignedURLExpires != 0) {
				t.Errorf("%s: %s signedUrl/expiry disagree: %q %d", cache, a.Name, a.SignedURL, a.SignedURLExpires)
			}
		}
		minted := 0
		for _, u := range want {
			if u != "" {
				minted++
			}
		}
		if fm.signed != minted*(pass+1) {
			t.Errorf("%s: SignedURLMinted = %d, want %d", cache, fm.signed, minted*(pass+1))
		}
	}
	if fm.miss != 1 || fm.hit != 1 {
		t.Errorf("resolve cache miss/hit = %d/%d, want 1/1", fm.miss, fm.hit)
	}
}

// Only the non-default backend signs: its artifact gets its URL; artifacts on the default
// (named or implied) fall back to storageUri/stream-through with no signedUrl.
func TestResolveSignsWithArtifactBackendNonDefault(t *testing.T) {
	def, other := newNamedBackend("fs", false), newNamedBackend("s3", true)
	fm := &fakeMeter{}
	svc := resolveTwoBackends(t, def, other, fm)
	checkSigned(t, svc, fm, map[string]string{
		"on-default": "", "unnamed": "", "on-other": "https://s3.signed/u://other",
	})
	for _, u := range other.asked {
		if u != "u://other" {
			t.Errorf("s3 backend asked to sign foreign artifact %q", u)
		}
	}
}

// The reverse: only the default signs. Its artifacts (named or implied) get its URL; the
// non-default artifact is never handed to the default backend to sign.
func TestResolveSignsWithArtifactBackendDefault(t *testing.T) {
	def, other := newNamedBackend("s3", true), newNamedBackend("fs", false)
	fm := &fakeMeter{}
	svc := resolveTwoBackends(t, def, other, fm)
	checkSigned(t, svc, fm, map[string]string{
		"on-default": "https://s3.signed/u://def", "unnamed": "https://s3.signed/u://unnamed", "on-other": "",
	})
	for _, u := range def.asked {
		if u == "u://other" {
			t.Errorf("default backend asked to sign the non-default artifact")
		}
	}
}
