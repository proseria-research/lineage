package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// errAbort is what a unit of work returns to force a rollback.
var errAbort = errors.New("storetest: abort unit of work")

// RunUnitOfWork holds every adapter to the same InTx contract (domain.Transactor): commit on
// nil, roll back on an error or a panic, nested calls join the outer unit — including the store
// methods that carry their own atomicity — and the singleton-stage lock still serialises
// concurrent promotions when each one also writes its event.
//
// It builds its own models so it can run after the other suites.
func RunUnitOfWork(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	now := domain.NowMillis()

	model := func(name string) *domain.Model {
		return &domain.Model{ID: domain.NewID(), Name: name, State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
	}
	event := func(subjectID string) *domain.AuditEvent {
		return &domain.AuditEvent{ID: domain.NewID(), At: domain.NowMillis(), Actor: "ci", Action: "uow.test",
			SubjectType: "model", SubjectID: subjectID}
	}
	events := func(t *testing.T, subjectID string) int {
		t.Helper()
		got, _, err := store.ListAudit(ctx, "model", subjectID, domain.ListOptions{})
		if err != nil {
			t.Fatalf("ListAudit: %v", err)
		}
		return len(got)
	}
	absent := func(t *testing.T, name string) {
		t.Helper()
		if _, err := store.GetModel(ctx, name); !domain.IsNotFound(err) {
			t.Fatalf("model %s should not exist after rollback: %v", name, err)
		}
	}

	t.Run("commit", func(t *testing.T) {
		m := model("uow-commit")
		if err := store.InTx(ctx, func(tx domain.MetadataStore) error {
			if err := tx.CreateModel(ctx, m); err != nil {
				return err
			}
			// Reads inside the unit see its own writes.
			if _, err := tx.GetModel(ctx, m.Name); err != nil {
				return err
			}
			return tx.AppendAudit(ctx, event(m.ID))
		}); err != nil {
			t.Fatalf("InTx: %v", err)
		}
		if _, err := store.GetModel(ctx, m.Name); err != nil {
			t.Fatalf("committed model missing: %v", err)
		}
		if n := events(t, m.ID); n != 1 {
			t.Fatalf("committed events = %d, want 1", n)
		}
	})

	t.Run("error rolls back and is returned as is", func(t *testing.T) {
		m := model("uow-rollback")
		err := store.InTx(ctx, func(tx domain.MetadataStore) error {
			if err := tx.CreateModel(ctx, m); err != nil {
				return err
			}
			if err := tx.AppendAudit(ctx, event(m.ID)); err != nil {
				return err
			}
			return errAbort
		})
		if !errors.Is(err, errAbort) {
			t.Fatalf("InTx error = %v, want errAbort", err)
		}
		absent(t, m.Name)
		if n := events(t, m.ID); n != 0 {
			t.Fatalf("rolled-back events = %d, want 0", n)
		}
	})

	t.Run("panic rolls back and leaves the store usable", func(t *testing.T) {
		m := model("uow-panic")
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("expected the panic to propagate")
				}
			}()
			_ = store.InTx(ctx, func(tx domain.MetadataStore) error {
				if err := tx.CreateModel(ctx, m); err != nil {
					return err
				}
				panic("boom")
			})
		}()
		absent(t, m.Name)
		// A leaked transaction would hold SQLite's one connection, or the memory lock, and
		// this write would never return.
		after := model("uow-after-panic")
		if err := store.CreateModel(ctx, after); err != nil {
			t.Fatalf("store unusable after a panicking unit of work: %v", err)
		}
	})

	// The methods that open their own transaction outside a unit of work must join the
	// caller's inside one: the outer failure has to undo them too.
	t.Run("nested atomic methods join the outer unit", func(t *testing.T) {
		m := model("uow-nested")
		if err := store.CreateModel(ctx, m); err != nil {
			t.Fatalf("CreateModel: %v", err)
		}
		live := mkVersion(m.ID, "1")
		next := mkVersion(m.ID, "2")
		mustCreateVersion(t, store, live)
		mustCreateVersion(t, store, next)
		if err := store.SetStage(ctx, live.ID, domain.StageProduction, true); err != nil {
			t.Fatalf("SetStage: %v", err)
		}

		err := store.InTx(ctx, func(tx domain.MetadataStore) error {
			if err := tx.SetStage(ctx, next.ID, domain.StageProduction, true); err != nil {
				return err
			}
			if err := tx.PutClassification(ctx, &domain.RiskClassification{
				ModelID: m.ID, Regime: domain.RegimeEUAIAct, EUSystemRiskClass: domain.EUClassMinimal,
				EUGpaiTier: domain.EUGpaiNone, ClassifiedAt: now, ClassifiedBy: "ci",
			}); err != nil {
				return err
			}
			if err := tx.CreateChangePlan(ctx, &domain.ChangePlan{
				ID: domain.NewID(), ModelID: m.ID, Summary: "nested",
				AllowedVerdicts: []domain.Verdict{domain.VerdictIdentical},
				EffectiveFrom:   now, DeclaredBy: "ci", DeclaredAt: now,
			}, ""); err != nil {
				return err
			}
			if err := tx.UpsertInsight(ctx, &domain.VersionInsight{VersionID: next.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			if err := tx.ReplaceLayerBlocks(ctx, next.ID, []*domain.LayerBlock{{VersionID: next.ID, Ordinal: 0, Path: "l0"}}); err != nil {
				return err
			}
			if err := tx.UpsertFootprint(ctx, &domain.Footprint{
				ID: domain.NewID(), VersionID: next.ID, Scenario: "s", Source: domain.FootprintMeasured,
				CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return err
			}
			// A nested InTx on the view joins rather than committing on its own.
			if err := tx.InTx(ctx, func(inner domain.MetadataStore) error {
				return inner.AppendAudit(ctx, event(m.ID))
			}); err != nil {
				return err
			}
			return errAbort
		})
		if !errors.Is(err, errAbort) {
			t.Fatalf("InTx error = %v, want errAbort", err)
		}

		if v, _ := store.GetVersionByID(ctx, live.ID); v == nil || v.Stage != domain.StageProduction {
			t.Fatalf("demotion of the live version was not rolled back: %+v", v)
		}
		if v, _ := store.GetVersionByID(ctx, next.ID); v == nil || v.Stage != domain.StageDraft {
			t.Fatalf("promotion was not rolled back: %+v", v)
		}
		if _, err := store.GetClassification(ctx, m.ID, domain.RegimeEUAIAct); !domain.IsNotFound(err) {
			t.Fatalf("classification was not rolled back: %v", err)
		}
		if plans, err := store.ListChangePlans(ctx, m.ID); err != nil || len(plans) != 0 {
			t.Fatalf("change plan was not rolled back: %v %d", err, len(plans))
		}
		if _, err := store.GetInsight(ctx, next.ID); !domain.IsNotFound(err) {
			t.Fatalf("insight was not rolled back: %v", err)
		}
		if ls, err := store.ListLayerBlocks(ctx, next.ID); err != nil || len(ls) != 0 {
			t.Fatalf("layer blocks were not rolled back: %v %d", err, len(ls))
		}
		if fs, err := store.ListFootprints(ctx, next.ID); err != nil || len(fs) != 0 {
			t.Fatalf("footprint was not rolled back: %v %d", err, len(fs))
		}
		if n := events(t, m.ID); n != 0 {
			t.Fatalf("nested event was not rolled back: %d", n)
		}
	})

	// §02.4 under the unit of work: every promotion also writes its event inside the same
	// transaction, and the singleton invariant must still come out exactly one.
	t.Run("concurrent promotions with events keep one production", func(t *testing.T) {
		m := model("uow-race")
		if err := store.CreateModel(ctx, m); err != nil {
			t.Fatalf("CreateModel: %v", err)
		}
		const n = 8
		vs := make([]*domain.ModelVersion, n)
		for i := range vs {
			vs[i] = mkVersion(m.ID, string(rune('a'+i)))
			mustCreateVersion(t, store, vs[i])
		}
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for _, v := range vs {
			wg.Add(1)
			go func(v *domain.ModelVersion) {
				defer wg.Done()
				errs <- store.InTx(ctx, func(tx domain.MetadataStore) error {
					if err := tx.SetStage(ctx, v.ID, domain.StageProduction, true); err != nil {
						return err
					}
					return tx.AppendAudit(ctx, event(m.ID))
				})
			}(v)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent promotion: %v", err)
			}
		}
		if c, err := store.CountVersionsInStage(ctx, m.ID, domain.StageProduction); err != nil || c != 1 {
			t.Fatalf("production versions = %d (%v), want exactly 1", c, err)
		}
		if got := events(t, m.ID); got != n {
			t.Fatalf("events = %d, want %d — one per committed promotion", got, n)
		}
	})
}
