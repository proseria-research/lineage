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

	// Versions
	CreateVersion(ctx context.Context, v *ModelVersion) error
	GetVersion(ctx context.Context, model, version string) (*ModelVersion, error)
	ListVersions(ctx context.Context, model string, o ListOptions) ([]*ModelVersion, string, error)
	UpdateVersion(ctx context.Context, v *ModelVersion) error
	// SetStage moves versionID to `to`. If singleton is true and `to` is a singleton
	// stage, any *other* version of the same model currently in `to` is demoted to
	// archived within the same transaction (§02.4). Enforcing this inside the store keeps
	// the invariant correct under concurrency (Postgres FOR UPDATE; SQLite single-writer).
	SetStage(ctx context.Context, versionID string, to Stage, singleton bool) error
	// Resolve returns the version matching sel within model (§04.2).
	Resolve(ctx context.Context, model string, sel Selector) (*ModelVersion, error)

	// Artifacts
	CreateArtifact(ctx context.Context, a *Artifact) error
	ListArtifacts(ctx context.Context, versionID string) ([]*Artifact, error)

	// Lineage & audit
	AddLineageEdge(ctx context.Context, e *LineageEdge) error
	ListLineage(ctx context.Context, versionID string) ([]*LineageEdge, error)
	AppendAudit(ctx context.Context, e *AuditEvent) error
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

// StorageBackend is the artifact-bytes port. Bytes flow directly between backend and
// consumer on the read path; the core only proxies when a backend can't sign (§05.1).
type StorageBackend interface {
	Name() string
	Capabilities() StorageCapabilities
	Stat(ctx context.Context, uri string) (ObjectInfo, error)
	SignGet(ctx context.Context, uri string, ttl time.Duration) (string, error)
	SignPut(ctx context.Context, path string, ttl time.Duration) (SignedRequest, error)
	Get(ctx context.Context, uri string) (io.ReadCloser, error)
	Delete(ctx context.Context, uri string) error
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
