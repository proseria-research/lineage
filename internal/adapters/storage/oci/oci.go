// Package oci is the OCI-registry StorageBackend (§05.3.1). It stores a model version as
// **one manifest per version, one layer per artifact** — so `oci://<registry>/<repo>:<version>`
// is a single pullable reference containing the whole model directory, which is what an
// OCI-native consumer (ORAS, KServe modelcars, Modal, Baseten) wants.
//
// Layers hold the artifact bytes verbatim, named by the ORAS `org.opencontainers.image.title`
// annotation. That keeps Lineage's central invariant intact: a layer's digest *is* the
// artifact's content digest (§05.5), so Stat is free and nothing has to be unpacked to verify
// integrity. It also means Lineage pushes an OCI **artifact**, not a runnable image — see
// §05.3.1 for what that does and does not give you.
//
// Reads offload where the registry lets them: a blob GET against a registry backed by object
// storage answers 307 to a presigned URL, which SignGet returns. Writes have no presignable
// target in the distribution spec, so uploads stream through the API (Capabilities.SignPut is
// false, §05.6).
package oci

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// Config configures the OCI backend (§05.4). Credentials come from the environment/secret
// and never appear in API responses.
type Config struct {
	Registry   string // host[:port], e.g. ghcr.io, harbor.internal:443, localhost:5000
	Repository string // repository prefix Lineage owns, e.g. "lineage/models"
	Username   string
	Password   string
	PlainHTTP  bool // http instead of https (in-cluster registries, dev)
}

type Backend struct {
	name string
	cfg  Config
	rc   *client

	// mu serializes the read-modify-write of a manifest, keyed by "repo:tag". Two artifacts
	// uploaded concurrently to one version each rewrite the same manifest, and a lost update
	// would silently drop a layer. This guards a single process — see §05.3.1 on replicas.
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New(name string, cfg Config) (*Backend, error) {
	if cfg.Registry == "" {
		return nil, domain.Invalid("oci backend: registry is required")
	}
	if strings.Contains(cfg.Registry, "/") {
		return nil, domain.Invalid("oci backend: registry must be a host[:port], not a path")
	}
	if cfg.Repository != "" && !domain.ValidOCIRepository(cfg.Repository) {
		return nil, domain.Invalid("oci backend: invalid repository prefix '" + cfg.Repository + "'")
	}
	return &Backend{
		name:  name,
		cfg:   cfg,
		rc:    newClient(cfg.Registry, cfg.PlainHTTP, cfg.Username, cfg.Password),
		locks: map[string]*sync.Mutex{},
	}, nil
}

var _ domain.StorageBackend = (*Backend)(nil)

func (b *Backend) Name() string { return b.name }

// Capabilities: reads can offload via the registry's blob redirect; writes cannot be
// presigned, and multipart/GC-listing do not exist in the distribution spec (§05.3.1).
func (b *Backend) Capabilities() domain.StorageCapabilities {
	return domain.StorageCapabilities{Signing: true, Ranges: true, OCI: true}
}

// ---- Addressing ----

// URIFor maps a storage path (<model>/<version>/<artifact>, §05.5) onto an oci:// ref:
// repository `<prefix>/<model>`, tag `<version>`, layer `<artifact>`.
func (b *Backend) URIFor(path string) string {
	model, version, artifact := splitPath(path)
	return domain.OCIRef{
		Registry:   b.cfg.Registry,
		Repository: b.repo(model),
		Tag:        domain.OCITag(version),
		Layer:      artifact,
	}.String()
}

// repo prefixes a model name into the repository namespace Lineage owns.
func (b *Backend) repo(model string) string {
	name := sanitizeRepoComponent(model)
	if b.cfg.Repository == "" {
		return name
	}
	return b.cfg.Repository + "/" + name
}

// splitPath breaks "<model>/<version>/<artifact>" apart. Artifact names never contain a
// slash (ValidArtifactName), so anything beyond the first two segments is the file name.
func splitPath(path string) (model, version, artifact string) {
	p := strings.TrimPrefix(path, "/")
	model, rest, _ := strings.Cut(p, "/")
	version, artifact, _ = strings.Cut(rest, "/")
	return model, version, artifact
}

// sanitizeRepoComponent coerces a model name into the distribution-spec name grammar. Model
// names are already lowercase slugs, so this only has to collapse the separator runs the
// spec forbids (`a..b`) and drop leading/trailing separators.
func sanitizeRepoComponent(s string) string {
	var b strings.Builder
	prevSep := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
			prevSep = false
		case c == '.' || c == '_' || c == '-':
			if prevSep {
				continue // no leading separator, no run
			}
			b.WriteByte(c)
			prevSep = true
		default:
			if !prevSep {
				b.WriteByte('-')
				prevSep = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "._-")
	if out == "" {
		return "model"
	}
	return out
}

// ---- Reads ----

// Stat resolves a ref to its size and content digest. With a `#layer` fragment that is the
// layer's own descriptor — i.e. the artifact's bytes. Without one it describes the whole
// image: the manifest digest and the summed layer sizes.
func (b *Backend) Stat(ctx context.Context, uri string) (domain.ObjectInfo, error) {
	ref, err := domain.ParseOCIURI(uri)
	if err != nil {
		return domain.ObjectInfo{}, err
	}
	m, manifestDigest, err := b.rc.getManifest(ctx, ref.Repository, ref.Reference())
	if err != nil {
		if err == errManifestNotFound {
			return domain.ObjectInfo{Exists: false}, nil
		}
		return domain.ObjectInfo{}, err
	}
	if ref.Layer == "" {
		var total int64
		for _, l := range m.Layers {
			total += l.Size
		}
		return domain.ObjectInfo{Exists: true, SizeBytes: total, Digest: manifestDigest}, nil
	}
	l, ok := m.layer(ref.Layer)
	if !ok {
		return domain.ObjectInfo{Exists: false}, nil
	}
	// The layer descriptor is the registry's own record of the bytes; trust it for size, and
	// report its digest as the content digest because that is exactly what it is.
	return domain.ObjectInfo{Exists: true, SizeBytes: l.Size, Digest: l.Digest}, nil
}

// SignGet returns the registry's redirect target for the blob — a presigned URL when the
// registry is backed by object storage. ttl is the registry's to choose, not ours. Registries
// that serve blobs inline yield ErrStorageUnsupported and the caller streams through (§05.7).
func (b *Backend) SignGet(ctx context.Context, uri string, _ time.Duration) (string, error) {
	ref, err := domain.ParseOCIURI(uri)
	if err != nil {
		return "", err
	}
	dig, err := b.blobDigest(ctx, ref)
	if err != nil {
		return "", err
	}
	return b.rc.blobRedirect(ctx, ref.Repository, dig)
}

// SignPut is unsupported: the distribution spec's upload session is authenticated per
// request, so there is no URL a credential-less client could PUT to (§05.3.1).
func (b *Backend) SignPut(context.Context, string, time.Duration) (domain.SignedRequest, error) {
	return domain.SignedRequest{}, domain.ErrStorageUnsupported
}

// Get streams the artifact's bytes. A ref with no layer fragment addresses an image rather
// than a file, and there is no single stream to hand back.
func (b *Backend) Get(ctx context.Context, uri string) (io.ReadCloser, error) {
	ref, err := domain.ParseOCIURI(uri)
	if err != nil {
		return nil, err
	}
	dig, err := b.blobDigest(ctx, ref)
	if err != nil {
		return nil, err
	}
	return b.rc.getBlob(ctx, ref.Repository, dig)
}

// blobDigest resolves a ref to the digest of the blob it names.
func (b *Backend) blobDigest(ctx context.Context, ref domain.OCIRef) (string, error) {
	if ref.Layer == "" {
		return "", domain.Invalid("oci: '" + ref.String() + "' addresses an image, not a single blob; " +
			"reference one layer with #<artifact>")
	}
	m, _, err := b.rc.getManifest(ctx, ref.Repository, ref.Reference())
	if err != nil {
		return "", err
	}
	l, ok := m.layer(ref.Layer)
	if !ok {
		return "", domain.NotFound("oci: no layer titled '" + ref.Layer + "' in " + ref.Image())
	}
	return l.Digest, nil
}

// ---- Writes ----

// Put pushes the bytes as a layer and folds it into the version's manifest, creating the
// manifest on first write and replacing a same-named layer on a rewrite. It returns the
// canonical oci:// ref for the artifact.
func (b *Backend) Put(ctx context.Context, path string, r io.Reader, _ int64, mediaType string) (string, error) {
	model, version, artifact := splitPath(path)
	if artifact == "" {
		return "", domain.Invalid("oci: storage path '" + path + "' names no artifact")
	}
	repo, tag := b.repo(model), domain.OCITag(version)

	// The blob push is outside the lock: it is the slow part, it is content-addressed, and
	// two pushes of the same bytes are indistinguishable to the registry.
	layer, err := b.rc.pushBlob(ctx, repo, r, mediaType)
	if err != nil {
		return "", err
	}
	layer.Annotations = map[string]string{titleAnnotation: artifact}

	unlock := b.lockTag(repo, tag)
	defer unlock()

	m, _, err := b.rc.getManifest(ctx, repo, tag)
	if err != nil {
		if err != errManifestNotFound {
			return "", err
		}
		cfg, cerr := b.rc.ensureEmptyConfig(ctx, repo)
		if cerr != nil {
			return "", cerr
		}
		m = &manifest{
			SchemaVersion: 2,
			MediaType:     mediaTypeManifest,
			ArtifactType:  artifactTypeModel,
			Config:        cfg,
			Annotations:   map[string]string{titleAnnotation: model + "@" + version},
		}
	}
	m.Layers = upsertLayer(m.Layers, layer)
	if _, err := b.rc.putManifest(ctx, repo, tag, m); err != nil {
		return "", err
	}
	return domain.OCIRef{Registry: b.cfg.Registry, Repository: repo, Tag: tag, Layer: artifact}.String(), nil
}

// Delete removes what the ref names: one layer from the version's manifest, or — for a ref
// with no fragment, or once the last layer is gone — the manifest itself. Blobs are left to
// the registry's own GC, which is the only thing that can tell whether another manifest
// still references them (§05.8).
func (b *Backend) Delete(ctx context.Context, uri string) error {
	ref, err := domain.ParseOCIURI(uri)
	if err != nil {
		return err
	}
	unlock := b.lockTag(ref.Repository, ref.Reference())
	defer unlock()

	m, digest, err := b.rc.getManifest(ctx, ref.Repository, ref.Reference())
	if err != nil {
		if err == errManifestNotFound {
			return nil // idempotent
		}
		return err
	}
	if ref.Layer == "" {
		return b.rc.deleteManifest(ctx, ref.Repository, digest)
	}
	kept := m.Layers[:0]
	for _, l := range m.Layers {
		if l.title() != ref.Layer {
			kept = append(kept, l)
		}
	}
	if len(kept) == len(m.Layers) {
		return nil // no such layer; nothing to do
	}
	if len(kept) == 0 {
		return b.rc.deleteManifest(ctx, ref.Repository, digest)
	}
	m.Layers = kept
	_, err = b.rc.putManifest(ctx, ref.Repository, ref.Reference(), m)
	return err
}

// lockTag serializes manifest read-modify-write per (repo, tag).
func (b *Backend) lockTag(repo, tag string) func() {
	key := repo + ":" + tag
	b.mu.Lock()
	l, ok := b.locks[key]
	if !ok {
		l = &sync.Mutex{}
		b.locks[key] = l
	}
	b.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// upsertLayer replaces a same-titled layer in place (so artifact order is stable across a
// rewrite) or appends a new one.
func upsertLayer(layers []descriptor, d descriptor) []descriptor {
	for i, l := range layers {
		if l.title() == d.title() {
			layers[i] = d
			return layers
		}
	}
	return append(layers, d)
}

// ---- Unsupported ----

// Multipart has no analogue in the distribution spec — a blob upload is a single session, and
// clients cannot hold one anyway without our credentials. Uploads stream through instead.
func (b *Backend) InitiateMultipart(context.Context, string, int, int64, time.Duration) (domain.MultipartPlan, error) {
	return domain.MultipartPlan{}, domain.ErrStorageUnsupported
}

func (b *Backend) CompleteMultipart(context.Context, string, string, []domain.MultipartPart) (string, error) {
	return "", domain.ErrStorageUnsupported
}

func (b *Backend) AbortMultipart(context.Context, string, string) error {
	return domain.ErrStorageUnsupported
}

// ListObjects is unsupported, so Lineage's reference-counted sweeper skips this backend
// (§05.8). Blob lifetime in a registry is decided by the registry's own GC over manifest
// reachability — Lineage cannot see the other manifests that may share a blob, and a sweeper
// that guessed would delete bytes another image still needs. Registry retention policies
// (Harbor, ECR lifecycle, zot) own this.
func (b *Backend) ListObjects(context.Context, string) ([]domain.ObjectRef, error) {
	return nil, domain.ErrStorageUnsupported
}
