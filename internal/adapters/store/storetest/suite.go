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

	// Fetch a version by id (lineage-graph node labeling).
	if got, err := store.GetVersionByID(ctx, v14.ID); err != nil || got.Name != "1.4.0" || got.Model != "fraud-detector" {
		t.Fatalf("GetVersionByID: %v %+v", err, got)
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
	if err := store.AppendAudit(ctx, &domain.AuditEvent{ID: domain.NewID(), At: now, Actor: "ci", Action: "version.create", SubjectType: "model", SubjectID: m.ID}); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}

	// Audit feed filter by subject.
	if es, _, err := store.ListAudit(ctx, "model", m.ID, domain.ListOptions{}); err != nil || len(es) != 1 {
		t.Fatalf("ListAudit by subject: %v len=%d", err, len(es))
	}

	// Artifact get / update (metadata) / uri reference / delete.
	if got, err := store.GetArtifact(ctx, v14.ID, "model.onnx"); err != nil || got.Digest != "sha256:aaa" {
		t.Fatalf("GetArtifact: %v %+v", err, got)
	}
	if ref, err := store.ArtifactRefsURI(ctx, "s3://m/1.4.0"); err != nil || !ref {
		t.Fatalf("ArtifactRefsURI should be true: %v %v", ref, err)
	}
	art.MediaType = "application/octet-stream"
	if err := store.UpdateArtifact(ctx, art); err != nil {
		t.Fatalf("UpdateArtifact: %v", err)
	}
	if got, _ := store.GetArtifact(ctx, v14.ID, "model.onnx"); got.MediaType != "application/octet-stream" {
		t.Fatalf("UpdateArtifact not persisted: %+v", got)
	}

	// Deployment CRUD.
	dep := &domain.Deployment{ID: domain.NewID(), VersionID: v15.ID, Environment: "prod", Status: domain.DeployActive, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateDeployment(ctx, dep); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if ds, err := store.ListDeployments(ctx, v15.ID); err != nil || len(ds) != 1 {
		t.Fatalf("ListDeployments: %v len=%d", err, len(ds))
	}
	dep.Status = domain.DeployInactive
	if err := store.UpdateDeployment(ctx, dep); err != nil {
		t.Fatalf("UpdateDeployment: %v", err)
	}
	if got, err := store.GetDeployment(ctx, dep.ID); err != nil || got.Status != domain.DeployInactive {
		t.Fatalf("GetDeployment after update: %v %+v", err, got)
	}
	if err := store.DeleteDeployment(ctx, dep.ID); err != nil {
		t.Fatalf("DeleteDeployment: %v", err)
	}

	// Delete lineage edge (scoped to the version).
	if err := store.DeleteLineageEdge(ctx, edge.ID, v15.ID); err != nil {
		t.Fatalf("DeleteLineageEdge: %v", err)
	}
	if es, _ := store.ListLineage(ctx, v15.ID); len(es) != 0 {
		t.Fatalf("lineage edge not deleted: %+v", es)
	}

	// Production guard count.
	if n, err := store.CountVersionsInStage(ctx, m.ID, domain.StageProduction); err != nil || n != 1 {
		t.Fatalf("CountVersionsInStage production: %v n=%d", err, n)
	}

	runInsights(t, store, v15.ID)

	// Risk classifications (§16.7.1). Asserted through a type switch because the SQL
	// adapters gain ComplianceStore in a later step; once all three have it, the port is
	// embedded in MetadataStore and this guard goes away.
	if cs, ok := store.(domain.ComplianceStore); ok {
		other := &domain.Model{ID: domain.NewID(), Name: "churn-predictor", State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateModel(ctx, other); err != nil {
			t.Fatalf("CreateModel for classification isolation: %v", err)
		}
		runClassifications(t, cs, m.ID, other.ID)
	} else {
		t.Log("adapter does not implement domain.ComplianceStore yet; skipping §16 classification cases")
	}

	// Delete a draft version, then the model (cascade).
	v16 := mkVersion(m.ID, "1.6.0")
	mustCreateVersion(t, store, v16)
	if err := store.DeleteVersion(ctx, v16.ID); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}
	if _, err := store.GetVersion(ctx, "fraud-detector", "1.6.0"); err == nil {
		t.Fatal("expected not_found after DeleteVersion")
	}
	if err := store.DeleteModel(ctx, m.ID); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if _, err := store.GetModel(ctx, "fraud-detector"); err == nil {
		t.Fatal("expected not_found after DeleteModel")
	}
	// Cascade: the artifact of a deleted model's version must be gone.
	if ref, _ := store.ArtifactRefsURI(ctx, "s3://m/1.4.0"); ref {
		t.Fatal("artifact should be cascade-deleted with its model")
	}
	// Cascade: so must its classifications (§16.7.1 FK ON DELETE CASCADE).
	if cs, ok := store.(domain.ComplianceStore); ok {
		if list, err := cs.ListClassifications(ctx, m.ID); err != nil || len(list) != 0 {
			t.Fatalf("classifications should be cascade-deleted with their model: %v len=%d", err, len(list))
		}
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
