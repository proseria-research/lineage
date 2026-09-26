package core_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// TestEveryWriteAuditsExactlyOnce walks every state-changing Service method in order and
// checks the events each one appended: exactly one, with the expected action — except
// supersession, which records the declaration and the closed predecessor as two (§22.7.1),
// and a no-op transition, which changes nothing and so records nothing (§02.5).
//
// The steps share one registry so each can build on the last; a step's expectation is the
// delta in the global feed across that step alone.
func TestEveryWriteAuditsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	s := newSvc(t)
	const m = "audited"
	yes := true

	var edgeID, depID, validationID, planID, uploadID string

	steps := []struct {
		name string
		op   func() error
		want []string
	}{
		{"create model", func() error {
			_, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: m})
			return err
		}, []string{"model.create"}},
		{"patch model", func() error {
			d := "described"
			_, err := s.PatchModel(ctx, "me", m, core.PatchModelInput{Description: &d})
			return err
		}, []string{"model.update"}},
		{"publish version with artifacts", func() error {
			_, _, err := s.PublishVersion(ctx, "me", m, core.PublishVersionInput{Name: "1", Artifacts: []core.ArtifactInput{
				{Name: "weights", URI: "s3://b/w1"}, {Name: "report", Kind: domain.KindDoc, URI: "s3://b/r1"},
			}})
			return err
		}, []string{"version.create"}},
		{"publish second version", func() error {
			_, _, err := s.PublishVersion(ctx, "me", m, core.PublishVersionInput{Name: "2"})
			return err
		}, []string{"version.create"}},
		{"patch version", func() error {
			d := "v1"
			_, err := s.PatchVersion(ctx, "me", m, "1", core.PatchVersionInput{Description: &d})
			return err
		}, []string{"version.update"}},
		{"register artifact", func() error {
			_, err := s.RegisterArtifact(ctx, "me", m, "1", core.ArtifactInput{Name: "tokenizer", URI: "s3://b/t1"})
			return err
		}, []string{"artifact.register"}},
		{"patch artifact", func() error {
			mt := "application/octet-stream"
			_, err := s.PatchArtifact(ctx, "me", m, "1", "tokenizer", core.PatchArtifactInput{MediaType: &mt})
			return err
		}, []string{"artifact.update"}},
		{"delete artifact", func() error {
			return s.DeleteArtifact(ctx, "me", m, "1", "tokenizer")
		}, []string{"artifact.delete"}},
		{"initiate upload writes nothing", func() error {
			tk, err := s.InitiateUpload(ctx, "me", m, "1", core.InitiateUploadInput{Name: "extra.bin"})
			if err == nil {
				uploadID = tk.UploadID
			}
			return err
		}, nil},
		{"upload content writes nothing", func() error {
			return s.UploadContent(ctx, uploadID, strings.NewReader("bytes"), 5)
		}, nil},
		{"finalize upload", func() error {
			_, err := s.FinalizeUpload(ctx, "me", m, "1", uploadID, "", nil)
			return err
		}, []string{"artifact.upload"}},
		// After the artifact writes: entering staging locks the set (§00.11.19).
		{"transition", func() error {
			_, err := s.Transition(ctx, "me", m, "1", domain.StageStaging, "qa")
			return err
		}, []string{"version.stage_changed"}},
		{"transition to the current stage is a no-op", func() error {
			_, err := s.Transition(ctx, "me", m, "1", domain.StageStaging, "again")
			return err
		}, nil},
		{"add lineage", func() error {
			e, err := s.AddLineage(ctx, "me", m, "2", core.LineageInput{Relation: domain.RelDerivedFrom,
				To: struct {
					Version string `json:"version"`
					URI     string `json:"uri"`
				}{Version: "1"}})
			if err == nil {
				edgeID = e.ID
			}
			return err
		}, []string{"lineage.add"}},
		{"record review", func() error {
			_, err := s.RecordReview(ctx, "reviewer", m, "2", core.ReviewInput{EdgeID: edgeID, Outcome: domain.OutcomeNotSubstantial})
			return err
		}, []string{"review.record"}},
		{"write insight", func() error {
			_, err := s.WriteInsight(ctx, "me", m, "1", core.InsightWrite{SchemaVersion: "1", Source: domain.SourceMeasured, Reporter: "ci",
				Facts: map[string]json.RawMessage{"tensorCount": json.RawMessage(`12`)}}, false, false)
			return err
		}, []string{"insight.update"}},
		{"replace insight", func() error {
			_, err := s.WriteInsight(ctx, "me", m, "1", core.InsightWrite{SchemaVersion: "1", Source: domain.SourceMeasured, Reporter: "ci",
				Facts: map[string]json.RawMessage{"tensorCount": json.RawMessage(`13`)}}, true, false)
			return err
		}, []string{"insight.replace"}},
		{"add evaluation", func() error {
			_, err := s.AddEvaluation(ctx, "me", m, "1", core.EvaluationInput{Suite: "s", Metric: "acc", Value: 0.9, HigherIsBetter: &yes})
			return err
		}, []string{"evaluation.create"}},
		{"put footprint", func() error {
			_, err := s.PutFootprint(ctx, "me", m, "1", "serve", core.FootprintInput{Source: domain.FootprintMeasured})
			return err
		}, []string{"footprint.update"}},
		{"set classification", func() error {
			_, err := s.SetClassification(ctx, "risk", m, domain.RegimeEUAIAct, highRisk())
			return err
		}, []string{"classification.set"}},
		{"set mrm classification", func() error {
			_, err := s.SetClassification(ctx, "risk", m, domain.RegimeMRM, core.ClassificationInput{MRMTier: domain.MRMTier3, IntendedPurpose: "scoring"})
			return err
		}, []string{"classification.set"}},
		{"record validation", func() error {
			v, err := s.RecordValidation(ctx, "validator", m, "1", core.ValidationInput{
				Outcome: domain.ValidationConditional, Conditions: "add a drift monitor"})
			if err == nil {
				validationID = v.ID
			}
			return err
		}, []string{"validation.record"}},
		{"clear validation conditions", func() error {
			_, err := s.ClearValidationConditions(ctx, "validator", m, "1", validationID)
			return err
		}, []string{"validation.conditions_cleared"}},
		{"declare change plan", func() error {
			from := domain.NowMillis() - 3_600_000
			p, err := s.DeclareChangePlan(ctx, "ra", m, core.ChangePlanInput{Summary: "retrain",
				AllowedVerdicts: []domain.Verdict{domain.VerdictReweighted}, EffectiveFrom: &from})
			if err == nil {
				planID = p.ID
			}
			return err
		}, []string{"change_plan.declare"}},
		{"supersede change plan", func() error {
			_, err := s.DeclareChangePlan(ctx, "ra", m, core.ChangePlanInput{Summary: "retrain v2",
				AllowedVerdicts: []domain.Verdict{domain.VerdictReweighted}, Supersedes: planID})
			return err
		}, []string{"change_plan.declare", "change_plan.supersede"}},
		{"create deployment", func() error {
			d, err := s.CreateDeployment(ctx, "me", m, "1", core.DeploymentInput{Environment: "prod"})
			if err == nil {
				depID = d.ID
			}
			return err
		}, []string{"deployment.create"}},
		{"patch deployment", func() error {
			st := domain.DeployInactive
			_, err := s.PatchDeployment(ctx, "me", depID, core.DeploymentPatch{Status: &st})
			return err
		}, []string{"deployment.update"}},
		{"delete deployment", func() error {
			return s.DeleteDeployment(ctx, "me", depID)
		}, []string{"deployment.delete"}},
		{"set model hold", func() error {
			_, err := s.SetModelHold(ctx, "legal", m, "matter 1")
			return err
		}, []string{"hold.set"}},
		{"release model hold", func() error {
			_, err := s.ReleaseModelHold(ctx, "legal", m, "closed")
			return err
		}, []string{"hold.release"}},
		{"set version hold", func() error {
			_, err := s.SetVersionHold(ctx, "legal", m, "2", "matter 2")
			return err
		}, []string{"hold.set"}},
		{"release version hold", func() error {
			_, err := s.ReleaseVersionHold(ctx, "legal", m, "2", "closed")
			return err
		}, []string{"hold.release"}},
		{"delete lineage", func() error {
			return s.DeleteLineage(ctx, "me", m, "2", edgeID)
		}, []string{"lineage.delete"}},
		{"archive model", func() error {
			_, err := s.ArchiveModel(ctx, "me", m)
			return err
		}, []string{"model.update"}},
		{"delete version", func() error {
			return s.DeleteVersion(ctx, "me", m, "2", false)
		}, []string{"version.delete"}},
		{"delete model", func() error {
			return s.DeleteModel(ctx, "me", m, true)
		}, []string{"model.delete"}},
	}

	// Diffed by id: rows written in the same millisecond have no defined order in the feed.
	seen := map[string]bool{}
	for _, st := range steps {
		if err := st.op(); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		all, _, err := s.ListAudit(ctx, "", "", domain.ListOptions{})
		if err != nil {
			t.Fatalf("ListAudit: %v", err)
		}
		var got []string
		for _, e := range all {
			if !seen[e.ID] {
				seen[e.ID] = true
				got = append(got, e.Action)
			}
		}
		slices.Sort(got)
		want := slices.Clone(st.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: events %v, want %v", st.name, got, want)
		}
	}
}
