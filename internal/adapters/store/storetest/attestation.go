package storetest

import (
	"context"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// RunAttestation holds every adapter to identical sealing storage semantics (§19.5, §19.6):
// a nil epoch is "not attested" and never sealable, a window is sealed at most once, and the
// chain is readable in order.
//
// It deliberately writes audit rows directly rather than through core, so the epochs are
// fixed numbers and the assertions do not depend on the wall clock.
func RunAttestation(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()

	// Epoch numbers well past anything a real clock produces at a 60s interval, so these rows
	// cannot collide with those any other suite happens to write.
	const (
		e1 int64 = 9_000_001
		e2 int64 = 9_000_002
		e3 int64 = 9_000_003
	)
	epoch := func(v int64) *int64 { return &v }

	mk := func(id string, at int64, ep *int64) *domain.AuditEvent {
		return &domain.AuditEvent{
			ID: id, At: at, Actor: "tester", Action: "model.update",
			SubjectType: domain.SubjectModel, SubjectID: "subject-" + id,
			Summary: "did a thing", Epoch: ep,
		}
	}
	rows := []*domain.AuditEvent{
		mk("01ATT0001", 1, epoch(e1)),
		mk("01ATT0002", 2, epoch(e1)),
		mk("01ATT0003", 3, epoch(e2)),
		// Unattested: written before §19.5, or while attestation was off. It must never be
		// sealed and must never appear in a window (§19.5.4 — enabling later does not
		// backfill, and a backfilled root proves nothing anyway).
		mk("01ATT0004", 4, nil),
	}
	for _, r := range rows {
		if err := store.AppendAudit(ctx, r); err != nil {
			t.Fatalf("AppendAudit: %v", err)
		}
	}

	// ---- The epoch round-trips, and nil stays nil ----

	got, err := store.GetAuditEvent(ctx, "01ATT0001")
	if err != nil {
		t.Fatalf("GetAuditEvent: %v", err)
	}
	if got.Epoch == nil || *got.Epoch != e1 {
		t.Fatalf("epoch round-trip: %v", got.Epoch)
	}
	// 0 is a real epoch, so an unattested row must come back nil rather than zero — otherwise
	// it would read as sealed into the first window of 1970.
	if got, err := store.GetAuditEvent(ctx, "01ATT0004"); err != nil || got.Epoch != nil {
		t.Fatalf("an unattested row must have a nil epoch: %v %v", err, got.Epoch)
	}
	if _, err := store.GetAuditEvent(ctx, "no-such-event"); err == nil {
		t.Fatal("expected not_found for an unknown audit id")
	}

	// ---- Windows ----

	in1, err := store.AuditEventsInEpoch(ctx, e1)
	if err != nil || len(in1) != 2 {
		t.Fatalf("AuditEventsInEpoch(e1): %v len=%d", err, len(in1))
	}
	if in2, err := store.AuditEventsInEpoch(ctx, e2); err != nil || len(in2) != 1 {
		t.Fatalf("AuditEventsInEpoch(e2): %v len=%d", err, len(in2))
	}
	if in3, err := store.AuditEventsInEpoch(ctx, e3); err != nil || len(in3) != 0 {
		t.Fatalf("an empty window must be empty, not an error: %v len=%d", err, len(in3))
	}

	// ---- Sealable ----

	// notAfter excludes the still-open window: e2 is open here, so only e1 is sealable.
	sealable, err := store.SealableEpochs(ctx, e2, 100)
	if err != nil {
		t.Fatalf("SealableEpochs: %v", err)
	}
	if !containsEpoch(sealable, e1) {
		t.Fatalf("e1 must be sealable: %v", sealable)
	}
	if containsEpoch(sealable, e2) {
		t.Fatalf("a window at or past notAfter must not be sealable: %v", sealable)
	}

	// ---- Sealing, and the chain ----

	if _, err := store.LatestSealedEpoch(ctx); err == nil {
		t.Fatal("expected not_found before anything is sealed")
	} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}

	seal1 := &domain.AuditEpoch{Epoch: e1, Root: "sha256:aaa", LeafCount: 2, IntervalMillis: 60_000, SealedAt: 100}
	if err := store.AppendEpoch(ctx, seal1); err != nil {
		t.Fatalf("AppendEpoch: %v", err)
	}
	// Append-only: re-sealing is either a duplicated sealer or a rewrite, and both must fail
	// loudly rather than replace a root someone may already have cited (§19.6.1).
	if err := store.AppendEpoch(ctx, seal1); err == nil {
		t.Fatal("re-sealing a window must be refused")
	} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeAlreadyExists {
		t.Fatalf("expected already_exists, got %v", err)
	}

	// A sealed window drops out of the sealable set.
	if sealable, err := store.SealableEpochs(ctx, e2, 100); err != nil || containsEpoch(sealable, e1) {
		t.Fatalf("a sealed window must not be sealable again: %v %v", err, sealable)
	}

	if err := store.AppendEpoch(ctx, &domain.AuditEpoch{
		Epoch: e2, Root: "sha256:bbb", PrevRoot: "sha256:aaa", LeafCount: 1,
		IntervalMillis: 60_000, SealedAt: 200,
	}); err != nil {
		t.Fatalf("AppendEpoch(e2): %v", err)
	}

	latest, err := store.LatestSealedEpoch(ctx)
	if err != nil || latest.Epoch != e2 || latest.PrevRoot != "sha256:aaa" {
		t.Fatalf("LatestSealedEpoch: %v %+v", err, latest)
	}

	chain, err := store.ListEpochs(ctx, e1, 0)
	if err != nil || len(chain) != 2 {
		t.Fatalf("ListEpochs: %v len=%d", err, len(chain))
	}
	if chain[0].Epoch != e1 || chain[1].Epoch != e2 {
		t.Fatalf("epochs must come back ascending: %d, %d", chain[0].Epoch, chain[1].Epoch)
	}
	// The first epoch in a chain has no predecessor, and "" must not round-trip as a root.
	if chain[0].PrevRoot != "" {
		t.Fatalf("prevRoot on the first seal = %q, want empty", chain[0].PrevRoot)
	}
	if chain[0].LeafCount != 2 || chain[0].IntervalMillis != 60_000 || chain[0].SealedAt != 100 {
		t.Fatalf("seal round-trip: %+v", chain[0])
	}
	if bounded, err := store.ListEpochs(ctx, e1, e1); err != nil || len(bounded) != 1 {
		t.Fatalf("a bounded range must be honoured: %v len=%d", err, len(bounded))
	}

	// ---- attestationStartedAt is derived, not stored ----

	first, ok, err := store.FirstAttestedEpoch(ctx)
	if err != nil || !ok {
		t.Fatalf("FirstAttestedEpoch: %v ok=%v", err, ok)
	}
	// Other suites write audit rows too, and those are unattested — so the earliest attested
	// epoch must be one of ours, never dragged down to zero by a nil.
	if first > e1 {
		t.Fatalf("FirstAttestedEpoch = %d, want <= %d", first, e1)
	}
	if first == 0 {
		t.Fatal("an unattested row must not count as epoch 0")
	}
}

func containsEpoch(list []int64, e int64) bool {
	for _, v := range list {
		if v == e {
			return true
		}
	}
	return false
}
