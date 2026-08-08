package core

import (
	"context"
	"encoding/hex"
	"log"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// Merkle epoch sealing (§19.5) — the write-side epoch stamp, and the background sealer.

// WithAttestation configures sealing. Unset, attestation is **off in the binary** for the
// same reason the retention floor is (see WithRetention): the chart carries §19.4's default,
// which for attestation is on.
//
// The asymmetry with §19.5.2 is deliberate and worth stating. Sealing defaults on because its
// write cost is one derived integer — but a Service constructed in a test or an embedding
// program has no sealer goroutine running, and stamping epochs nothing will ever seal would
// leave every install permanently reporting an unsealed backlog. Enabling it and running the
// sealer are one decision, made once, at the composition root.
func WithAttestation(cfg domain.AttestationConfig) Option {
	return func(s *Service) { s.attest = cfg }
}

// Attestation returns the sealing configuration in force, for /healthz and :verify.
func (s *Service) Attestation() domain.AttestationConfig { return s.attest }

// epochFor stamps an audit row's window from its own timestamp (§19.5.1).
//
// nil when attestation is off, which is what keeps §19.5.4 true: turning it on later starts
// at the current epoch and does not backfill. Rows written while it was off stay unattested
// forever, and honestly so — a root computed over them after the fact would prove nothing,
// since whoever could rewrite them could recompute it.
func (s *Service) epochFor(at int64) *int64 {
	if !s.attest.Enabled {
		return nil
	}
	e := domain.EpochOf(at, s.attest.IntervalMillis())
	return &e
}

// SealResult reports one sealing pass.
type SealResult struct {
	Sealed int
	Leaves int64
}

// maxEpochsPerPass bounds one pass. An install that was down for a week comes back with a
// week of unsealed windows; sealing them all in one transaction-less burst would hold the
// database busy for as long as it takes. Bounded passes catch up over a few intervals
// instead, and nothing is lost by taking longer — an unsealed epoch is still there.
const maxEpochsPerPass = 500

// SealDue seals every closed epoch that has rows and no seal yet (§19.5).
//
// It takes `now` rather than reading the clock so a test can drive it, and it is exported so
// the pass is testable without a goroutine — the ticker in RunSealer does nothing but call
// this.
func (s *Service) SealDue(ctx context.Context, now int64) (SealResult, error) {
	var res SealResult
	if !s.attest.Enabled {
		return res, nil
	}
	interval := s.attest.IntervalMillis()

	// The first window that might still take a commit. Grace pushes the boundary back so a
	// transaction that began inside a window commits before its epoch closes (§19.5.3).
	openFrom := domain.EpochOf(now-s.attest.GraceMillis(), interval)

	epochs, err := s.store.SealableEpochs(ctx, openFrom, maxEpochsPerPass)
	if err != nil {
		return res, err
	}
	for _, epoch := range epochs {
		sealed, err := s.sealEpoch(ctx, epoch, interval, now)
		if err != nil {
			return res, err
		}
		if sealed != nil {
			res.Sealed++
			res.Leaves += sealed.LeafCount
		}
	}
	return res, nil
}

func (s *Service) sealEpoch(ctx context.Context, epoch, interval, now int64) (*domain.AuditEpoch, error) {
	rows, err := s.store.AuditEventsInEpoch(ctx, epoch)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// Nothing to commit to. An empty window is never sealed (§19.6.1) — the chain skips
		// it, which is why prev_root points at the previous *sealed* epoch and not epoch−1.
		return nil, nil
	}

	prev, err := s.store.LatestSealedEpoch(ctx)
	if err != nil && !domain.IsNotFound(err) {
		return nil, err
	}

	var prevRoot string
	if prev != nil {
		prevRoot = prev.Root
		// Changing sealIntervalSeconds re-numbers future epochs, and a *widened* interval
		// produces indices that were sealed long ago — those rows would land in a sealed
		// window and later surface as leaf_count_mismatch, which reads exactly like
		// tampering. Refusing to seal says what actually happened instead, and refusing
		// loses nothing: the rows are still there, and sealing resumes the moment the
		// interval is put back or the operator decides to keep the new one deliberately.
		if prev.IntervalMillis != 0 && prev.IntervalMillis != interval {
			return nil, domain.Precondition(
				"sealIntervalSeconds changed since the last seal; refusing to seal because the new "+
					"window numbering can overlap epochs already sealed",
				map[string]any{
					"reason":         "seal_interval_changed",
					"sealedWithMs":   prev.IntervalMillis,
					"configuredMs":   interval,
					"lastSealedAt":   prev.SealedAt,
					"lastSealsEpoch": prev.Epoch,
				})
		}
		// The sealer works forward. An epoch below the high-water mark means rows arrived in
		// a window already sealed — impossible while grace < interval, so it is a clock jump
		// or a second sealer, and either way sealing over it would produce a root that
		// disagrees with the one already published.
		if epoch <= prev.Epoch {
			return nil, domain.Precondition(
				"epoch is at or below the last sealed epoch; refusing to seal backwards",
				map[string]any{"reason": "epoch_already_passed", "epoch": epoch, "lastSealed": prev.Epoch})
		}
	}

	e := &domain.AuditEpoch{
		Epoch:          epoch,
		Root:           domain.MerkleRootOfEvents(rows),
		PrevRoot:       prevRoot,
		LeafCount:      int64(len(rows)),
		IntervalMillis: interval,
		SealedAt:       now,
	}
	if err := s.store.AppendEpoch(ctx, e); err != nil {
		return nil, err
	}
	return e, nil
}

// RunSealer starts the background sealer (§19.5). It is a no-op when attestation is off.
//
// Sealing happens here rather than on the write path on purpose: that is the entire reason
// epochs exist. A per-row hash chain would have to run inside every audit write, serialized
// behind one sequence; this runs once an interval and touches nothing a writer holds.
func (s *Service) RunSealer(ctx context.Context) {
	if !s.attest.Enabled {
		return
	}
	interval := time.Duration(s.attest.SealIntervalSeconds) * time.Second
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				res, err := s.SealDue(ctx, domain.NowMillis())
				if err != nil {
					// Logged every interval until someone fixes it, which is correct: an audit
					// log that has stopped being sealed is not a condition to mention once.
					log.Printf("attestation: sealing error: %v", err)
					continue
				}
				if res.Sealed > 0 {
					log.Printf("attestation: sealed %d epoch(s), %d event(s)", res.Sealed, res.Leaves)
				}
			}
		}
	}()
}

// ---- Verification (§19.7.2, §19.7.3) ----

// disabled is the shared refusal for both read endpoints. Answering "ok: true" with
// attestation off would be the worst possible lie: nothing is sealed, so nothing disagrees.
func (s *Service) attestationEnabled() *domain.Error {
	if !s.attest.Enabled {
		return domain.Precondition("audit attestation is disabled on this install",
			map[string]any{"reason": "attestation_disabled"})
	}
	return nil
}

// VerifyAudit recomputes every sealed epoch's root from its rows and checks the chain
// (§19.7.2). from/to bound the scan; a zero `to` means unbounded.
func (s *Service) VerifyAudit(ctx context.Context, from, to int64) (domain.VerifyResult, error) {
	var res domain.VerifyResult
	if err := s.attestationEnabled(); err != nil {
		return res, err
	}
	interval := s.attest.IntervalMillis()

	if first, ok, err := s.store.FirstAttestedEpoch(ctx); err != nil {
		return res, err
	} else if ok {
		res.AttestationStartedAt = first * interval
	}
	res.OpenEpochSince = domain.EpochOf(domain.NowMillis(), interval) * interval

	epochs, err := s.store.ListEpochs(ctx, from, to)
	if err != nil {
		return res, err
	}

	var prev *domain.AuditEpoch
	for i, e := range epochs {
		rows, err := s.store.AuditEventsInEpoch(ctx, e.Epoch)
		if err != nil {
			return res, err
		}
		res.EpochsChecked++
		res.LeavesChecked += int64(len(rows))

		// Leaf count first: a deletion changes both the count and the root, and "17 sealed,
		// 16 present" is a sharper diagnosis than "the root differs".
		if int64(len(rows)) != e.LeafCount {
			res.FirstBreak = &domain.VerifyBreak{Epoch: e.Epoch, Kind: domain.BreakLeafCountMismatch,
				Expected: e.LeafCount, Found: int64(len(rows)), SealedAt: e.SealedAt}
			return res, nil
		}
		if root := domain.MerkleRootOfEvents(rows); root != e.Root {
			res.FirstBreak = &domain.VerifyBreak{Epoch: e.Epoch, Kind: domain.BreakRootMismatch,
				Expected: e.Root, Found: root, SealedAt: e.SealedAt}
			return res, nil
		}

		switch {
		case prev != nil:
			if e.PrevRoot != prev.Root {
				res.FirstBreak = &domain.VerifyBreak{Epoch: e.Epoch, Kind: domain.BreakPrevRootMismatch,
					Expected: prev.Root, Found: e.PrevRoot, SealedAt: e.SealedAt}
				return res, nil
			}
		case i == 0 && from <= 0 && e.PrevRoot != "":
			// A full scan starts at the genesis seal, which by construction has no
			// predecessor. A prev_root here means the epoch it pointed at is gone — the
			// "removed wholesale" case, and the only way to catch a removal at the *head* of
			// the chain, where there is no later epoch to disagree with it.
			//
			// Skipped on a bounded scan, where the predecessor is legitimately out of range.
			res.FirstBreak = &domain.VerifyBreak{Epoch: e.Epoch, Kind: domain.BreakPrevRootMismatch,
				Expected: "", Found: e.PrevRoot, SealedAt: e.SealedAt}
			return res, nil
		}
		prev = e
	}

	res.OK = true
	return res, nil
}

// ProveAudit returns an inclusion proof for one event (§19.7.3).
func (s *Service) ProveAudit(ctx context.Context, id string) (*domain.InclusionProof, error) {
	if err := s.attestationEnabled(); err != nil {
		return nil, err
	}
	e, err := s.store.GetAuditEvent(ctx, id)
	if err != nil {
		return nil, err
	}
	if e.Epoch == nil {
		// Written before §19.5, or while attestation was off. There is no root that covers
		// it and there never will be (§19.5.4), so this is a permanent answer, not a wait.
		return nil, domain.Precondition("this event predates attestation and is not covered by any root",
			map[string]any{"reason": "not_attested"})
	}
	interval := s.attest.IntervalMillis()

	epochs, err := s.store.ListEpochs(ctx, *e.Epoch, *e.Epoch)
	if err != nil {
		return nil, err
	}
	if len(epochs) == 0 {
		// The open epoch is reported, not glossed (§19.5.3): the caller is told when the
		// proof becomes available rather than handed a 404 they cannot interpret.
		return nil, domain.Precondition("the epoch containing this event has not been sealed yet",
			map[string]any{
				"reason":  "epoch_unsealed",
				"epoch":   *e.Epoch,
				"sealsAt": (*e.Epoch+1)*interval + s.attest.GraceMillis(),
			})
	}
	sealed := epochs[0]

	rows, err := s.store.AuditEventsInEpoch(ctx, *e.Epoch)
	if err != nil {
		return nil, err
	}
	path, index, found := domain.MerkleProofForEvents(rows, id)
	if !found {
		return nil, domain.Internal("audit event is not present in its own epoch")
	}
	return &domain.InclusionProof{
		ID: id, Epoch: *e.Epoch,
		LeafHash:  "sha256:" + hex.EncodeToString(domain.AuditLeafHash(e)),
		LeafIndex: index, LeafCount: len(rows),
		Path: path, Root: sealed.Root, SealedAt: sealed.SealedAt,
	}, nil
}
