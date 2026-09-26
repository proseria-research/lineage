package storetest

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// errAuditDown is the injected AppendAudit failure.
var errAuditDown = errors.New("storetest: audit write refused")

// failingAudit is a MetadataStore whose AppendAudit fails while armed. It re-wraps the tx
// InTx hands out, so the fault reaches the audit write inside the unit of work — which is the
// only place the core writes events.
type failingAudit struct {
	domain.MetadataStore
	armed *atomic.Bool
}

func (f failingAudit) AppendAudit(ctx context.Context, e *domain.AuditEvent) error {
	if f.armed.Load() {
		return errAuditDown
	}
	return f.MetadataStore.AppendAudit(ctx, e)
}

func (f failingAudit) InTx(ctx context.Context, fn func(tx domain.MetadataStore) error) error {
	return f.MetadataStore.InTx(ctx, func(tx domain.MetadataStore) error {
		return fn(failingAudit{MetadataStore: tx, armed: f.armed})
	})
}

// RunAuditAtomicity drives the core against store with AppendAudit failing, and holds every
// adapter to §02.5: a change whose event cannot be written fails, leaves nothing behind, and
// publishes no domain event (cache invalidation and webhooks run only after commit).
//
// It builds its own models so it can run after the other suites.
func RunAuditAtomicity(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	armed := &atomic.Bool{}
	bus := events.New()
	var published atomic.Int64
	bus.Subscribe(func(domain.Event) { published.Add(1) })
	svc := core.New(failingAudit{MetadataStore: store, armed: armed}, map[string]domain.StorageBackend{}, "", memcache.New(), bus)

	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	auditCount := func(t *testing.T) int {
		t.Helper()
		all, _, err := store.ListAudit(ctx, "", "", domain.ListOptions{})
		if err != nil {
			t.Fatalf("ListAudit: %v", err)
		}
		return len(all)
	}
	// model creates a model with one version, "1", with the fault disarmed.
	model := func(t *testing.T, name string) {
		t.Helper()
		_, err := svc.CreateModel(ctx, "ci", core.CreateModelInput{Name: name})
		must(t, err)
		_, _, err = svc.PublishVersion(ctx, "ci", name, core.PublishVersionInput{Name: "1"})
		must(t, err)
	}
	stageOf := func(t *testing.T, name, version string) domain.Stage {
		t.Helper()
		v, err := svc.GetVersion(ctx, name, version)
		must(t, err)
		return v.Stage
	}
	// A plan effective from now; a predecessor is declared an hour back so the successor
	// starts strictly after it.
	plan := func(supersedes string) core.ChangePlanInput {
		return core.ChangePlanInput{Summary: "retraining only", Supersedes: supersedes,
			AllowedVerdicts: []domain.Verdict{domain.VerdictReweighted}}
	}
	earlier := func() core.ChangePlanInput {
		p := plan("")
		from := domain.NowMillis() - 3_600_000
		p.EffectiveFrom = &from
		return p
	}

	cases := []struct {
		name  string
		setup func(t *testing.T, m string)
		op    func(m string) error
		check func(t *testing.T, m string)
	}{
		{
			name: "model create",
			op: func(m string) error {
				_, err := svc.CreateModel(ctx, "ci", core.CreateModelInput{Name: m})
				return err
			},
			check: func(t *testing.T, m string) {
				if _, err := store.GetModel(ctx, m); !domain.IsNotFound(err) {
					t.Fatalf("model exists without its event: %v", err)
				}
			},
		},
		{
			name: "version publish",
			setup: func(t *testing.T, m string) {
				_, err := svc.CreateModel(ctx, "ci", core.CreateModelInput{Name: m})
				must(t, err)
			},
			op: func(m string) error {
				_, _, err := svc.PublishVersion(ctx, "ci", m, core.PublishVersionInput{Name: "1",
					Artifacts: []core.ArtifactInput{{Name: "weights", URI: "s3://bucket/w"}}})
				return err
			},
			check: func(t *testing.T, m string) {
				if _, err := store.GetVersion(ctx, m, "1"); !domain.IsNotFound(err) {
					t.Fatalf("version exists without its event: %v", err)
				}
				if ok, err := store.ArtifactRefsURI(ctx, "s3://bucket/w"); err != nil || ok {
					t.Fatalf("artifact exists without its version's event: %v %v", ok, err)
				}
			},
		},
		{
			// The promotion would demote the live version; both halves must roll back.
			name: "stage transition with singleton demotion",
			setup: func(t *testing.T, m string) {
				model(t, m)
				_, err := svc.Transition(ctx, "ci", m, "1", domain.StageStaging, "")
				must(t, err)
				_, err = svc.Transition(ctx, "ci", m, "1", domain.StageProduction, "")
				must(t, err)
				_, _, err = svc.PublishVersion(ctx, "ci", m, core.PublishVersionInput{Name: "2"})
				must(t, err)
				_, err = svc.Transition(ctx, "ci", m, "2", domain.StageStaging, "")
				must(t, err)
			},
			op: func(m string) error {
				_, err := svc.Transition(ctx, "ci", m, "2", domain.StageProduction, "")
				return err
			},
			check: func(t *testing.T, m string) {
				if s := stageOf(t, m, "2"); s != domain.StageStaging {
					t.Fatalf("promoted without its event: %s", s)
				}
				if s := stageOf(t, m, "1"); s != domain.StageProduction {
					t.Fatalf("demoted without an event: %s", s)
				}
			},
		},
		{
			name:  "classification set",
			setup: model,
			op: func(m string) error {
				_, err := svc.SetClassification(ctx, "ci", m, domain.RegimeEUAIAct, core.ClassificationInput{
					EUSystemRiskClass: domain.EUClassMinimal, EUGpaiTier: domain.EUGpaiNone, IntendedPurpose: "demo"})
				return err
			},
			check: func(t *testing.T, m string) {
				mm, err := store.GetModel(ctx, m)
				must(t, err)
				if _, err := store.GetClassification(ctx, mm.ID, domain.RegimeEUAIAct); !domain.IsNotFound(err) {
					t.Fatalf("classification exists without its event: %v", err)
				}
			},
		},
		{
			name:  "hold set",
			setup: model,
			op: func(m string) error {
				_, err := svc.SetModelHold(ctx, "ci", m, "matter 42")
				return err
			},
			check: func(t *testing.T, m string) {
				if mm, err := store.GetModel(ctx, m); err != nil || mm.LegalHold != nil {
					t.Fatalf("hold placed without its event: %v %+v", err, mm)
				}
			},
		},
		{
			name: "hold release",
			setup: func(t *testing.T, m string) {
				model(t, m)
				_, err := svc.SetVersionHold(ctx, "ci", m, "1", "matter 42")
				must(t, err)
			},
			op: func(m string) error {
				_, err := svc.ReleaseVersionHold(ctx, "ci", m, "1", "closed")
				return err
			},
			check: func(t *testing.T, m string) {
				if v, err := store.GetVersion(ctx, m, "1"); err != nil || v.LegalHold == nil {
					t.Fatalf("hold released without its event: %v %+v", err, v)
				}
			},
		},
		{
			name:  "validation record",
			setup: model,
			op: func(m string) error {
				_, err := svc.RecordValidation(ctx, "validator", m, "1", core.ValidationInput{Outcome: domain.ValidationApproved})
				return err
			},
			check: func(t *testing.T, m string) {
				v, err := store.GetVersion(ctx, m, "1")
				must(t, err)
				if vals, err := store.ListValidations(ctx, v.ID); err != nil || len(vals) != 0 {
					t.Fatalf("validation recorded without its event: %v %d", err, len(vals))
				}
			},
		},
		{
			name: "validation conditions cleared",
			setup: func(t *testing.T, m string) {
				model(t, m)
				_, err := svc.RecordValidation(ctx, "validator", m, "1", core.ValidationInput{
					Outcome: domain.ValidationConditional, Conditions: "add a drift monitor"})
				must(t, err)
			},
			op: func(m string) error {
				v, err := store.GetVersion(ctx, m, "1")
				if err != nil {
					return err
				}
				vals, err := store.ListValidations(ctx, v.ID)
				if err != nil || len(vals) != 1 {
					return errors.New("setup: expected one validation")
				}
				_, err = svc.ClearValidationConditions(ctx, "validator", m, "1", vals[0].ID)
				return err
			},
			check: func(t *testing.T, m string) {
				v, err := store.GetVersion(ctx, m, "1")
				must(t, err)
				vals, err := store.ListValidations(ctx, v.ID)
				must(t, err)
				if len(vals) != 1 || vals[0].ConditionsClearedAt != nil {
					t.Fatalf("conditions cleared without the event: %+v", vals)
				}
			},
		},
		{
			name:  "change plan declare",
			setup: model,
			op: func(m string) error {
				_, err := svc.DeclareChangePlan(ctx, "ra", m, plan(""))
				return err
			},
			check: func(t *testing.T, m string) {
				if ps, err := svc.ListChangePlans(ctx, m); err != nil || len(ps) != 0 {
					t.Fatalf("plan declared without its event: %v %d", err, len(ps))
				}
			},
		},
		{
			// Two events in one unit: the declaration and the supersession.
			name: "change plan supersede",
			setup: func(t *testing.T, m string) {
				model(t, m)
				_, err := svc.DeclareChangePlan(ctx, "ra", m, earlier())
				must(t, err)
			},
			op: func(m string) error {
				ps, err := svc.ListChangePlans(ctx, m)
				if err != nil || len(ps) != 1 {
					return errors.New("setup: expected one plan")
				}
				_, err = svc.DeclareChangePlan(ctx, "ra", m, plan(ps[0].ID))
				return err
			},
			check: func(t *testing.T, m string) {
				ps, err := svc.ListChangePlans(ctx, m)
				must(t, err)
				if len(ps) != 1 || ps[0].EffectiveTo != nil {
					t.Fatalf("predecessor closed or successor added without events: %+v", ps)
				}
			},
		},
		{
			name:  "version delete",
			setup: model,
			op: func(m string) error {
				return svc.DeleteVersion(ctx, "ci", m, "1", false)
			},
			check: func(t *testing.T, m string) {
				if _, err := store.GetVersion(ctx, m, "1"); err != nil {
					t.Fatalf("version deleted without its event: %v", err)
				}
			},
		},
	}

	for i, tc := range cases {
		t.Run("audit failure rolls back "+tc.name, func(t *testing.T) {
			m := "atomic-" + string(rune('a'+i))
			armed.Store(false)
			if tc.setup != nil {
				tc.setup(t, m)
			}
			before, pubBefore := auditCount(t), published.Load()

			armed.Store(true)
			err := tc.op(m)
			armed.Store(false)

			if !errors.Is(err, errAuditDown) {
				t.Fatalf("op error = %v, want the audit failure", err)
			}
			tc.check(t, m)
			if after := auditCount(t); after != before {
				t.Fatalf("audit rows %d → %d across a failed change", before, after)
			}
			if p := published.Load(); p != pubBefore {
				t.Fatalf("a rolled-back change published %d domain event(s)", p-pubBefore)
			}
		})
	}
}
