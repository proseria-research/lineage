// Package domain holds Lineage's first-class entities, the port interfaces the core
// depends on, and the business enums/rules (§02 data model, §01 dependency rule).
package domain

import (
	"encoding/json"
	"regexp"
	"time"
)

// NowMillis is the canonical timestamp: epoch-millis, set by the app (§02.1).
func NowMillis() int64 { return time.Now().UnixMilli() }

// nameRe is the path-safe, exact-match name pattern for models/versions/artifacts (§03.1).
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,61}[a-z0-9])?$`)

// ValidName reports whether s is a legal entity name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// ---- Enums (stored as TEXT + CHECK, §02.7) ----

type ModelState string

const (
	StateActive   ModelState = "ACTIVE"
	StateArchived ModelState = "ARCHIVED"
)

type Stage string

const (
	StageDraft      Stage = "draft"
	StageStaging    Stage = "staging"
	StageProduction Stage = "production"
	StageArchived   Stage = "archived"
)

type ArtifactKind string

const (
	KindModel ArtifactKind = "MODEL"
	KindDoc   ArtifactKind = "DOC"
)

type LineageRelation string

const (
	RelDerivedFrom LineageRelation = "derived_from"
	RelTrainedOn   LineageRelation = "trained_on"
	RelProducedBy  LineageRelation = "produced_by"
	RelDeployedAs  LineageRelation = "deployed_as"
)

type DeploymentStatus string

const (
	DeployActive   DeploymentStatus = "ACTIVE"
	DeployInactive DeploymentStatus = "INACTIVE"
)

// ---- Entities (§02.3) ----

type Model struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Owner            string            `json:"owner,omitempty"`
	State            ModelState        `json:"state"`
	Labels           map[string]string `json:"labels,omitempty"`
	CustomProperties json.RawMessage   `json:"customProperties,omitempty"`
	CreatedAt        int64             `json:"createdAt"`
	UpdatedAt        int64             `json:"updatedAt"`
}

type ModelVersion struct {
	ID               string            `json:"id"`
	ModelID          string            `json:"modelId"`
	Model            string            `json:"model"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Author           string            `json:"author,omitempty"`
	Stage            Stage             `json:"stage"`
	Labels           map[string]string `json:"labels,omitempty"`
	CustomProperties json.RawMessage   `json:"customProperties,omitempty"`
	CreatedAt        int64             `json:"createdAt"`
	UpdatedAt        int64             `json:"updatedAt"`
}

type ModelFormat struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type Artifact struct {
	ID               string            `json:"id"`
	VersionID        string            `json:"versionId"`
	Kind             ArtifactKind      `json:"kind"`
	Name             string            `json:"name"`
	URI              string            `json:"uri"`
	StorageBackend   string            `json:"storageBackend,omitempty"`
	StoragePath      string            `json:"storagePath,omitempty"`
	SizeBytes        int64             `json:"sizeBytes,omitempty"`
	Digest           string            `json:"digest,omitempty"`
	MediaType        string            `json:"mediaType,omitempty"`
	ModelFormat      *ModelFormat      `json:"modelFormat,omitempty"`
	ServiceAccount   string            `json:"serviceAccount,omitempty"`
	CustomProperties json.RawMessage   `json:"customProperties,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	CreatedAt        int64             `json:"createdAt"`
	UpdatedAt        int64             `json:"updatedAt"`
}

type LineageEdge struct {
	ID       string          `json:"id"`
	SrcType  string          `json:"srcType"`
	SrcID    string          `json:"srcId"`
	Relation LineageRelation `json:"relation"`
	DstType  string          `json:"dstType,omitempty"`
	DstID    string          `json:"dstId,omitempty"`
	DstRef   string          `json:"dstRef,omitempty"`
	// Properties carries how the relation came about, e.g.
	// {"method":"quantize","from_dtype":"fp16"} on a derived_from edge (§11.3.6). Keeping
	// it here lets a diff verdict be corroborated by declared intent without extending the
	// relation enum.
	Properties json.RawMessage `json:"properties,omitempty"`
	CreatedAt  int64           `json:"createdAt"`
}

type Deployment struct {
	ID          string           `json:"id"`
	VersionID   string           `json:"versionId"`
	Environment string           `json:"environment"`
	EndpointURI string           `json:"endpointUri,omitempty"`
	Status      DeploymentStatus `json:"status"`
	ExternalRef string           `json:"externalRef,omitempty"`
	CreatedAt   int64            `json:"createdAt"`
	UpdatedAt   int64            `json:"updatedAt"`
}

// AuditEvent is an append-only record of every mutation (§02.3.7, §02.5 invariant 4).
type AuditEvent struct {
	ID          string          `json:"id"`
	At          int64           `json:"at"`
	Actor       string          `json:"actor,omitempty"`
	Action      string          `json:"action"`
	SubjectType string          `json:"subjectType"`
	SubjectID   string          `json:"subjectId"`
	Summary     string          `json:"summary"`
	Data        json.RawMessage `json:"data,omitempty"`
}
