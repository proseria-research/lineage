package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// lockedFixture publishes m@1 with one artifact and a stream-through upload initiated while
// it was still a draft, then promotes it to staging — which locks its artifact set.
func lockedFixture(t *testing.T) (*core.Service, context.Context, string) {
	t.Helper()
	ctx := context.Background()
	s := newSvc(t)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1",
		Artifacts: []core.ArtifactInput{{Name: "weights", URI: "s3://b/w"}}}); err != nil {
		t.Fatal(err)
	}
	tk, err := s.InitiateUpload(ctx, "me", "m", "1", core.InitiateUploadInput{Name: "late.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetVersion(ctx, "m", "1"); v.LockedAt != 0 {
		t.Fatalf("a draft is locked: %d", v.LockedAt)
	}
	if _, err := s.Transition(ctx, "me", "m", "1", domain.StageStaging, ""); err != nil {
		t.Fatal(err)
	}
	return s, ctx, tk.UploadID
}

func wantLocked(t *testing.T, what string, err error) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != domain.CodeFailedPrecondition || de.Details["reason"] != domain.RefusedVersionLocked {
		t.Fatalf("%s: got %v, want failed_precondition/version_locked", what, err)
	}
	if !strings.Contains(de.Message, "publish a new version") {
		t.Fatalf("%s: message does not name the remedy: %q", what, de.Message)
	}
}

func TestLockedVersionRefusesArtifactWrites(t *testing.T) {
	s, ctx, uploadID := lockedFixture(t)

	v, err := s.GetVersion(ctx, "m", "1")
	if err != nil || v.LockedAt == 0 {
		t.Fatalf("staging did not lock: %v %+v", err, v)
	}

	_, err = s.RegisterArtifact(ctx, "me", "m", "1", core.ArtifactInput{Name: "extra", URI: "s3://b/x"})
	wantLocked(t, "register", err)
	_, err = s.InitiateUpload(ctx, "me", "m", "1", core.InitiateUploadInput{Name: "new.bin"})
	wantLocked(t, "initiate upload", err)
	// The upload initiated while it was a draft cannot land its bytes or be finalized.
	wantLocked(t, "upload content", s.UploadContent(ctx, uploadID, strings.NewReader("bytes"), 5))
	_, err = s.FinalizeUpload(ctx, "me", "m", "1", uploadID, "", nil)
	wantLocked(t, "finalize upload", err)
	wantLocked(t, "delete artifact", s.DeleteArtifact(ctx, "me", "m", "1", "weights"))

	// Content fields keep their own, older refusal (no reason: immutable on any version).
	uri := "s3://b/other"
	if _, err := s.PatchArtifact(ctx, "me", "m", "1", "weights", core.PatchArtifactInput{URI: &uri}); err == nil {
		t.Fatal("changing a locked artifact's uri was accepted")
	}

	arts, _ := s.ListArtifacts(ctx, "m", "1")
	if len(arts) != 1 || arts[0].Name != "weights" {
		t.Fatalf("artifact set changed: %+v", arts)
	}
}

// Nothing unlocks: back to draft, through production and archived, the set stays frozen.
func TestLockSurvivesEveryLaterStage(t *testing.T) {
	s, ctx, _ := lockedFixture(t)
	v0, _ := s.GetVersion(ctx, "m", "1")
	for _, to := range []domain.Stage{domain.StageDraft, domain.StageStaging, domain.StageProduction, domain.StageArchived, domain.StageDraft} {
		if _, err := s.Transition(ctx, "me", "m", "1", to, ""); err != nil {
			t.Fatalf("→%s: %v", to, err)
		}
		_, err := s.RegisterArtifact(ctx, "me", "m", "1", core.ArtifactInput{Name: "extra", URI: "s3://b/x"})
		wantLocked(t, "register after →"+string(to), err)
		if v, _ := s.GetVersion(ctx, "m", "1"); v.LockedAt != v0.LockedAt {
			t.Fatalf("→%s moved lockedAt %d → %d", to, v0.LockedAt, v.LockedAt)
		}
	}
}

// A draft archived without ever reaching staging was never tested, so it stays open.
func TestArchivedDraftStaysOpen(t *testing.T) {
	ctx := context.Background()
	s := newSvc(t)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "me", "m", "1", domain.StageArchived, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterArtifact(ctx, "me", "m", "1", core.ArtifactInput{Name: "w", URI: "s3://b/w"}); err != nil {
		t.Fatalf("an archived draft refused a file: %v", err)
	}
}

// Everything that is not the artifact set keeps working on a locked version.
func TestLockedVersionKeepsEverythingElse(t *testing.T) {
	s, ctx, _ := lockedFixture(t)
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "2"}); err != nil {
		t.Fatal(err)
	}

	mt, sa := "application/x-onnx", "reader"
	a, err := s.PatchArtifact(ctx, "me", "m", "1", "weights", core.PatchArtifactInput{
		MediaType: &mt, ServiceAccount: &sa, CustomProperties: []byte(`{"note":"ok"}`)})
	if err != nil || a.MediaType != mt || a.ServiceAccount != sa {
		t.Fatalf("metadata patch on a locked artifact: %v %+v", err, a)
	}
	uri := "s3://b/w" // unchanged content fields are not a change
	if _, err := s.PatchArtifact(ctx, "me", "m", "1", "weights", core.PatchArtifactInput{URI: &uri}); err != nil {
		t.Fatalf("no-op content field: %v", err)
	}

	d := "notes"
	if _, err := s.PatchVersion(ctx, "me", "m", "1", core.PatchVersionInput{Description: &d, Labels: map[string]string{"k": "v"}}); err != nil {
		t.Fatalf("version metadata: %v", err)
	}
	if _, err := s.SetVersionHold(ctx, "legal", "m", "1", "matter"); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := s.ReleaseVersionHold(ctx, "legal", "m", "1", "closed"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := s.AddLineage(ctx, "me", "m", "1", core.LineageInput{Relation: domain.RelDerivedFrom,
		To: struct {
			Version string `json:"version"`
			URI     string `json:"uri"`
		}{Version: "2"}}); err != nil {
		t.Fatalf("lineage: %v", err)
	}
	yes := true
	if _, err := s.AddEvaluation(ctx, "me", "m", "1", core.EvaluationInput{Suite: "s", Metric: "acc", Value: 0.9, HigherIsBetter: &yes}); err != nil {
		t.Fatalf("evaluation: %v", err)
	}
	if _, err := s.CreateDeployment(ctx, "me", "m", "1", core.DeploymentInput{Environment: "prod"}); err != nil {
		t.Fatalf("deployment: %v", err)
	}
	if _, err := s.Transition(ctx, "me", "m", "1", domain.StageProduction, ""); err != nil {
		t.Fatalf("promotion: %v", err)
	}
	// Delete rules are unchanged: the production guard, not the lock, decides.
	if err := s.DeleteVersion(ctx, "me", "m", "1", true); err != nil {
		t.Fatalf("forced delete of a locked production version: %v", err)
	}
}
