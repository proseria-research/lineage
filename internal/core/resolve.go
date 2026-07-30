package core

import (
	"context"
	"encoding/json"

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
	ResolvedAt  int64               `json:"resolvedAt"`
}

// Resolve returns the version matching sel plus signed artifact refs, using the
// resolve cache with event-driven invalidation (§04.2, §04.4).
func (s *Service) Resolve(ctx context.Context, model string, sel domain.Selector) (*Resolution, error) {
	key := model + "|" + cacheKey(sel)
	if cached, ok := s.cache.Get(key); ok {
		var r Resolution
		if json.Unmarshal(cached, &r) == nil {
			s.signRefs(ctx, &r) // signed URLs are minted per response, never cached (§04.4)
			return &r, nil
		}
	}
	v, err := s.store.Resolve(ctx, model, sel)
	if err != nil {
		return nil, err
	}
	arts, err := s.store.ListArtifacts(ctx, v.ID)
	if err != nil {
		return nil, err
	}
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
		})
	}
	// Cache the selection (without signed URLs), then sign for this response.
	if b, err := json.Marshal(r); err == nil {
		s.cache.Set(key, b, 60_000_000_000) // 60s backstop TTL
	}
	s.signRefs(ctx, r)
	return r, nil
}

// signRefs mints fresh signed URLs where the backend supports signing; otherwise the
// consumer uses storageUri or the stream-through fetch endpoint (§04.3).
func (s *Service) signRefs(ctx context.Context, r *Resolution) {
	b := s.backend("")
	if b == nil || !b.Capabilities().Signing {
		return
	}
	for i := range r.Artifacts {
		if url, err := b.SignGet(ctx, r.Artifacts[i].StorageURI, s.signTTL); err == nil {
			r.Artifacts[i].SignedURL = url
			r.Artifacts[i].SignedURLExpires = domain.NowMillis() + s.signTTL.Milliseconds()
		}
	}
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
