package core_test

import (
	"context"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// svcWithFloor is newSvc plus a configured retention floor (§19.4).
func svcWithFloor(t *testing.T, days int) *core.Service {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	return core.New(
		memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend},
		backend.Name(),
		memcache.New(),
		events.New(),
		core.WithRetention(domain.RetentionConfig{MinArchivedVersionDays: days}),
	)
}

// holdable returns a service with model "m" and version "1.0.0".
func holdable(t *testing.T, s *core.Service) context.Context {
	t.Helper()
	ctx := context.Background()
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func refusal(t *testing.T, err error) map[string]any {
	t.Helper()
	de, ok := err.(*domain.Error)
	if !ok {
		t.Fatalf("expected a *domain.Error, got %T: %v", err, err)
	}
	if de.Code != domain.CodeFailedPrecondition {
		t.Fatalf("code = %q, want %q", de.Code, domain.CodeFailedPrecondition)
	}
	return de.Details
}

// ---- Holds ----

func TestSetAndReleaseModelHold(t *testing.T) {
	s := newSvc(t)
	ctx := holdable(t, s)

	m, err := s.SetModelHold(ctx, "counsel@acme.example", "m", "Regulator inquiry REF-2026-118")
	if err != nil {
		t.Fatalf("SetModelHold: %v", err)
	}
	if m.LegalHold == nil || m.LegalHold.HeldBy != "counsel@acme.example" || m.LegalHold.HeldSince == 0 {
		t.Fatalf("hold not recorded on the returned model: %+v", m.LegalHold)
	}

	// The reason lives on the event, not the row — the row would be overwritten by the next
	// hold, and the matter is exactly what someone reconstructs years later (§19.7.1).
	ev := auditActions(t, s, ctx, "model", m.ID)
	if ev["hold.set"] == "" {
		t.Fatalf("expected a hold.set event, got %v", ev)
	}
	if ev["hold.set"] != `{"reason":"Regulator inquiry REF-2026-118"}` {
		t.Fatalf("hold.set data = %s", ev["hold.set"])
	}

	m, err = s.ReleaseModelHold(ctx, "counsel@acme.example", "m", "Matter closed")
	if err != nil {
		t.Fatalf("ReleaseModelHold: %v", err)
	}
	if m.LegalHold != nil {
		t.Fatalf("release must clear the hold: %+v", m.LegalHold)
	}
	// Clearing a hold is a separate, audited action (§19.3.1) — it is the event an auditor
	// looks for, so it can never be implicit.
	if ev := auditActions(t, s, ctx, "model", m.ID); ev["hold.release"] == "" {
		t.Fatalf("expected a hold.release event, got %v", ev)
	}
}

func TestHoldRequiresAReason(t *testing.T) {
	s := newSvc(t)
	ctx := holdable(t, s)
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"set", func() error { _, err := s.SetModelHold(ctx, "me", "m", ""); return err }},
		{"release", func() error { _, err := s.ReleaseModelHold(ctx, "me", "m", ""); return err }},
	} {
		err := tc.call()
		if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeInvalidArgument {
			t.Fatalf("%s without a reason: want invalid_argument, got %v", tc.name, err)
		}
	}
}

func TestDoubleHoldAndDoubleRelease(t *testing.T) {
	s := newSvc(t)
	ctx := holdable(t, s)

	// Releasing something not held is a refusal, not a no-op (§19.7.4).
	_, err := s.ReleaseModelHold(ctx, "me", "m", "x")
	if d := refusal(t, err); d["reason"] != domain.RefusedNotHeld {
		t.Fatalf("reason = %v, want %q", d["reason"], domain.RefusedNotHeld)
	}

	if _, err := s.SetModelHold(ctx, "counsel@acme.example", "m", "Matter A"); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetModel(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}

	// Re-holding refuses rather than refreshing. The date a hold was placed is evidence;
	// silently moving it forward would rewrite it.
	_, err = s.SetModelHold(ctx, "other@acme.example", "m", "Matter B")
	d := refusal(t, err)
	if d["reason"] != "already_held" {
		t.Fatalf("reason = %v, want already_held", d["reason"])
	}
	if d["heldBy"] != "counsel@acme.example" {
		t.Fatalf("the refusal must name the existing holder, got %v", d["heldBy"])
	}
	after, err := s.GetModel(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	if after.LegalHold.HeldSince != before.LegalHold.HeldSince || after.LegalHold.HeldBy != before.LegalHold.HeldBy {
		t.Fatalf("a refused re-hold must not touch the stored hold: %+v -> %+v", before.LegalHold, after.LegalHold)
	}
}

// ---- The delete guard ----

func TestDeleteRefusedByHold(t *testing.T) {
	s := newSvc(t)
	ctx := holdable(t, s)
	if _, err := s.SetModelHold(ctx, "counsel@acme.example", "m", "Regulator inquiry"); err != nil {
		t.Fatal(err)
	}

	err := s.DeleteModel(ctx, "me", "m", false)
	if d := refusal(t, err); d["reason"] != domain.RefusedLegalHold {
		t.Fatalf("reason = %v, want %q", d["reason"], domain.RefusedLegalHold)
	}

	// §19.3.1, downward: the version under it is refused too, and the refusal names the model
	// so the caller knows what to release.
	err = s.DeleteVersion(ctx, "me", "m", "1.0.0", false)
	d := refusal(t, err)
	if d["reason"] != domain.RefusedLegalHold || d["heldSubject"] != "model/m" {
		t.Fatalf("version under a held model: %v", d)
	}
}

// TestForceCannotClearAHold is the one that matters. ?force=true overrides the
// production-version guard, which protects an operator from their own mistake; a hold
// protects evidence from the operator, and a flag that clears it is not a hold.
func TestForceCannotClearAHold(t *testing.T) {
	s := newSvc(t)
	ctx := holdable(t, s)
	if _, err := s.SetModelHold(ctx, "counsel@acme.example", "m", "Regulator inquiry"); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		if d := refusal(t, s.DeleteModel(ctx, "me", "m", force)); d["reason"] != domain.RefusedLegalHold {
			t.Fatalf("force=%v: reason = %v", force, d["reason"])
		}
	}
	if _, err := s.GetModel(ctx, "m"); err != nil {
		t.Fatalf("the model must still exist: %v", err)
	}
}

// TestHoldRefusalPrecedesTheForceHint: when both guards apply, the caller must hear about the
// hold. Reporting "retry with ?force=true" while a legal hold is in place would be actively
// misleading — they would retry, and be refused again with no new information.
func TestHoldRefusalPrecedesTheForceHint(t *testing.T) {
	s := newSvc(t)
	ctx := holdable(t, s)
	if _, err := s.Transition(ctx, "me", "m", "1.0.0", domain.StageStaging, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "me", "m", "1.0.0", domain.StageProduction, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetModelHold(ctx, "counsel@acme.example", "m", "Regulator inquiry"); err != nil {
		t.Fatal(err)
	}
	d := refusal(t, s.DeleteModel(ctx, "me", "m", false))
	if d["reason"] != domain.RefusedLegalHold {
		t.Fatalf("reason = %v, want the hold, not the production guard", d["reason"])
	}
	if _, ok := d["hint"]; ok {
		t.Fatalf("a hold refusal must not offer the force hint: %v", d)
	}
}

// TestUpwardHoldRefusesTheCascade: deleting a model destroys every version under it, so a
// hold on any one of them has to refuse. §19.3.1 states only the downward rule; this is the
// larger destruction of the two.
func TestUpwardHoldRefusesTheCascade(t *testing.T) {
	s := newSvc(t)
	ctx := holdable(t, s)
	if _, err := s.SetVersionHold(ctx, "counsel@acme.example", "m", "1.0.0", "Regulator inquiry"); err != nil {
		t.Fatal(err)
	}
	d := refusal(t, s.DeleteModel(ctx, "me", "m", true))
	if d["reason"] != domain.RefusedLegalHold || d["heldSubject"] != "version/m@1.0.0" {
		t.Fatalf("model delete over a held version: %v", d)
	}
}

// ---- The floor ----

func TestFloorRefusesAYoungSubject(t *testing.T) {
	s := svcWithFloor(t, 3650)
	ctx := holdable(t, s)

	d := refusal(t, s.DeleteVersion(ctx, "me", "m", "1.0.0", true))
	if d["reason"] != domain.RefusedRetentionFloor {
		t.Fatalf("reason = %v, want %q", d["reason"], domain.RefusedRetentionFloor)
	}
	if d["floorDays"] != 3650 || d["ageDays"] != int64(0) {
		t.Fatalf("details = %v", d)
	}
	// Same for the model, whose cascade would take the version with it.
	if d := refusal(t, s.DeleteModel(ctx, "me", "m", true)); d["reason"] != domain.RefusedRetentionFloor {
		t.Fatalf("model: reason = %v", d["reason"])
	}
}

// TestFloorDisabledByDefault: 3650 is the *chart's* default, not the binary's. A service
// built without WithRetention imposes no floor, or every existing install would start
// refusing deletes on upgrade.
func TestFloorDisabledByDefault(t *testing.T) {
	s := newSvc(t)
	ctx := holdable(t, s)
	if got := s.Retention(); got.MinArchivedVersionDays != 0 || got.MinAuditAgeDays != 0 {
		t.Fatalf("unconfigured retention = %+v, want both floors disabled", got)
	}
	if err := s.DeleteVersion(ctx, "me", "m", "1.0.0", true); err != nil {
		t.Fatalf("with no floor configured the delete must proceed: %v", err)
	}
}

// TestFloorZeroIsARealChoice: §19.4 insists 0 means "disabled", not "unset".
func TestFloorZeroIsARealChoice(t *testing.T) {
	s := svcWithFloor(t, 0)
	ctx := holdable(t, s)
	if err := s.DeleteModel(ctx, "me", "m", true); err != nil {
		t.Fatalf("floor 0 must permit the delete: %v", err)
	}
}

// TestHoldDoesNotBlockNonDestructiveWork: a hold is not a freeze (§19.3.1). Metadata edits,
// stage transitions and archival all keep working — a held model keeps moving through its
// lifecycle.
func TestHoldDoesNotBlockNonDestructiveWork(t *testing.T) {
	s := newSvc(t)
	ctx := holdable(t, s)
	if _, err := s.SetModelHold(ctx, "counsel@acme.example", "m", "Regulator inquiry"); err != nil {
		t.Fatal(err)
	}
	owner := "risk@acme.example"
	if _, err := s.PatchModel(ctx, "me", "m", core.PatchModelInput{Owner: &owner}); err != nil {
		t.Fatalf("PATCH under a hold: %v", err)
	}
	if _, err := s.Transition(ctx, "me", "m", "1.0.0", domain.StageStaging, ""); err != nil {
		t.Fatalf("transition under a hold: %v", err)
	}
	if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.1.0"}); err != nil {
		t.Fatalf("publish under a hold: %v", err)
	}
	if _, err := s.ArchiveModel(ctx, "me", "m"); err != nil {
		t.Fatalf("archive under a hold: %v", err)
	}
	// And the hold survived all of it.
	got, err := s.GetModel(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	if got.LegalHold == nil {
		t.Fatal("the hold must survive a PATCH")
	}
}

// auditActions maps action -> data for a subject's audit events.
func auditActions(t *testing.T, s *core.Service, ctx context.Context, subjType, subjID string) map[string]string {
	t.Helper()
	evs, _, err := s.ListAudit(ctx, subjType, subjID, domain.ListOptions{PageSize: 100})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	out := map[string]string{}
	for _, e := range evs {
		out[e.Action] = string(e.Data)
	}
	return out
}
