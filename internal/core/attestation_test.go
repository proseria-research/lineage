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
