package core

import (
	"context"
	"encoding/json"
	"io"

	"github.com/proseria-research/lineage/internal/domain"
)

// ResolvedArtifact is one artifact ref in a resolution response (§04.2).
type ResolvedArtifact struct {
	Name             string              `json:"name"`
	Kind             domain.ArtifactKind `json:"kind"`
	StorageURI       string              `json:"storageUri"`
	SignedURL        string              `json:"signedUrl,omitempty"`
	SignedURLExpires int64               `json:"signedUrlExpiresAt,omitempty"`
	SizeBytes        int64               `json:"sizeBytes,omitempty"`
	Digest           string              `json:"digest,omitempty"`
	MediaType        string              `json:"mediaType,omitempty"`
	ServiceAccount   string              `json:"serviceAccount,omitempty"`

	// backend names the storage backend the artifact lives on, so it is signed by that
	// backend rather than the default. Internal: not part of the wire shape (§04.2).
	backend string
}

// cachedResolution is the resolve-cache entry: the unsigned resolution plus each artifact's
// backend name, which the wire shape does not carry but signing on a hit needs.
type cachedResolution struct {
	R        *Resolution `json:"r"`
	Backends []string    `json:"b"`
}

// ResolvedInsight is the compact composition block a scheduler needs to pick a device
// class in one call (§11.6.3). It is opt-in so the hot cached path stays small.
type ResolvedInsight struct {
	ParamCount *int64       `json:"paramCount,omitempty"`
	Dtype      domain.Dtype `json:"dtype,omitempty"`
	DiskBytes  *int64       `json:"diskBytes,omitempty"`
	// MinDeviceMemoryBytes is the smallest recorded scenario total — the least memory the
	// model has been reported to run in. The scenario is named alongside it, because a
	// footprint without its basis is not actionable (§11.3.3).
	MinDeviceMemoryBytes    *int64            `json:"minDeviceMemoryBytes,omitempty"`
	MinDeviceMemoryScenario string            `json:"minDeviceMemoryScenario,omitempty"`
	Source                  domain.FactSource `json:"source,omitempty"`
}

// Resolution is the one-call answer for consumers (§04.2).
type Resolution struct {
	Model       string              `json:"model"`
	VersionID   string              `json:"versionId"`
	Version     string              `json:"version"`
	Stage       domain.Stage        `json:"stage"`
	Digest      string              `json:"digest,omitempty"`
	ModelFormat *domain.ModelFormat `json:"modelFormat,omitempty"`
	Artifacts   []ResolvedArtifact  `json:"artifacts"`
	// OCIImage is the pullable image reference when this version's MODEL artifacts live in an
	// OCI registry — the version's artifacts share one manifest, so one ref covers the whole
	// model directory. It is what goes in `InferenceService.spec.predictor.model.storageUri`
	// for a modelcars/OCI pull (§04.6, §05.3.1). Omitted for blob backends.
	OCIImage   string           `json:"ociImage,omitempty"`
	Insight    *ResolvedInsight `json:"insight,omitempty"`
	ResolvedAt int64            `json:"resolvedAt"`
}

// ResolveOption toggles optional blocks on a resolution.
type ResolveOption func(*resolveOpts)

type resolveOpts struct{ insight bool }

// WithInsight includes the compact insight block (§11.6.3).
func WithInsight() ResolveOption { return func(o *resolveOpts) { o.insight = true } }

// Resolve returns the version matching sel plus signed artifact refs, using the
// resolve cache with event-driven invalidation (§04.2, §04.4).
func (s *Service) Resolve(ctx context.Context, model string, sel domain.Selector, opts ...ResolveOption) (_ *Resolution, err error) {
	var ro resolveOpts
	for _, o := range opts {
		o(&ro)
	}
	ctx, sp := s.tracer.Start(ctx, "core.Resolve")
	// Named err + one defer: every return path reports its own outcome, so a span can never
	// be silently marked OK because a new early return forgot to record.
	defer func() { sp.RecordError(err); sp.End() }()
	sp.SetString("lineage.model", model)

	// The included blocks are part of the cached shape, so they belong in the key.
	key := model + "|" + cacheKey(sel)
	if ro.insight {
		key += "|+insight"
	}
	if cached, ok := s.cache.Get(key); ok {
		// An entry that does not decode to the current shape (e.g. written by an older
		// replica into a shared cache) is treated as a miss and overwritten below.
		var c cachedResolution
		if json.Unmarshal(cached, &c) == nil && c.R != nil && len(c.Backends) == len(c.R.Artifacts) {
			r := c.R
			for i := range r.Artifacts {
				r.Artifacts[i].backend = c.Backends[i]
			}
			s.meter.ResolveServed(true)
			// The cache decision is the single most useful attribute on this span: it explains
			// the latency difference between two otherwise identical resolves (§04.4).
			sp.SetString("lineage.cache", "hit")
			s.signRefs(ctx, r) // signed URLs are minted per response, never cached (§04.4)
			return r, nil
		}
	}
	sp.SetString("lineage.cache", "miss")
	s.meter.ResolveServed(false)
	v, err := s.store.Resolve(ctx, model, sel)
	if err != nil {
		return nil, err
	}
	arts, err := s.store.ListArtifacts(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	sp.SetString("lineage.version", v.Name)
	r := &Resolution{
		Model: model, VersionID: v.ID, Version: v.Name, Stage: v.Stage,
		Artifacts: make([]ResolvedArtifact, 0, len(arts)), ResolvedAt: domain.NowMillis(),
	}
	for _, a := range arts {
		if a.Kind == domain.KindModel && r.Digest == "" {
			r.Digest, r.ModelFormat = a.Digest, a.ModelFormat
		}
		r.Artifacts = append(r.Artifacts, ResolvedArtifact{
			Name: a.Name, Kind: a.Kind, StorageURI: a.URI, SizeBytes: a.SizeBytes,
			Digest: a.Digest, MediaType: a.MediaType, ServiceAccount: a.ServiceAccount,
			backend: a.StorageBackend,
		})
	}
	r.OCIImage = ociImage(arts)
	if ro.insight {
		r.Insight = s.compactInsight(ctx, v.ID)
	}
	// Cache the selection (without signed URLs), then sign for this response.
	c := cachedResolution{R: r, Backends: make([]string, len(r.Artifacts))}
	for i, a := range r.Artifacts {
		c.Backends[i] = a.backend
	}
	if b, err := json.Marshal(c); err == nil {
		s.cache.Set(key, b, 60_000_000_000) // 60s backstop TTL
	}
	s.signRefs(ctx, r)
	return r, nil
}

// ociImage returns the shared image reference for a version's MODEL artifacts when they all
// live in one OCI manifest (§05.3.1). It stays empty when the artifacts are not OCI, or when
// they are spread across more than one image — a single storageUri would then be a lie about
// what a consumer gets by pulling it.
func ociImage(arts []*domain.Artifact) string {
	var image string
	for _, a := range arts {
		if a.Kind != domain.KindModel {
			continue
		}
		ref, err := domain.ParseOCIURI(a.URI)
		if err != nil {
			return ""
		}
		if image != "" && image != ref.Image() {
			return ""
		}
		image = ref.Image()
	}
	return image
}

// compactInsight assembles the resolve-time block from stored facts. A version nobody has
// reported on simply has no block — the field is omitted rather than zero-filled (§11.2).
func (s *Service) compactInsight(ctx context.Context, versionID string) *ResolvedInsight {
	in, err := s.store.GetInsight(ctx, versionID)
	if err != nil {
		return nil
	}
	ri := &ResolvedInsight{
		ParamCount: in.ParamCountTotal, Dtype: in.DtypeDominant,
		DiskBytes: in.DiskBytes, Source: in.Source,
	}
	if fps, err := s.store.ListFootprints(ctx, versionID); err == nil {
		for _, f := range fps {
			if f.TotalBytes == nil {
				continue
			}
			if ri.MinDeviceMemoryBytes == nil || *f.TotalBytes < *ri.MinDeviceMemoryBytes {
				ri.MinDeviceMemoryBytes, ri.MinDeviceMemoryScenario = f.TotalBytes, f.Scenario
			}
		}
	}
	return ri
}

// signRefs mints fresh signed URLs, each by the backend its artifact lives on (as
// FetchArtifact selects it), where that backend supports signing; otherwise the consumer
// uses storageUri or the stream-through fetch endpoint (§04.3).
func (s *Service) signRefs(ctx context.Context, r *Resolution) {
	for i := range r.Artifacts {
		b := s.backend(r.Artifacts[i].backend)
		if b == nil || !b.Capabilities().Signing {
			continue
		}
		if url, err := b.SignGet(ctx, r.Artifacts[i].StorageURI, s.signTTL); err == nil {
			r.Artifacts[i].SignedURL = url
			r.Artifacts[i].SignedURLExpires = domain.NowMillis() + s.signTTL.Milliseconds()
			s.meter.SignedURLMinted()
		}
	}
}

// Fetch is the result of FetchArtifact (§04.3): a delivery plan for one artifact's bytes.
// Exactly one of SignedURL / Stream is populated. The caller closes Stream.
type Fetch struct {
	Artifact  *domain.Artifact
	SignedURL string        // set when the backend can sign (default: 302 redirect)
	Stream    io.ReadCloser // set for stream-through (backend can't sign, or ?mode=stream)
}

// FetchArtifact locates a named artifact within a version and prepares byte delivery (§04.3):
// a fresh signed URL to redirect to, or a stream-through reader when the backend can't sign
// (or the caller forced streaming). It never proxies bytes when a signed URL will do.
func (s *Service) FetchArtifact(ctx context.Context, model, version, artifact string, forceStream bool) (*Fetch, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	arts, err := s.store.ListArtifacts(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	var a *domain.Artifact
	for _, cand := range arts {
		if cand.Name == artifact {
			a = cand
			break
		}
	}
	if a == nil {
		return nil, domain.NotFound("artifact '" + artifact + "' not found on " + model + "@" + version)
	}
	b := s.backend(a.StorageBackend)
	if b == nil {
		return nil, domain.Internal("storage backend '" + a.StorageBackend + "' unavailable")
	}
	if !forceStream && b.Capabilities().Signing {
		url, err := b.SignGet(ctx, a.URI, s.signTTL)
		if err == nil {
			return &Fetch{Artifact: a, SignedURL: url}, nil
		}
		// Fall through to stream-through if signing unexpectedly fails.
	}
	rc, err := b.Get(ctx, a.URI)
	if err != nil {
		return nil, err
	}
	return &Fetch{Artifact: a, Stream: rc}, nil
}

func cacheKey(sel domain.Selector) string {
	switch {
	case sel.Version != "":
		return "v=" + sel.Version
	case sel.LabelKey != "":
		return "l=" + sel.LabelKey + "=" + sel.LabelValue
	case sel.Stage != "":
		return "s=" + string(sel.Stage)
	default:
		return "s=production"
	}
}
