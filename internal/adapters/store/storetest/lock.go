package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// RunArtifactLock holds every adapter to the artifact-lock rule (§00.11.19): locked_at is set
// by SetStage on the first entry into staging or production, kept on every later move, and
// never set by a path that skips both. It builds its own model so it can run after the others.
func RunArtifactLock(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	now := domain.NowMillis()
	m := &domain.Model{ID: domain.NewID(), Name: "lock-model", State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	mkv := func(name string) *domain.ModelVersion {
		v := &domain.ModelVersion{ID: domain.NewID(), ModelID: m.ID, Name: name, Stage: domain.StageDraft, CreatedAt: now, UpdatedAt: now}
		mustCreateVersion(t, store, v)
		return v
	}
	get := func(id string) *domain.ModelVersion {
		t.Helper()
		v, err := store.GetVersionByID(ctx, id)
		if err != nil {
			t.Fatalf("GetVersionByID: %v", err)
		}
		return v
	}
	move := func(id string, to domain.Stage) {
		t.Helper()
		// A millisecond apart, so an overwrite of locked_at would show as a changed value.
		time.Sleep(2 * time.Millisecond)
		if err := store.SetStage(ctx, id, to, domain.IsSingleton(to)); err != nil {
			t.Fatalf("SetStage %s: %v", to, err)
		}
	}

	t.Run("first staging entry locks, nothing unlocks", func(t *testing.T) {
		v := mkv("1.0.0")
		if got := get(v.ID); got.LockedAt != 0 {
			t.Fatalf("a new draft is locked: %d", got.LockedAt)
		}
		move(v.ID, domain.StageStaging)
		locked := get(v.ID).LockedAt
		if locked == 0 {
			t.Fatal("entering staging did not lock")
		}
		// An update does not touch it.
		u := get(v.ID)
		u.Description = "edited"
		u.LockedAt = 0
		if err := store.UpdateVersion(ctx, u); err != nil {
			t.Fatalf("UpdateVersion: %v", err)
		}
		for _, to := range []domain.Stage{domain.StageDraft, domain.StageStaging, domain.StageProduction, domain.StageArchived, domain.StageDraft} {
			move(v.ID, to)
			if got := get(v.ID); got.LockedAt != locked {
				t.Fatalf("after →%s locked_at = %d, want %d", to, got.LockedAt, locked)
			}
		}
	})

	t.Run("draft to archived never locks", func(t *testing.T) {
		v := mkv("2.0.0")
		move(v.ID, domain.StageArchived)
		move(v.ID, domain.StageDraft)
		if got := get(v.ID); got.LockedAt != 0 {
			t.Fatalf("a version that never reached staging is locked: %d", got.LockedAt)
		}
	})

	t.Run("a demoted production version stays locked", func(t *testing.T) {
		a, b := mkv("3.0.0"), mkv("4.0.0")
		move(a.ID, domain.StageProduction)
		locked := get(a.ID).LockedAt
		if locked == 0 {
			t.Fatal("entering production did not lock")
		}
		move(b.ID, domain.StageProduction) // demotes a to archived
		if got := get(a.ID); got.Stage != domain.StageArchived || got.LockedAt != locked {
			t.Fatalf("demoted: stage=%s locked_at=%d want %d", got.Stage, got.LockedAt, locked)
		}
	})
}

// A promotion into staging must wait for an artifact write already holding the version
// (LockVersionForArtifacts inside InTx), so the write cannot land on a version that locked
// underneath it (§00.11.19). Postgres serializes on FOR SHARE vs the promotion's UPDATE;
// SQLite on its single writer; memory on InTx's write lock. Checked by timing: the promotion
// must not finish while the unit is open, and must finish once it commits.
func RunArtifactLockSerializes(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	now := domain.NowMillis()
	m := &domain.Model{ID: domain.NewID(), Name: "lock-race-model", State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	v := &domain.ModelVersion{ID: domain.NewID(), ModelID: m.ID, Name: "1.0.0", Stage: domain.StageDraft, CreatedAt: now, UpdatedAt: now}
	mustCreateVersion(t, store, v)

	holding := make(chan struct{})
	release := make(chan struct{})
	unit := make(chan error, 1)
	go func() {
		unit <- store.InTx(ctx, func(tx domain.MetadataStore) error {
			got, err := tx.LockVersionForArtifacts(ctx, v.ID)
			if err != nil {
				return err
			}
			if got.Locked() {
				t.Errorf("version locked before the promotion ran")
			}
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	promoted := make(chan error, 1)
	go func() { promoted <- store.SetStage(ctx, v.ID, domain.StageStaging, false) }()

	select {
	case err := <-promoted:
		close(release)
		<-unit
		t.Fatalf("promotion finished while an artifact write held the version (err=%v)", err)
	case <-time.After(250 * time.Millisecond):
	}
	close(release)
	if err := <-unit; err != nil {
		t.Fatalf("artifact unit: %v", err)
	}
	select {
	case err := <-promoted:
		if err != nil {
			t.Fatalf("promotion after the unit committed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("promotion never completed after the artifact unit committed")
	}
	after, err := store.GetVersionByID(ctx, v.ID)
	if err != nil || !after.Locked() {
		t.Fatalf("version not locked after the promotion: %v", err)
	}
}
