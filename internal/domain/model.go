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

// nameRe is the path-safe, exact-match name pattern for models and versions (§03.1). These
// are URL identities, so they stay lowercase slugs — case-folding collisions across stores
// and filesystems are not worth the ergonomics.
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,61}[a-z0-9])?$`)

// artifactNameRe is the filename pattern for artifacts. Artifacts are *files*, and real model
// repos ship `README.md`, `MODEL_CARD.md` and `.gitattributes`, so mixed case and a leading
// dot are allowed where model/version names forbid them. The charset stays path-safe: no
// separators, no whitespace, no control or shell characters. Traversal is rejected separately
// because `..` matches this pattern (§05.6).
var artifactNameRe = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9._-]{0,254}$`)

// ValidName reports whether s is a legal model or version name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// ValidArtifactName reports whether s is a legal artifact (file) name. It is deliberately
// more permissive than ValidName, but must never admit a name that escapes the directory it
// is joined into — artifact names become storage keys server-side and local paths in the SDK,
// CLI and KServe initializer.
func ValidArtifactName(s string) bool {
	if s == "." || s == ".." {
		return false
	}
	return artifactNameRe.MatchString(s)
}

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
	// LegalHold is the subject's own hold, nil when not held (§19.3). It rides on the entity
	// so every GET already carries it and the console needs no second call — and it is set
	// only through RetentionStore.SetHold, never by PATCH.
	LegalHold *Hold `json:"legalHold,omitempty"`
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
	// StageChangedAt is when the version entered its current stage (§20.8.3), set by the
	// store on create and on every stage move, demotions included. It is what §20.7 clause 3
	// measures monitoring against; UpdatedAt cannot serve, because a description edit moves it.
	StageChangedAt int64 `json:"stageChangedAt,omitempty"`
	// LegalHold is this version's own hold, nil when not held (§19.3). A version under a held
	// model is *not* marked here — inheritance is resolved at the delete guard, not copied
	// onto rows, so releasing the model does not leave stale marks behind on its versions.
	LegalHold *Hold `json:"legalHold,omitempty"`
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
	// Epoch is the sealing window this row belongs to (§19.6), derived at write from the
	// row's own At. **Nil means the row is not attested** — it predates §19.5, or was written
	// while attestation was disabled. Nil rather than 0 on purpose: 0 is a real epoch (the
	// first minute of 1970), and a row that was never covered must not look sealed.
	//
	// Once written it is never recomputed. Changing sealIntervalSeconds later therefore
	// cannot re-group rows that are already sealed.
	Epoch *int64 `json:"epoch,omitempty"`
}
