package domain

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrStorageUnsupported is returned by a StorageBackend for a capability it lacks
// (e.g. SignGet on the fs driver) so the API can fall back to stream-through (§05.2).
var ErrStorageUnsupported = errors.New("storage: operation not supported by backend")

// ListOptions carries pagination/filter/sort for collection reads (§03.3).
type ListOptions struct {
	PageSize  int
	PageToken string
	OrderBy   string            // e.g. "createdAt desc"
	Q         string            // substring match on name
	Filters   map[string]string // e.g. {"state":"ACTIVE"}, {"stage":"production"}
	Labels    map[string]string // label.<key>=<value>
}

// Selector resolves a version within a model (§04.2). Exactly one field is set;
// empty selector means the default serving stage (production).
type Selector struct {
	Stage      Stage
	Version    string
	LabelKey   string
	LabelValue string
}

// MetadataStore is the persistence port. Adapters (memory now; sqlite/postgres
// per-dialect, §02.7) implement it; the core never sees dialect (§01 dependency rule).
type MetadataStore interface {
	// Models
	CreateModel(ctx context.Context, m *Model) error
	GetModel(ctx context.Context, nameOrID string) (*Model, error)
	ListModels(ctx context.Context, o ListOptions) ([]*Model, string, error)
	UpdateModel(ctx context.Context, m *Model) error

	DeleteModel(ctx context.Context, id string) error

	// Versions
	CreateVersion(ctx context.Context, v *ModelVersion) error
	GetVersion(ctx context.Context, model, version string) (*ModelVersion, error)
	ListVersions(ctx context.Context, model string, o ListOptions) ([]*ModelVersion, string, error)
	UpdateVersion(ctx context.Context, v *ModelVersion) error
	DeleteVersion(ctx context.Context, id string) error
	// CountVersionsInStage counts a model's versions currently in a stage — used to guard
	// deletes against removing a live production version (§03.4).
	CountVersionsInStage(ctx context.Context, modelID string, stage Stage) (int, error)
	// SetStage moves versionID to `to`. If singleton is true and `to` is a singleton
	// stage, any *other* version of the same model currently in `to` is demoted to
	// archived within the same transaction (§02.4). Enforcing this inside the store keeps
	// the invariant correct under concurrency (Postgres FOR UPDATE; SQLite single-writer).
	SetStage(ctx context.Context, versionID string, to Stage, singleton bool) error
	// Resolve returns the version matching sel within model (§04.2).
	Resolve(ctx context.Context, model string, sel Selector) (*ModelVersion, error)

	// Artifacts
	CreateArtifact(ctx context.Context, a *Artifact) error
	GetArtifact(ctx context.Context, versionID, name string) (*Artifact, error)
	ListArtifacts(ctx context.Context, versionID string) ([]*Artifact, error)
	UpdateArtifact(ctx context.Context, a *Artifact) error
	DeleteArtifact(ctx context.Context, id string) error
	// ArtifactRefsURI reports whether any artifact row still points at uri. GC uses it to
	// reference-count backend objects before sweeping them (§05.8).
	ArtifactRefsURI(ctx context.Context, uri string) (bool, error)

	// Lineage
	AddLineageEdge(ctx context.Context, e *LineageEdge) error
	ListLineage(ctx context.Context, versionID string) ([]*LineageEdge, error)
	DeleteLineageEdge(ctx context.Context, id, versionID string) error

	// Deployments (§02.3.4)
	CreateDeployment(ctx context.Context, d *Deployment) error
	GetDeployment(ctx context.Context, id string) (*Deployment, error)
	ListDeployments(ctx context.Context, versionID string) ([]*Deployment, error)
	UpdateDeployment(ctx context.Context, d *Deployment) error
	DeleteDeployment(ctx context.Context, id string) error

	// Audit (append-only; read for the activity feed, §09)
	AppendAudit(ctx context.Context, e *AuditEvent) error
	ListAudit(ctx context.Context, subjectType, subjectID string, o ListOptions) ([]*AuditEvent, string, error)
}

// StorageCapabilities advertises what a backend can do so the API adapts (§05.2).
type StorageCapabilities struct {
	Signing   bool
	Ranges    bool
	Multipart bool
	OCI       bool
}

// SignedRequest describes a direct-to-storage upload target (§05.6).
type SignedRequest struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt int64             `json:"expiresAt"`
}

// ObjectInfo is the result of a Stat (§05.2).
type ObjectInfo struct {
	Exists    bool
	SizeBytes int64
	Digest    string
}

// ObjectRef identifies one stored object when enumerating a backend for GC (§05.8).
type ObjectRef struct {
	URI        string
	SizeBytes  int64
	ModifiedAt int64 // epoch ms
}

// MultipartPart is one part of a multipart upload: a presigned PUT URL on the way out
// (initiate) and the ETag the client observed on the way back (finalize) (§05.6).
type MultipartPart struct {
	PartNumber int    `json:"partNumber"`
	URL        string `json:"url,omitempty"`
	ETag       string `json:"etag,omitempty"`
}

// MultipartPlan is returned by InitiateMultipart: the backend's opaque upload id, the part
// size, and a presigned PUT target per part (§05.6).
type MultipartPlan struct {
	UploadID string          `json:"-"`
	PartSize int64           `json:"partSize"`
	Parts    []MultipartPart `json:"parts"`
}

// StorageBackend is the artifact-bytes port. Bytes flow directly between backend and
// consumer on the read path; the core only proxies when a backend can't sign (§05.1).
type StorageBackend interface {
	Name() string
	Capabilities() StorageCapabilities
	Stat(ctx context.Context, uri string) (ObjectInfo, error)
	SignGet(ctx context.Context, uri string, ttl time.Duration) (string, error)
	SignPut(ctx context.Context, path string, ttl time.Duration) (SignedRequest, error)
	Get(ctx context.Context, uri string) (io.ReadCloser, error)
	Put(ctx context.Context, path string, r io.Reader, size int64, contentType string) (string, error)
	// URIFor returns the canonical native uri for a stored path (e.g. file://…, s3://bucket/…).
	URIFor(path string) string
	Delete(ctx context.Context, uri string) error

	// --- Multipart upload (large files, §05.6). Backends without Capabilities().Multipart
	// return ErrStorageUnsupported and the core falls back to a single signed PUT. ---

	// InitiateMultipart starts a multipart upload for path and returns the backend upload id
	// plus a presigned PUT URL for each of `parts` parts (each `partSize` bytes, last shorter).
	InitiateMultipart(ctx context.Context, path string, parts int, partSize int64, ttl time.Duration) (MultipartPlan, error)
	// CompleteMultipart assembles the object from the client-observed per-part ETags (ordered)
	// and returns the canonical native uri.
	CompleteMultipart(ctx context.Context, path, uploadID string, parts []MultipartPart) (string, error)
	// AbortMultipart discards an incomplete multipart upload (finalize failure / expiry).
	AbortMultipart(ctx context.Context, path, uploadID string) error

	// ListObjects enumerates stored objects under prefix, for reference-counted GC (§05.8).
	ListObjects(ctx context.Context, prefix string) ([]ObjectRef, error)
}

// ResolutionCache caches (model, selector) → resolution, invalidated per-model on
// publish/stage change (§04.4). Signed URLs are never cached (minted per response).
type ResolutionCache interface {
	Get(key string) ([]byte, bool)
	Set(key string, val []byte, ttl time.Duration)
	InvalidateModel(model string)
}

// Event is a domain event published on mutations (§01, §03).
type Event struct {
	Type    string
	Model   string
	Version string
	Data    map[string]any
}

// EventBus fans domain events out to subscribers (webhooks, cache invalidation).
type EventBus interface {
	Publish(e Event)
	Subscribe(fn func(Event))
}
