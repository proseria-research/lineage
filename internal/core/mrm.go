package core

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/proseria-research/lineage/internal/domain"
)

// Model risk management (§20). The tier rides the §16 classification path with `mrm` in the
// regime slot; this file adds the validation record and the §20.7 state.
//
// It records and flags. Nothing here refuses a publish or a promotion over a stale or missing
// validation, and a self-validation is flagged, never refused (§20.1, §20.6).

// ValidationInput is the client-settable half of a validation.
//
// There is no field for ValidatedBy or ValidatedAt: §20.9.1 makes both server-set, and the
// shared decoder rejects unknown fields, so a client sending either gets 400 — the ReviewInput
// arrangement, for the same reason.
type ValidationInput struct {
	Outcome            domain.ValidationOutcome `json:"outcome"`
	Scope              string                   `json:"scope"`
	Findings           string                   `json:"findings"`
	Conditions         string                   `json:"conditions"`
	ValidUntil         *int64                   `json:"validUntil"`
	EvidenceArtifactID string                   `json:"evidenceArtifactId"`
}

// evidenceKinds are the artifact kinds a validation report may be (§20.8.2). METRICS is the
// kind §02 reserves; the artifact table does not enforce kinds, so it can already exist.
var evidenceKinds = map[domain.ArtifactKind]bool{domain.KindDoc: true, "METRICS": true}

// RecordValidation appends one validation of one version (§20.9.1).
//
// It does not require the model to be tiered, for the RecordReview reason: refusing to record
// a validator's judgement because nobody filled in a tier would lose the judgement, which is
// the one thing here that cannot be recomputed.
func (s *Service) RecordValidation(ctx context.Context, actor, model, version string, in ValidationInput) (*domain.ValidationView, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if !domain.ValidValidationOutcome(in.Outcome) {
		e := domain.Invalid("unknown outcome")
		e.Details = map[string]any{"field": "outcome", "allowedValues": domain.ValidationOutcomes()}
		return nil, e
	}
	// A conditional approval without its conditions is an approval with the part that makes
	// it true missing (§20.5).
	if in.Outcome == domain.ValidationConditional && strings.TrimSpace(in.Conditions) == "" {
		e := domain.Unprocessable("conditions is required when outcome is conditional")
		e.Details = map[string]any{"field": "conditions"}
		return nil, e
	}
	now := domain.NowMillis()
	if in.ValidUntil != nil && *in.ValidUntil <= now {
		return nil, invalidField("validUntil must be in the future", "validUntil")
	}
	if in.EvidenceArtifactID != "" {
		if err := s.checkEvidence(ctx, v.ID, in.EvidenceArtifactID); err != nil {
			return nil, err
		}
	}

	val := &domain.Validation{
		ID: domain.NewID(), VersionID: v.ID, Outcome: in.Outcome,
		Scope: in.Scope, Findings: in.Findings, Conditions: in.Conditions,
		ValidUntil: in.ValidUntil, EvidenceArtifactID: in.EvidenceArtifactID,
		// Server-set: the value of either is that the registry witnessed it (§20.9.1).
		ValidatedBy: actor, ValidatedAt: now,
	}
	view := domain.NewValidationView(val, v.Author)
	data, _ := json.Marshal(map[string]any{
		"validationId":          val.ID,
		"outcome":               string(val.Outcome),
		"validUntil":            val.ValidUntil,
		"independenceEvidenced": view.IndependenceEvidenced,
	})
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.CreateValidation(ctx, val); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "validation.record", "model_version", v.ID,
			model+"@"+version+" validated "+string(val.Outcome), data)
	}); err != nil {
		return nil, err
	}
	return view, nil
}

// checkEvidence resolves the report against the version being validated. There is no foreign
// key (see the validation migration), so this is where the reference is held to account.
func (s *Service) checkEvidence(ctx context.Context, versionID, artifactID string) error {
	arts, err := s.store.ListArtifacts(ctx, versionID)
	if err != nil {
		return err
	}
	for _, a := range arts {
		if a.ID != artifactID {
			continue
		}
		if !evidenceKinds[a.Kind] {
			return invalidField("evidenceArtifactId must be an artifact of kind DOC or METRICS, not "+string(a.Kind), "evidenceArtifactId")
		}
		return nil
	}
	return invalidField("artifact '"+artifactID+"' not found on this version", "evidenceArtifactId")
}

// VersionValidations is a version's validation history with that version's §20.7 state.
type VersionValidations struct {
	// Items is every validation, newest first. The first is the current answer; the rest
	// are the record of what was believed before (§20.5).
	Items []*domain.ValidationView `json:"items"`
	// State is §20.7 evaluated for *this* version against the model's `mrm` row. The model-
	// level read (GET …/classifications/mrm) answers the same question for the model's
	// subject version; this answers it for whichever version was asked about.
	State        domain.ClassificationState `json:"state"`
	StaleReasons []string                   `json:"staleReasons,omitempty"`
}

// ListValidations returns a version's validations and its state.
func (s *Service) ListValidations(ctx context.Context, model, version string) (*VersionValidations, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	vals, err := s.store.ListValidations(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	c, err := s.store.GetClassification(ctx, v.ModelID, domain.RegimeMRM)
	if err != nil && !domain.IsNotFound(err) {
		return nil, err
	}

	facts, err := s.versionMRMFacts(ctx, v)
	if err != nil {
		return nil, err
	}
	if len(vals) > 0 {
		facts.Validation = vals[0]
	}
	state, reasons := domain.MRMStateOf(c, facts, domain.NowMillis())

	out := &VersionValidations{Items: make([]*domain.ValidationView, 0, len(vals)), State: state, StaleReasons: reasons}
	for _, val := range vals {
		out.Items = append(out.Items, domain.NewValidationView(val, v.Author))
	}
	return out, nil
}

// ClearValidationConditions records that a conditional validation's conditions are met — the
// one later write §20.8.2 allows, and the input to §20.7 clause 4.
//
// Only a conditional row can be cleared (409 not_conditional), and only once (409
// already_cleared), so the timestamp says when it happened rather than when it last happened.
func (s *Service) ClearValidationConditions(ctx context.Context, actor, model, version, id string) (*domain.ValidationView, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	vals, err := s.store.ListValidations(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	var val *domain.Validation
	for _, x := range vals {
		if x.ID == id {
			val = x
		}
	}
	if val == nil {
		return nil, domain.NotFound("validation '" + id + "' not found on this version")
	}
	if val.Outcome != domain.ValidationConditional {
		return nil, domain.Precondition("only a conditional validation has conditions to clear",
			map[string]any{"reason": "not_conditional"})
	}
	now := domain.NowMillis()
	data, _ := json.Marshal(map[string]any{"validationId": id})
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.ClearValidationConditions(ctx, v.ID, id, now); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "validation.conditions_cleared", "model_version", v.ID,
			model+"@"+version+" validation conditions cleared", data)
	}); err != nil {
		return nil, err
	}
	val.ConditionsClearedAt = &now
	return domain.NewValidationView(val, v.Author), nil
}

// versionMRMFacts gathers §20.7's facts for one named version. Validation is left for the
// caller, which usually has the list already.
func (s *Service) versionMRMFacts(ctx context.Context, v *domain.ModelVersion) (domain.MRMFacts, error) {
	drift, err := s.store.DriftFactsFor(ctx, v.ModelID)
	if err != nil {
		return domain.MRMFacts{}, err
	}
	evals, err := s.store.ListEvaluations(ctx, v.ID)
	if err != nil {
		return domain.MRMFacts{}, err
	}
	f := domain.MRMFacts{
		Subject: &domain.MRMSubject{VersionID: v.ID, Version: v.Name, Author: v.Author,
			Stage: v.Stage, StageChangedAt: v.StageChangedAt},
		LatestVersionCreatedAt: drift.LatestVersionCreatedAt,
	}
	for _, e := range evals {
		if e.RunAt > f.LatestEvaluationRunAt {
			f.LatestEvaluationRunAt = e.RunAt
		}
	}
	return f, nil
}

// mrmFacts gathers §20.7's facts for a model's subject version, through the same store
// statement the inventory uses — so "which version is the model's state about" is decided in
// one place.
func (s *Service) mrmFacts(ctx context.Context, modelID string) (domain.MRMFacts, error) {
	rows, err := s.store.ListMRMInventory(ctx, domain.ListOptions{}, domain.MRMFilter{ModelID: modelID})
	if err != nil || len(rows) == 0 {
		return domain.MRMFacts{}, err
	}
	return rows[0].Facts, nil
}

func newMRMView(c *domain.RiskClassification, f domain.MRMFacts, now int64) *domain.ClassificationView {
	state, reasons := domain.MRMStateOf(c, f, now)
	v := &domain.ClassificationView{RiskClassification: *c, State: state, StaleReasons: reasons}
	if f.Subject != nil {
		v.Version = f.Subject.Version
		if f.Validation != nil {
			v.LatestValidation = domain.NewValidationView(f.Validation, f.Subject.Author)
		}
	}
	return v
}

// mrmInventory is the §20.9.2 lens: the tier in SQL, the state here.
func (s *Service) mrmInventory(ctx context.Context, o domain.ListOptions, q MRMInventory) ([]*domain.ModelInventoryItem, error) {
	if q.Tier != "" && !domain.ValidMRMTier(q.Tier) {
		e := domain.Invalid("unknown mrmTier")
		e.Details = map[string]any{"field": "mrmTier", "allowedValues": domain.MRMTiers()}
		return nil, e
	}
	if q.State != "" && !domain.ValidMRMState(q.State) {
		e := domain.Invalid("unknown mrmState")
		e.Details = map[string]any{"field": "mrmState", "allowedValues": domain.MRMStates()}
		return nil, e
	}
	rows, err := s.store.ListMRMInventory(ctx, o, domain.MRMFilter{Tier: q.Tier})
	if err != nil {
		return nil, err
	}
	now := domain.NowMillis()
	items := make([]*domain.ModelInventoryItem, 0, len(rows))
	for _, r := range rows {
		// A model with no `mrm` row is `untiered`, and is filterable as such, but carries no
		// view: absence is the state, as for §16.4.
		st, _ := domain.MRMStateOf(r.Classification, r.Facts, now)
		if q.State != "" && st != q.State {
			continue
		}
		item := &domain.ModelInventoryItem{Model: r.Model}
		if r.Classification != nil {
			item.MRM = newMRMView(r.Classification, r.Facts, now)
		}
		items = append(items, item)
	}
	return items, nil
}
