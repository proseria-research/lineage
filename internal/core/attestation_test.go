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

// sealingSvc returns a service with attestation on, and the store beneath it so a test can
// inspect and tamper with rows the way an attacker with database access would.
func sealingSvc(t *testing.T, cfg domain.AttestationConfig) (*core.Service, *memstore.Store) {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	store := memstore.New()
	svc := core.New(store,
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New(), core.WithAttestation(cfg))
	return svc, store
}

// on is the shipped configuration (§19.4).
func on() domain.AttestationConfig { return domain.DefaultAttestation }

// makeAudit produces one audited change and returns the resulting event.
func makeAudit(t *testing.T, s *core.Service, ctx context.Context, name string) *domain.AuditEvent {
	t.Helper()
	m, err := s.CreateModel(ctx, "tester", core.CreateModelInput{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	evs, _, err := s.ListAudit(ctx, "model", m.ID, domain.ListOptions{PageSize: 10})
	if err != nil || len(evs) == 0 {
		t.Fatalf("ListAudit: %v len=%d", err, len(evs))
	}
	return evs[0]
}

func TestEpochIsStampedOnWrite(t *testing.T) {
	s, _ := sealingSvc(t, on())
	ctx := context.Background()
	e := makeAudit(t, s, ctx, "m")
	if e.Epoch == nil {
		t.Fatal("an audit row written with attestation on must carry an epoch")
	}
	if want := domain.EpochOf(e.At, on().IntervalMillis()); *e.Epoch != want {
		t.Fatalf("epoch = %d, want %d — it must derive from the row's own clock", *e.Epoch, want)
	}
}

// TestNoBackfill is §19.5.4: enabling attestation later starts at the current epoch. A
// backfilled root proves nothing, because whoever could rewrite history could recompute it.
func TestNoBackfill(t *testing.T) {
	off, store := sealingSvc(t, domain.AttestationConfig{Enabled: false})
	ctx := context.Background()
	e := makeAudit(t, off, ctx, "written-while-off")
	if e.Epoch != nil {
		t.Fatalf("attestation off must leave the row unattested, got epoch %d", *e.Epoch)
	}

	// Restart with attestation on, over the same store.
	backend := fs.New("default", t.TempDir())
	svc := core.New(store, map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New(), core.WithAttestation(on()))

	res, err := svc.SealDue(ctx, domain.NowMillis()+10*on().IntervalMillis())
	if err != nil {
		t.Fatalf("SealDue: %v", err)
	}
	if res.Sealed != 0 {
		t.Fatalf("sealed %d epochs over rows written while attestation was off", res.Sealed)
	}
	// The pre-existing row stays unattested forever, and honestly so.
	if got, err := store.GetAuditEvent(ctx, e.ID); err != nil || got.Epoch != nil {
		t.Fatalf("a row written while off must never gain an epoch: %v %v", err, got.Epoch)
	}
}

func TestSealDueSealsClosedWindowsOnly(t *testing.T) {
	s, store := sealingSvc(t, on())
	ctx := context.Background()
	e := makeAudit(t, s, ctx, "m")
	now := e.At

	// The row's own window is still open, and grace holds the boundary back further.
	if res, err := s.SealDue(ctx, now); err != nil || res.Sealed != 0 {
		t.Fatalf("the open window must not be sealed: %v %+v", err, res)
	}
	// Inside the grace period after the window closed: still not sealed, so a transaction
	// that began in the window can commit (§19.5.3).
	justClosed := (*e.Epoch+1)*on().IntervalMillis() + 1
	if res, err := s.SealDue(ctx, justClosed); err != nil || res.Sealed != 0 {
		t.Fatalf("grace must hold the boundary back: %v %+v", err, res)
	}
	// Past grace: sealed.
	res, err := s.SealDue(ctx, justClosed+on().GraceMillis())
	if err != nil || res.Sealed != 1 || res.Leaves != 1 {
		t.Fatalf("SealDue: %v %+v", err, res)
	}

	sealed, err := store.LatestSealedEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Epoch != *e.Epoch || sealed.LeafCount != 1 {
		t.Fatalf("seal = %+v, want epoch %d with 1 leaf", sealed, *e.Epoch)
	}
	if sealed.Root != domain.MerkleRootOfEvents([]*domain.AuditEvent{e}) {
		t.Fatalf("root does not match a recompute over the same row")
	}
	if sealed.PrevRoot != "" {
		t.Fatalf("the first seal has no predecessor, got %q", sealed.PrevRoot)
	}
	if sealed.IntervalMillis != on().IntervalMillis() {
		t.Fatalf("intervalMillis = %d", sealed.IntervalMillis)
	}

	// Idempotent: a second pass finds nothing left to do rather than re-sealing.
	if res, err := s.SealDue(ctx, justClosed+on().GraceMillis()); err != nil || res.Sealed != 0 {
		t.Fatalf("a second pass must be a no-op: %v %+v", err, res)
	}
}

// TestChainSkipsEmptyWindows: prev_root points at the previous *sealed* epoch, not epoch−1.
// Empty windows are never sealed, so a gap in epoch numbers is normal — what proves nothing
// was removed is that the chain links (§19.6.1).
func TestChainSkipsEmptyWindows(t *testing.T) {
	s, store := sealingSvc(t, on())
	ctx := context.Background()

	first := makeAudit(t, s, ctx, "m1")
	// Seal the first window before writing again, so the second row lands in a later epoch
	// with quiet windows in between.
	firstClosed := (*first.Epoch+1)*on().IntervalMillis() + on().GraceMillis() + 1
	if _, err := s.SealDue(ctx, firstClosed); err != nil {
		t.Fatal(err)
	}
	firstSeal, err := store.LatestSealedEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}

	second := makeAudit(t, s, ctx, "m2")
	// Force the second row into a much later window, the way a quiet registry would.
	later := *second.Epoch + 5
	if err := forceEpoch(store, second.ID, later); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SealDue(ctx, (later+1)*on().IntervalMillis()+on().GraceMillis()+1); err != nil {
		t.Fatal(err)
	}

	chain, err := store.ListEpochs(ctx, 0, 0)
	if err != nil || len(chain) != 2 {
		t.Fatalf("ListEpochs: %v len=%d", err, len(chain))
	}
	if chain[1].Epoch != later {
		t.Fatalf("second seal at epoch %d, want %d", chain[1].Epoch, later)
	}
	if chain[1].PrevRoot != firstSeal.Root {
		t.Fatalf("prevRoot must chain to the previous *sealed* epoch across the gap: %q vs %q",
			chain[1].PrevRoot, firstSeal.Root)
	}
}

// TestSealIntervalChangeRefuses: widening the interval re-numbers epochs downward, so a new
// row can land in a window sealed long ago. That would later surface as leaf_count_mismatch,
// which reads exactly like tampering. Refusing says what actually happened.
func TestSealIntervalChangeRefuses(t *testing.T) {
	s, store := sealingSvc(t, on())
	ctx := context.Background()
	e := makeAudit(t, s, ctx, "m")
	closed := (*e.Epoch+1)*on().IntervalMillis() + on().GraceMillis() + 1
	if _, err := s.SealDue(ctx, closed); err != nil {
		t.Fatal(err)
	}

	// Same store, a different interval.
	backend := fs.New("default", t.TempDir())
	wide := domain.AttestationConfig{Enabled: true, SealIntervalSeconds: 3600, SealGraceSeconds: 5}
	svc2 := core.New(store, map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New(), core.WithAttestation(wide))
	makeAudit(t, svc2, ctx, "m2")

	_, err := svc2.SealDue(ctx, domain.NowMillis()+2*wide.IntervalMillis())
	de, ok := err.(*domain.Error)
	if !ok || de.Code != domain.CodeFailedPrecondition {
		t.Fatalf("changing the interval must refuse, got %v", err)
	}
	if de.Details["reason"] != "seal_interval_changed" {
		t.Fatalf("reason = %v", de.Details["reason"])
	}
}

func TestSealingOffIsANoOp(t *testing.T) {
	s, store := sealingSvc(t, domain.AttestationConfig{Enabled: false})
	ctx := context.Background()
	makeAudit(t, s, ctx, "m")
	if res, err := s.SealDue(ctx, domain.NowMillis()+1_000_000); err != nil || res.Sealed != 0 {
		t.Fatalf("SealDue with attestation off: %v %+v", err, res)
	}
	if _, err := store.LatestSealedEpoch(ctx); err == nil {
		t.Fatal("nothing should have been sealed")
	}
}

// forceEpoch rewrites a stored row's epoch. It exists only to place rows in specific windows
// without sleeping through real intervals — the tamper tests in the verify step use the same
// door deliberately.
func forceEpoch(store *memstore.Store, id string, epoch int64) error {
	return store.TamperAuditEpochForTest(id, epoch)
}

// ---- Verification ----

// sealedThree builds three sealed epochs, one row each, and returns the events in order.
func sealedThree(t *testing.T) (*core.Service, *memstore.Store, []*domain.AuditEvent) {
	t.Helper()
	s, store := sealingSvc(t, on())
	ctx := context.Background()
	var evs []*domain.AuditEvent
	for i, name := range []string{"m1", "m2", "m3"} {
		e := makeAudit(t, s, ctx, name)
		// Place each row in its own window without sleeping through real intervals.
		ep := *e.Epoch + int64(i)*3
		if err := store.TamperAuditEpochForTest(e.ID, ep); err != nil {
			t.Fatal(err)
		}
		e.Epoch = &ep
		evs = append(evs, e)
		if _, err := s.SealDue(ctx, (ep+1)*on().IntervalMillis()+on().GraceMillis()+1); err != nil {
			t.Fatalf("SealDue: %v", err)
		}
	}
	if chain, err := store.ListEpochs(ctx, 0, 0); err != nil || len(chain) != 3 {
		t.Fatalf("expected three seals: %v len=%d", err, len(chain))
	}
	return s, store, evs
}

func TestVerifyCleanLog(t *testing.T) {
	s, _, _ := sealedThree(t)
	res, err := s.VerifyAudit(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("VerifyAudit: %v", err)
	}
	if !res.OK || res.FirstBreak != nil {
		t.Fatalf("a clean log must verify: %+v", res)
	}
	if res.EpochsChecked != 3 || res.LeavesChecked != 3 {
		t.Fatalf("checked %d epochs / %d leaves, want 3/3", res.EpochsChecked, res.LeavesChecked)
	}
	// The report states its own limits: what is covered, and what is not yet (§19.5.3).
	if res.AttestationStartedAt == 0 {
		t.Fatal("attestationStartedAt must be reported")
	}
	if res.OpenEpochSince == 0 {
		t.Fatal("openEpochSince must be reported — the unsealed window is explicit, never glossed")
	}
}

// TestVerifyDetectsAnEdit is the §19.5.2 claim: a row edited inside a sealed epoch is caught.
// It edits `summary`, which §19.5.1's field list would have left uncovered.
func TestVerifyDetectsAnEdit(t *testing.T) {
	s, store, evs := sealedThree(t)
	if err := store.TamperAuditSummaryForTest(evs[1].ID, "nothing to see here"); err != nil {
		t.Fatal(err)
	}
	res, err := s.VerifyAudit(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.FirstBreak == nil {
		t.Fatalf("an edited row must break verification: %+v", res)
	}
	if res.FirstBreak.Kind != domain.BreakRootMismatch {
		t.Fatalf("kind = %q, want %q", res.FirstBreak.Kind, domain.BreakRootMismatch)
	}
	if res.FirstBreak.Epoch != *evs[1].Epoch {
		t.Fatalf("break at epoch %d, want %d", res.FirstBreak.Epoch, *evs[1].Epoch)
	}
}

func TestVerifyDetectsADeletion(t *testing.T) {
	s, store, evs := sealedThree(t)
	if err := store.TamperDeleteAuditForTest(evs[2].ID); err != nil {
		t.Fatal(err)
	}
	res, err := s.VerifyAudit(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Leaf count is reported rather than the root, though both changed: "17 sealed, 16
	// present" is the sharper diagnosis.
	if res.OK || res.FirstBreak.Kind != domain.BreakLeafCountMismatch {
		t.Fatalf("a deleted row must report leaf_count_mismatch: %+v", res.FirstBreak)
	}
	if res.FirstBreak.Expected != int64(1) || res.FirstBreak.Found != int64(0) {
		t.Fatalf("expected/found = %v/%v", res.FirstBreak.Expected, res.FirstBreak.Found)
	}
}

func TestVerifyDetectsAWholeEpochRemoved(t *testing.T) {
	s, store, evs := sealedThree(t)
	ctx := context.Background()
	// Remove the middle seal *and* its rows, the way someone hiding a window would. Without
	// the chain this is invisible: every remaining epoch still verifies on its own.
	if err := store.TamperDeleteAuditForTest(evs[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.TamperDeleteEpochForTest(*evs[1].Epoch); err != nil {
		t.Fatal(err)
	}
	res, err := s.VerifyAudit(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.FirstBreak.Kind != domain.BreakPrevRootMismatch {
		t.Fatalf("a removed epoch must break the chain: %+v", res.FirstBreak)
	}
	if res.FirstBreak.Epoch != *evs[2].Epoch {
		t.Fatalf("break reported at epoch %d, want the successor %d", res.FirstBreak.Epoch, *evs[2].Epoch)
	}
}

// TestVerifyDetectsRemovalAtTheHead covers the case the chain alone cannot: deleting the
// *first* seal leaves no earlier epoch to disagree with. A full scan starts at the genesis
// seal, which by construction has no predecessor, so a prev_root there is the evidence.
func TestVerifyDetectsRemovalAtTheHead(t *testing.T) {
	s, store, evs := sealedThree(t)
	if err := store.TamperDeleteAuditForTest(evs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.TamperDeleteEpochForTest(*evs[0].Epoch); err != nil {
		t.Fatal(err)
	}
	res, err := s.VerifyAudit(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.FirstBreak.Kind != domain.BreakPrevRootMismatch {
		t.Fatalf("removing the genesis seal must be detected: %+v", res.FirstBreak)
	}

	// But a *bounded* scan must not report it: the predecessor is legitimately out of range,
	// and crying tamper on a range query would make the endpoint useless.
	bounded, err := s.VerifyAudit(context.Background(), *evs[1].Epoch, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bounded.OK {
		t.Fatalf("a bounded scan must not treat its own lower bound as a removal: %+v", bounded.FirstBreak)
	}
}

func TestVerifyRefusedWhenDisabled(t *testing.T) {
	s, _ := sealingSvc(t, domain.AttestationConfig{Enabled: false})
	_, err := s.VerifyAudit(context.Background(), 0, 0)
	de, ok := err.(*domain.Error)
	if !ok || de.Details["reason"] != "attestation_disabled" {
		t.Fatalf("verify with attestation off must refuse, got %v", err)
	}
}

// ---- Inclusion proofs ----

func TestProofVerifiesAgainstTheSealedRoot(t *testing.T) {
	s, store := sealingSvc(t, on())
	ctx := context.Background()
	// Several rows in one window, so the proof has real siblings to carry.
	var ids []string
	var epoch int64
	for i, name := range []string{"p1", "p2", "p3", "p4", "p5"} {
		e := makeAudit(t, s, ctx, name)
		if i == 0 {
			epoch = *e.Epoch
		}
		if err := store.TamperAuditEpochForTest(e.ID, epoch); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	if _, err := s.SealDue(ctx, (epoch+1)*on().IntervalMillis()+on().GraceMillis()+1); err != nil {
		t.Fatal(err)
	}

	for _, id := range ids {
		p, err := s.ProveAudit(ctx, id)
		if err != nil {
			t.Fatalf("ProveAudit(%s): %v", id, err)
		}
		if p.LeafCount != 5 {
			t.Fatalf("leafCount = %d, want 5", p.LeafCount)
		}
		// The check a third party would run, against the same primitive they would use.
		row, err := store.GetAuditEvent(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !domain.VerifyMerkleProof(domain.AuditLeafHash(row), p.Path, p.LeafIndex, p.LeafCount, p.Root) {
			t.Fatalf("proof for %s did not verify against the sealed root", id)
		}
	}
}

// TestProofInTheOpenEpoch: the unsealed window is reported, not glossed (§19.5.3). The caller
// is told when the proof becomes available rather than handed a bare failure.
func TestProofInTheOpenEpoch(t *testing.T) {
	s, _ := sealingSvc(t, on())
	ctx := context.Background()
	e := makeAudit(t, s, ctx, "m")

	_, err := s.ProveAudit(ctx, e.ID)
	de, ok := err.(*domain.Error)
	if !ok || de.Code != domain.CodeFailedPrecondition {
		t.Fatalf("want failed_precondition, got %v", err)
	}
	if de.Details["reason"] != "epoch_unsealed" {
		t.Fatalf("reason = %v", de.Details["reason"])
	}
	sealsAt, _ := de.Details["sealsAt"].(int64)
	if sealsAt <= e.At {
		t.Fatalf("sealsAt = %d must be after the event at %d", sealsAt, e.At)
	}
}

// TestProofForAnUnattestedRow: no root covers it and none ever will (§19.5.4), so this is a
// permanent answer rather than "come back later".
func TestProofForAnUnattestedRow(t *testing.T) {
	off, store := sealingSvc(t, domain.AttestationConfig{Enabled: false})
	ctx := context.Background()
	e := makeAudit(t, off, ctx, "old")

	backend := fs.New("default", t.TempDir())
	svc := core.New(store, map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New(), core.WithAttestation(on()))

	_, err := svc.ProveAudit(ctx, e.ID)
	de, ok := err.(*domain.Error)
	if !ok || de.Details["reason"] != "not_attested" {
		t.Fatalf("want not_attested, got %v", err)
	}
}
