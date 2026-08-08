package storetest

import (
	"context"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// RunRetention holds every adapter to identical legal-hold behavior (§19.3): the hold rides
// on the entity, it is set and released through SetHold alone, and DeleteGuardFor resolves
// inheritance in both directions with the more specific hold winning.
//
// It builds its own models rather than reusing Run's, because a hold left behind on a shared
// fixture would silently change what a later suite is allowed to delete.
func RunRetention(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	now := domain.NowMillis()

	m := &domain.Model{ID: domain.NewID(), Name: "hold-subject", State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	v := mkVersion(m.ID, "1.0.0")
	mustCreateVersion(t, store, v)

	// ---- Nothing is held until someone says so ----

	if got, err := store.GetModel(ctx, m.ID); err != nil || got.LegalHold != nil {
		t.Fatalf("a new model must not be held: %v %+v", err, got.LegalHold)
	}
	if g, err := store.DeleteGuardFor(ctx, domain.SubjectModel, m.ID); err != nil || g.Hold != nil {
		t.Fatalf("guard on an unheld model: %v %+v", err, g.Hold)
	}

	// ---- Set, and read it back off the entity ----

	const since int64 = 1_780_000_000_000
	if err := store.SetHold(ctx, domain.SubjectModel, m.ID, &domain.Hold{HeldSince: since, HeldBy: "counsel@acme.example"}); err != nil {
		t.Fatalf("SetHold: %v", err)
	}
	got, err := store.GetModel(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.LegalHold == nil || got.LegalHold.HeldSince != since || got.LegalHold.HeldBy != "counsel@acme.example" {
		t.Fatalf("hold must ride on the entity: %+v", got.LegalHold)
	}

	// A metadata update must not disturb it. UpdateModel takes a whole entity, and that
	// entity has a LegalHold field — so an update built from a copy taken before the hold
	// existed carries nil there. If the write listed the hold columns, that PATCH would
	// silently release someone's hold, which §19.3.1 forbids: a hold clears through
	// hold.release and nothing else.
	//
	// The copy is deliberate. The memory adapter hands back its live pointer, so mutating
	// `got` would edit the store without an Update at all — a dev-adapter aliasing property
	// that is not what this assertion is about.
	stale := *got
	stale.Description = "touched"
	stale.LegalHold = nil
	stale.UpdatedAt = domain.NowMillis()
	if err := store.UpdateModel(ctx, &stale); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	if after, err := store.GetModel(ctx, m.ID); err != nil || after.LegalHold == nil {
		t.Fatalf("UpdateModel must not be able to clear a hold: %v %+v", err, after)
	}

	// ---- Downward inheritance (§19.3.1) ----

	g, err := store.DeleteGuardFor(ctx, domain.SubjectVersion, v.ID)
	if err != nil {
		t.Fatalf("DeleteGuardFor(version): %v", err)
	}
	if g.Hold == nil || g.HeldSubject != "model/hold-subject" {
		t.Fatalf("a version under a held model must be refused, naming the model: %+v", g)
	}
	// The version itself is not marked — inheritance is resolved, never copied onto rows.
	if gotV, err := store.GetVersion(ctx, m.Name, v.Name); err != nil || gotV.LegalHold != nil {
		t.Fatalf("inheritance must not write to the version row: %v %+v", err, gotV.LegalHold)
	}

	// The version's own hold is the more specific statement and wins.
	if err := store.SetHold(ctx, domain.SubjectVersion, v.ID, &domain.Hold{HeldSince: since + 1, HeldBy: "ops@acme.example"}); err != nil {
		t.Fatalf("SetHold(version): %v", err)
	}
	g, err = store.DeleteGuardFor(ctx, domain.SubjectVersion, v.ID)
	if err != nil {
		t.Fatalf("DeleteGuardFor(version): %v", err)
	}
	if g.Hold == nil || g.Hold.HeldBy != "ops@acme.example" || g.HeldSubject != "" {
		t.Fatalf("the version's own hold must win, with no heldSubject: %+v", g)
	}

	// ---- Upward inheritance: the cascade must not destroy a held version ----

	if err := store.SetHold(ctx, domain.SubjectModel, m.ID, nil); err != nil {
		t.Fatalf("release model hold: %v", err)
	}
	g, err = store.DeleteGuardFor(ctx, domain.SubjectModel, m.ID)
	if err != nil {
		t.Fatalf("DeleteGuardFor(model): %v", err)
	}
	if g.Hold == nil || g.HeldSubject != "version/hold-subject@1.0.0" {
		t.Fatalf("deleting a model with a held version must be refused, naming the version: %+v", g)
	}

	// ---- The floor measures the youngest record the delete destroys ----

	// The version was created after the model, so a model delete must be measured against it.
	if g.NewestCreatedAt < v.CreatedAt {
		t.Fatalf("model guard NewestCreatedAt=%d, want >= the version's %d", g.NewestCreatedAt, v.CreatedAt)
	}

	// ---- Release clears the row; history lives in the trail ----

	if err := store.SetHold(ctx, domain.SubjectVersion, v.ID, nil); err != nil {
		t.Fatalf("release version hold: %v", err)
	}
	if gotV, err := store.GetVersion(ctx, m.Name, v.Name); err != nil || gotV.LegalHold != nil {
		t.Fatalf("release must clear the row: %v %+v", err, gotV.LegalHold)
	}
	if g, err := store.DeleteGuardFor(ctx, domain.SubjectModel, m.ID); err != nil || g.Hold != nil {
		t.Fatalf("nothing held after both releases: %v %+v", err, g.Hold)
	}

	// Releasing something that is not held is not the store's problem to report — core makes
	// that call against the subject it already fetched (§19.7.4 not_held).
	if err := store.SetHold(ctx, domain.SubjectModel, m.ID, nil); err != nil {
		t.Fatalf("releasing an unheld subject must be a no-op at this layer: %v", err)
	}

	// ---- Unknown subjects ----

	if err := store.SetHold(ctx, domain.SubjectModel, "no-such-id", &domain.Hold{HeldSince: since}); err == nil {
		t.Fatal("expected not_found holding a model that does not exist")
	} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}
	if _, err := store.DeleteGuardFor(ctx, domain.SubjectVersion, "no-such-id"); err == nil {
		t.Fatal("expected not_found guarding a version that does not exist")
	}
	if err := store.SetHold(ctx, "artifact", "whatever", nil); err == nil {
		t.Fatal("expected invalid_argument for an unholdable subject type")
	} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %v", err)
	}
}
