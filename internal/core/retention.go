package core

import (
	"context"
	"encoding/json"

	"github.com/proseria-research/lineage/internal/domain"
)

// Legal hold and the retention floor (§19.3, §19.4) — the write side, and the guard the two
// delete paths share.

// WithRetention configures the floor (§19.4). Left unset, both floors are **0 — disabled**.
//
// §19.4's 3650 is the *chart's* default, not the binary's, and the difference is deliberate.
// A bare `go run` is a dev install, and one that refuses to delete anything created in the
// last decade is unusable. A production install arrives through Helm (axiom 2), which passes
// the floor explicitly. `/healthz` echoes whatever is actually in force, so no one has to
// infer which of the two they got.
//
// The value is validated where config is parsed, so a bad floor fails at startup rather than
// at the first delete.
func WithRetention(cfg domain.RetentionConfig) Option {
	return func(s *Service) { s.retention = cfg }
}

// Retention returns the floor in force, for /healthz and for anything that has to cite the
// value the registry actually ran under (§19.4).
func (s *Service) Retention() domain.RetentionConfig { return s.retention }

// ---- Holds ----

// SetModelHold places a legal hold on a model (§19.3). reason is recorded on the audit event,
// never on the row: the row carries current state, the trail carries why.
func (s *Service) SetModelHold(ctx context.Context, actor, name, reason string) (*domain.Model, error) {
	m, err := s.store.GetModel(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := s.setHold(ctx, actor, domain.SubjectModel, m.ID, "model "+m.Name, m.LegalHold, reason, true); err != nil {
		return nil, err
	}
	return s.store.GetModel(ctx, name)
}

// ReleaseModelHold clears a model's hold. Explicit and separately audited: §19.3.1 makes
// clearing a hold the event an auditor cares about, so it is never a side effect.
func (s *Service) ReleaseModelHold(ctx context.Context, actor, name, reason string) (*domain.Model, error) {
	m, err := s.store.GetModel(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := s.setHold(ctx, actor, domain.SubjectModel, m.ID, "model "+m.Name, m.LegalHold, reason, false); err != nil {
		return nil, err
	}
	return s.store.GetModel(ctx, name)
}

func (s *Service) SetVersionHold(ctx context.Context, actor, model, version, reason string) (*domain.ModelVersion, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if err := s.setHold(ctx, actor, domain.SubjectVersion, v.ID, model+"@"+version, v.LegalHold, reason, true); err != nil {
		return nil, err
	}
	return s.store.GetVersion(ctx, model, version)
}

func (s *Service) ReleaseVersionHold(ctx context.Context, actor, model, version, reason string) (*domain.ModelVersion, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if err := s.setHold(ctx, actor, domain.SubjectVersion, v.ID, model+"@"+version, v.LegalHold, reason, false); err != nil {
		return nil, err
	}
	return s.store.GetVersion(ctx, model, version)
}

// setHold is the one implementation behind all four verbs.
//
// A reason is required in both directions. It is one string, the event is permanent, and a
// hold with no recorded matter — or a release with no recorded reason to release — is the
// exact record an auditor will ask about and nobody will be able to reconstruct.
func (s *Service) setHold(ctx context.Context, actor, subjectType, subjectID, label string, cur *domain.Hold, reason string, hold bool) error {
	if reason == "" {
		return domain.Invalid("reason is required: it is the only record of why the hold was placed or lifted")
	}

	// Neither direction is a silent no-op.
	//
	// Re-holding could plausibly refresh heldSince, and that is precisely why it must not: the
	// date a hold was placed is evidence, and quietly moving it forward would rewrite it.
	// §19.7.4 tables not_held for the release side; already_held is its mirror, and both are
	// details.reason on the same failed_precondition code, so the §03.9 vocabulary is unchanged.
	if hold && cur != nil {
		return domain.Precondition(label+" is already under legal hold",
			map[string]any{"reason": "already_held", "heldSince": cur.HeldSince, "heldBy": cur.HeldBy})
	}
	if !hold && cur == nil {
		return domain.Precondition(label+" is not under legal hold",
			map[string]any{"reason": domain.RefusedNotHeld})
	}

	var h *domain.Hold
	action, summary := "hold.release", "released legal hold on "+label
	if hold {
		h = &domain.Hold{HeldSince: domain.NowMillis(), HeldBy: actor}
		action, summary = "hold.set", "placed legal hold on "+label
	}
	if err := s.store.SetHold(ctx, subjectType, subjectID, h); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"reason": reason})
	s.audit(ctx, actor, action, subjectType, subjectID, summary, data)
	return nil
}

// ---- The delete guard ----

// guardDelete refuses a deletion that would destroy held or retained evidence (§19.3.1).
//
// It runs **before** the ?force=true production check, and takes no force parameter. Those
// two facts are the same decision seen twice: force exists to let an operator override a
// guard that protects them from their own mistake, and a hold exists to protect evidence from
// the operator. Checking the hold first also keeps the refusal honest — surfacing "retry with
// ?force=true" when the real blocker is a legal hold would be worse than not answering.
func (s *Service) guardDelete(ctx context.Context, subjectType, subjectID string) error {
	g, err := s.store.DeleteGuardFor(ctx, subjectType, subjectID)
	if err != nil {
		return err
	}
	if e := domain.CheckDeletable(g, s.retention, domain.NowMillis()); e != nil {
		return e
	}
	return nil
}
