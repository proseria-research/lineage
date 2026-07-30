// Package storetest is a shared conformance suite for any domain.MetadataStore, so the
// memory, SQLite, and Postgres adapters are held to identical behavior (§02.7 acceptance).
package storetest

import (
	"context"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// Run exercises the full happy path plus key invariants against store.
func Run(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	now := domain.NowMillis()

	// Create model + uniqueness.
	m := &domain.Model{ID: domain.NewID(), Name: "fraud-detector", State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if err := store.CreateModel(ctx, &domain.Model{ID: domain.NewID(), Name: "fraud-detector", State: domain.StateActive}); err == nil {
		t.Fatal("expected already_exists on duplicate model name")
	} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeAlreadyExists {
		t.Fatalf("expected already_exists, got %v", err)
	}

	// Get by name and by id.
	if got, err := store.GetModel(ctx, "fraud-detector"); err != nil || got.ID != m.ID {
		t.Fatalf("GetModel by name: %v", err)
	}
	if got, err := store.GetModel(ctx, m.ID); err != nil || got.Name != "fraud-detector" {
		t.Fatalf("GetModel by id: %v", err)
	}
	if _, err := store.GetModel(ctx, "nope"); err == nil {
		t.Fatal("expected not_found for unknown model")
	}

	// Two versions + an artifact.
	v14 := mkVersion(m.ID, "1.4.0")
	v15 := mkVersion(m.ID, "1.5.0")
	mustCreateVersion(t, store, v14)
	mustCreateVersion(t, store, v15)
	art := &domain.Artifact{ID: domain.NewID(), VersionID: v14.ID, Kind: domain.KindModel, Name: "model.onnx",
		URI: "s3://m/1.4.0", Digest: "sha256:aaa", ModelFormat: &domain.ModelFormat{Name: "onnx", Version: "1.16"},
		CreatedAt: now, UpdatedAt: now}
	if err := store.CreateArtifact(ctx, art); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if arts, err := store.ListArtifacts(ctx, v14.ID); err != nil || len(arts) != 1 || arts[0].ModelFormat == nil || arts[0].ModelFormat.Name != "onnx" {
		t.Fatalf("ListArtifacts round-trip: %v %+v", err, arts)
	}

	// Illegal transition rejected (draft -> production directly not allowed by core, but the
	// store performs the move it is told; the state machine lives in the domain/core). Here we
	// exercise the store-level promote + singleton demotion the core relies on.
	// Promote 1.4.0 to production (singleton).
	if err := store.SetStage(ctx, v14.ID, domain.StageProduction, true); err != nil {
		t.Fatalf("SetStage 1.4.0->production: %v", err)
	}
	if got, _ := store.Resolve(ctx, "fraud-detector", domain.Selector{Stage: domain.StageProduction}); got == nil || got.Name != "1.4.0" {
		t.Fatalf("resolve production expected 1.4.0, got %+v", got)
	}

	// Promote 1.5.0 to production -> 1.4.0 auto-demoted to archived (singleton invariant).
	if err := store.SetStage(ctx, v15.ID, domain.StageProduction, true); err != nil {
		t.Fatalf("SetStage 1.5.0->production: %v", err)
	}
	if got, err := store.GetVersion(ctx, "fraud-detector", "1.4.0"); err != nil || got.Stage != domain.StageArchived {
		t.Fatalf("expected 1.4.0 archived after singleton demotion, got %v (%v)", got.Stage, err)
	}
	if got, _ := store.Resolve(ctx, "fraud-detector", domain.Selector{Stage: domain.StageProduction}); got == nil || got.Name != "1.5.0" {
		t.Fatalf("resolve production expected 1.5.0, got %+v", got)
	}

	// Resolve by exact version and empty selector (defaults to production).
	if got, err := store.Resolve(ctx, "fraud-detector", domain.Selector{Version: "1.4.0"}); err != nil || got.Name != "1.4.0" {
		t.Fatalf("resolve by version: %v", err)
	}
	if got, err := store.Resolve(ctx, "fraud-detector", domain.Selector{}); err != nil || got.Name != "1.5.0" {
		t.Fatalf("resolve default selector: %v %+v", err, got)
	}

	// Lists.
	if ms, _, err := store.ListModels(ctx, domain.ListOptions{}); err != nil || len(ms) != 1 {
		t.Fatalf("ListModels: %v len=%d", err, len(ms))
	}
	if vs, _, err := store.ListVersions(ctx, "fraud-detector", domain.ListOptions{}); err != nil || len(vs) != 2 {
		t.Fatalf("ListVersions: %v len=%d", err, len(vs))
	}
	if vs, _, err := store.ListVersions(ctx, "fraud-detector", domain.ListOptions{Filters: map[string]string{"stage": "production"}}); err != nil || len(vs) != 1 {
		t.Fatalf("ListVersions filtered by stage: %v len=%d", err, len(vs))
	}

	// Lineage + audit round-trip.
	edge := &domain.LineageEdge{ID: domain.NewID(), SrcType: "model_version", SrcID: v15.ID,
		Relation: domain.RelDerivedFrom, DstRef: "hf://bert-base", CreatedAt: now}
	if err := store.AddLineageEdge(ctx, edge); err != nil {
		t.Fatalf("AddLineageEdge: %v", err)
	}
	if es, err := store.ListLineage(ctx, v15.ID); err != nil || len(es) != 1 || es[0].Relation != domain.RelDerivedFrom {
		t.Fatalf("ListLineage: %v %+v", err, es)
	}
	if err := store.AppendAudit(ctx, &domain.AuditEvent{ID: domain.NewID(), At: now, Action: "version.create", SubjectType: "model_version", SubjectID: v15.ID}); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
}

func mkVersion(modelID, name string) *domain.ModelVersion {
	now := domain.NowMillis()
	return &domain.ModelVersion{ID: domain.NewID(), ModelID: modelID, Name: name, Stage: domain.StageDraft, CreatedAt: now, UpdatedAt: now}
}

func mustCreateVersion(t *testing.T, store domain.MetadataStore, v *domain.ModelVersion) {
	t.Helper()
	if err := store.CreateVersion(context.Background(), v); err != nil {
		t.Fatalf("CreateVersion %s: %v", v.Name, err)
	}
}
