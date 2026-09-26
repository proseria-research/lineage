package storetest

import (
	"context"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// RunMRM holds every adapter to identical model-risk behavior (§20.8): the `mrm` row beside
// the EU one without touching it, append-only validations with one set-once clear,
// stage_changed_at maintained by the stage machine, and the inventory's subject-version rule.
//
// It builds its own models so it can run after the other suites.
func RunMRM(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	now := domain.NowMillis()

	mk := func(name string) *domain.Model {
		m := &domain.Model{ID: domain.NewID(), Name: name, State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateModel(ctx, m); err != nil {
			t.Fatalf("CreateModel %s: %v", name, err)
		}
		return m
	}
	mkv := func(m *domain.Model, name, author string, at int64) *domain.ModelVersion {
		v := &domain.ModelVersion{ID: domain.NewID(), ModelID: m.ID, Name: name, Author: author,
			Stage: domain.StageDraft, CreatedAt: at, UpdatedAt: at}
		mustCreateVersion(t, store, v)
		return v
	}

	t.Run("regime isolation", func(t *testing.T) {
		m := mk("mrm-isolation")
		eu := &domain.RiskClassification{
			ModelID: m.ID, Regime: domain.RegimeEUAIAct,
			EUSystemRiskClass: domain.EUClassHighAnnexIII, EUGpaiTier: domain.EUGpaiNone,
			IntendedPurpose: "p", Basis: "b", ClassifiedAt: 1_773_000_000_000, ClassifiedBy: "risk@acme.example",
		}
		if err := store.PutClassification(ctx, eu); err != nil {
			t.Fatalf("Put eu_ai_act: %v", err)
		}
		// The June write of §16.3.2: a different team, a different regime, a later date.
		mrm := &domain.RiskClassification{
			ModelID: m.ID, Regime: domain.RegimeMRM, MRMTier: domain.MRMTier1,
			Basis: "Drives automated declines.", ClassifiedAt: 1_780_000_000_000, ClassifiedBy: "mrm@acme.example",
		}
		if err := store.PutClassification(ctx, mrm); err != nil {
			t.Fatalf("Put mrm: %v", err)
		}

		gotEU, err := store.GetClassification(ctx, m.ID, domain.RegimeEUAIAct)
		if err != nil {
			t.Fatalf("Get eu_ai_act: %v", err)
		}
		if gotEU.ClassifiedAt != eu.ClassifiedAt || gotEU.ClassifiedBy != eu.ClassifiedBy || gotEU.Basis != eu.Basis {
			t.Fatalf("writing mrm moved the EU row: %+v", gotEU)
		}
		if gotEU.MRMTier != "" {
			t.Fatalf("EU row grew an mrm tier: %q", gotEU.MRMTier)
		}
		gotMRM, err := store.GetClassification(ctx, m.ID, domain.RegimeMRM)
		if err != nil {
			t.Fatalf("Get mrm: %v", err)
		}
		if gotMRM.MRMTier != domain.MRMTier1 || gotMRM.Basis != mrm.Basis || gotMRM.ClassifiedAt != mrm.ClassifiedAt {
			t.Fatalf("mrm round-trip: %+v", gotMRM)
		}
		if gotMRM.EUSystemRiskClass != "" || gotMRM.EUGpaiTier != "" {
			t.Fatalf("mrm row grew an EU group: %+v", gotMRM)
		}
		list, err := store.ListClassifications(ctx, m.ID)
		if err != nil || len(list) != 2 || list[0].Regime != domain.RegimeEUAIAct || list[1].Regime != domain.RegimeMRM {
			t.Fatalf("ListClassifications: %v %+v", err, list)
		}

		// Replacing the EU row leaves the mrm row alone, in the other direction too.
		eu.ClassifiedAt = 1_790_000_000_000
		if err := store.PutClassification(ctx, eu); err != nil {
			t.Fatalf("re-Put eu_ai_act: %v", err)
		}
		if again, _ := store.GetClassification(ctx, m.ID, domain.RegimeMRM); again.ClassifiedAt != mrm.ClassifiedAt {
			t.Fatalf("writing eu_ai_act moved the mrm row: %+v", again)
		}
	})

	t.Run("stage_changed_at follows the stage machine", func(t *testing.T) {
		m := mk("mrm-stage")
		v1 := mkv(m, "1.0.0", "dev@acme.example", now-2_000)
		v2 := mkv(m, "2.0.0", "dev@acme.example", now-1_000)

		got, err := store.GetVersionByID(ctx, v1.ID)
		if err != nil {
			t.Fatalf("GetVersionByID: %v", err)
		}
		if got.StageChangedAt != v1.CreatedAt {
			t.Fatalf("a new version enters its first stage at creation: got %d want %d", got.StageChangedAt, v1.CreatedAt)
		}

		if err := store.SetStage(ctx, v1.ID, domain.StageProduction, true); err != nil {
			t.Fatalf("SetStage v1: %v", err)
		}
		promoted, _ := store.GetVersionByID(ctx, v1.ID)
		if promoted.StageChangedAt <= v1.CreatedAt {
			t.Fatalf("promotion did not move stage_changed_at: %d", promoted.StageChangedAt)
		}

		// A PATCH-style update is not a stage move.
		promoted.Description = "typo fixed"
		promoted.UpdatedAt = promoted.StageChangedAt + 10_000
		if err := store.UpdateVersion(ctx, promoted); err != nil {
			t.Fatalf("UpdateVersion: %v", err)
		}
		if after, _ := store.GetVersionByID(ctx, v1.ID); after.StageChangedAt != promoted.StageChangedAt {
			t.Fatalf("an update moved stage_changed_at: %d → %d", promoted.StageChangedAt, after.StageChangedAt)
		}

		// The demoted version enters `archived`, so it moves too.
		if err := store.SetStage(ctx, v2.ID, domain.StageProduction, true); err != nil {
			t.Fatalf("SetStage v2: %v", err)
		}
		demoted, _ := store.GetVersionByID(ctx, v1.ID)
		if demoted.Stage != domain.StageArchived || demoted.StageChangedAt < promoted.StageChangedAt {
			t.Fatalf("demotion: stage=%s stageChangedAt=%d", demoted.Stage, demoted.StageChangedAt)
		}
	})

	t.Run("validations are append-only, cleared once", func(t *testing.T) {
		m := mk("mrm-validations")
		v := mkv(m, "1.0.0", "dev@acme.example", now)
		other := mkv(m, "1.1.0", "dev@acme.example", now+1)

		if list, err := store.ListValidations(ctx, v.ID); err != nil || len(list) != 0 {
			t.Fatalf("ListValidations before any: %v len=%d", err, len(list))
		}
		until := now + 1_000_000
		first := &domain.Validation{ID: domain.NewID(), VersionID: v.ID, Outcome: domain.ValidationConditional,
			Scope: "cnp only", Findings: "psi elevated", Conditions: "re-measure monthly",
			ValidUntil: &until, EvidenceArtifactID: "01EVIDENCE", ValidatedBy: "mrm@acme.example", ValidatedAt: now}
		second := &domain.Validation{ID: domain.NewID(), VersionID: v.ID, Outcome: domain.ValidationApproved,
			ValidatedBy: "mrm@acme.example", ValidatedAt: now + 10}
		for _, val := range []*domain.Validation{first, second} {
			if err := store.CreateValidation(ctx, val); err != nil {
				t.Fatalf("CreateValidation: %v", err)
			}
		}

		list, err := store.ListValidations(ctx, v.ID)
		if err != nil || len(list) != 2 {
			t.Fatalf("ListValidations: %v len=%d", err, len(list))
		}
		// Newest first: the latest row is the current answer (§20.5), and the older one is
		// still there — a re-validation is a new row, not an overwrite.
		if list[0].ID != second.ID || list[1].ID != first.ID {
			t.Fatalf("order: %s, %s", list[0].ID, list[1].ID)
		}
		old := list[1]
		if old.Scope != first.Scope || old.Findings != first.Findings || old.Conditions != first.Conditions ||
			old.EvidenceArtifactID != first.EvidenceArtifactID || old.ValidatedBy != first.ValidatedBy ||
			old.ValidUntil == nil || *old.ValidUntil != until || old.ConditionsClearedAt != nil {
			t.Fatalf("round-trip: %+v", old)
		}
		if list[0].ValidUntil != nil {
			t.Fatalf("absent validUntil came back set: %v", *list[0].ValidUntil)
		}

		// Clearing is scoped to the version it names.
		if err := store.ClearValidationConditions(ctx, other.ID, first.ID, now+20); !isCode(err, domain.CodeNotFound) {
			t.Fatalf("clear via the wrong version: %v", err)
		}
		if err := store.ClearValidationConditions(ctx, v.ID, first.ID, now+20); err != nil {
			t.Fatalf("ClearValidationConditions: %v", err)
		}
		// Set once: a second clear cannot move the timestamp.
		if err := store.ClearValidationConditions(ctx, v.ID, first.ID, now+30); !isCode(err, domain.CodeFailedPrecondition) {
			t.Fatalf("second clear: %v", err)
		}
		list, _ = store.ListValidations(ctx, v.ID)
		if c := list[1].ConditionsClearedAt; c == nil || *c != now+20 {
			t.Fatalf("conditionsClearedAt: %v", c)
		}

		// Validations cascade with their version.
		if err := store.DeleteVersion(ctx, v.ID); err != nil {
			t.Fatalf("DeleteVersion: %v", err)
		}
		if list, _ := store.ListValidations(ctx, v.ID); len(list) != 0 {
			t.Fatalf("validations survived their version: %d", len(list))
		}
	})

	t.Run("inventory", func(t *testing.T) {
		tiered := mk("mrmi-tiered")
		mk("mrmi-untiered") // deliberately never tiered
		empty := mk("mrmi-empty")

		prod := mkv(tiered, "1.0.0", "dev@acme.example", now-3_000)
		newer := mkv(tiered, "2.0.0", "dev@acme.example", now-1_000)
		if err := store.SetStage(ctx, prod.ID, domain.StageProduction, true); err != nil {
			t.Fatalf("SetStage: %v", err)
		}
		put := func(m *domain.Model, tier domain.MRMTier) {
			c := &domain.RiskClassification{ModelID: m.ID, Regime: domain.RegimeMRM, MRMTier: tier,
				Basis: "b", ClassifiedAt: now, ClassifiedBy: "mrm@acme.example"}
			if err := store.PutClassification(ctx, c); err != nil {
				t.Fatalf("Put %s: %v", m.Name, err)
			}
		}
		put(tiered, domain.MRMTier1)
		put(empty, domain.MRMTier2)
		// An EU row on the tiered model must not surface through this lens.
		if err := store.PutClassification(ctx, &domain.RiskClassification{ModelID: tiered.ID, Regime: domain.RegimeEUAIAct,
			EUSystemRiskClass: domain.EUClassMinimal, EUGpaiTier: domain.EUGpaiNone, IntendedPurpose: "p", ClassifiedAt: now}); err != nil {
			t.Fatalf("Put eu: %v", err)
		}

		for _, val := range []*domain.Validation{
			{ID: domain.NewID(), VersionID: prod.ID, Outcome: domain.ValidationRejected, ValidatedAt: now + 1},
			{ID: domain.NewID(), VersionID: prod.ID, Outcome: domain.ValidationApproved, ValidatedBy: "mrm@acme.example", ValidatedAt: now + 2},
			// On the newer, non-production version: must not become the subject's.
			{ID: domain.NewID(), VersionID: newer.ID, Outcome: domain.ValidationRejected, ValidatedAt: now + 3},
		} {
			if err := store.CreateValidation(ctx, val); err != nil {
				t.Fatalf("CreateValidation: %v", err)
			}
		}
		// Two runs, older inserted second: the aggregate is MAX(run_at), not the last row.
		for _, runAt := range []int64{now + 100, now + 50} {
			if err := store.CreateEvaluation(ctx, &domain.Evaluation{ID: domain.NewID(), VersionID: prod.ID,
				Suite: "s", Metric: "m", Value: 1, HigherIsBetter: true,
				Source: domain.SourceMeasured, RunAt: runAt, CreatedAt: now}); err != nil {
				t.Fatalf("CreateEvaluation: %v", err)
			}
		}

		byName := func(f domain.MRMFilter) map[string]*domain.MRMInventoryRow {
			rows, err := store.ListMRMInventory(ctx, domain.ListOptions{Q: "mrmi-"}, f)
			if err != nil {
				t.Fatalf("ListMRMInventory: %v", err)
			}
			out := map[string]*domain.MRMInventoryRow{}
			for _, r := range rows {
				out[r.Model.Name] = r
			}
			return out
		}

		all := byName(domain.MRMFilter{})
		if len(all) != 3 {
			t.Fatalf("unfiltered: got %d rows, want 3 (untiered kept)", len(all))
		}
		if all["mrmi-untiered"].Classification != nil {
			t.Fatal("an untiered model came back with an mrm row")
		}
		r := all["mrmi-tiered"]
		if r.Classification == nil || r.Classification.Regime != domain.RegimeMRM || r.Classification.MRMTier != domain.MRMTier1 {
			t.Fatalf("mrm row not joined (or the EU row was): %+v", r.Classification)
		}
		// Production wins over newer.
		if s := r.Facts.Subject; s == nil || s.VersionID != prod.ID || s.Stage != domain.StageProduction ||
			s.Author != "dev@acme.example" || s.StageChangedAt == 0 {
			t.Fatalf("subject: %+v", r.Facts.Subject)
		}
		if r.Facts.LatestVersionCreatedAt != newer.CreatedAt {
			t.Fatalf("latestVersionCreatedAt = %d, want %d", r.Facts.LatestVersionCreatedAt, newer.CreatedAt)
		}
		if r.Facts.LatestEvaluationRunAt != now+100 {
			t.Fatalf("latestEvaluationRunAt = %d, want %d", r.Facts.LatestEvaluationRunAt, now+100)
		}
		if v := r.Facts.Validation; v == nil || v.Outcome != domain.ValidationApproved || v.ValidatedAt != now+2 {
			t.Fatalf("latest validation on the subject: %+v", r.Facts.Validation)
		}
		// No versions: no subject, no validation.
		if e := all["mrmi-empty"].Facts; e.Subject != nil || e.Validation != nil || e.LatestVersionCreatedAt != 0 {
			t.Fatalf("empty model facts: %+v", e)
		}

		// Without a production version the newest is the subject.
		if err := store.SetStage(ctx, prod.ID, domain.StageArchived, false); err != nil {
			t.Fatalf("SetStage archive: %v", err)
		}
		if s := byName(domain.MRMFilter{})["mrmi-tiered"].Facts.Subject; s == nil || s.VersionID != newer.ID {
			t.Fatalf("subject without production: %+v", s)
		}

		if only := byName(domain.MRMFilter{Tier: domain.MRMTier1}); len(only) != 1 || only["mrmi-tiered"] == nil {
			t.Fatalf("tier filter: %v", len(only))
		}
		if none := byName(domain.MRMFilter{Tier: domain.MRMOutOfScope}); len(none) != 0 {
			t.Fatalf("filter matching nothing: %d", len(none))
		}
		if one := byName(domain.MRMFilter{ModelID: empty.ID}); len(one) != 1 || one["mrmi-empty"] == nil {
			t.Fatalf("model filter: %d", len(one))
		}
	})
}

func isCode(err error, code string) bool {
	de, ok := err.(*domain.Error)
	return ok && de.Code == code
}
