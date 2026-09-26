package core

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/proseria-research/lineage/internal/domain"
)

// Change control plans (§22). A plan declares which changes are pre-authorised; this file
// records plans and reports whether each derivation falls inside the one in force when it
// shipped.
//
// It reports; it does not adjudicate. Nothing here refuses a publish, a promotion or a
// lineage write because a version is outside its plan, and there is no path that could
// (§22.5).

// ChangePlanInput is the client-settable half of a plan.
//
// There is no field for EffectiveTo, DeclaredBy or DeclaredAt. EffectiveTo is written only by
// a later supersession, and the other two are the registry's witness; the shared decoder
// rejects unknown fields, so a client sending any of them gets 400.
type ChangePlanInput struct {
	Ref             string           `json:"ref"`
	Summary         string           `json:"summary"`
	AllowedVerdicts []domain.Verdict `json:"allowedVerdicts"`
	// AllowedMethods: omitted or null is unconstrained; a list constrains (§22.3).
	AllowedMethods     []string `json:"allowedMethods"`
	ProtocolArtifactID string   `json:"protocolArtifactId"`
	// EffectiveFrom defaults to now. A past value is legitimate: a plan is usually recorded
	// after the clearance it reflects.
	EffectiveFrom *int64 `json:"effectiveFrom"`
	// Supersedes names the open plan this one replaces. Without it, declaring a plan while
	// another is open is 409 plan_overlap: replacing an envelope is deliberate, never a side
	// effect of a second POST.
	Supersedes string `json:"supersedes"`
}

// DeclareChangePlan records a plan against a model (§22.7.1), closing the superseded one in
// the same write.
func (s *Service) DeclareChangePlan(ctx context.Context, actor, model string, in ChangePlanInput) (*domain.ChangePlan, error) {
	m, err := s.store.GetModel(ctx, model)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Summary) == "" {
		return nil, invalidField("summary is required — what the plan permits, in the operator's words", "summary")
	}
	now := domain.NowMillis()
	p := &domain.ChangePlan{
		ID: domain.NewID(), ModelID: m.ID, Ref: in.Ref, Summary: in.Summary,
		AllowedVerdicts: in.AllowedVerdicts, AllowedMethods: in.AllowedMethods,
		ProtocolArtifactID: in.ProtocolArtifactID, EffectiveFrom: now,
		// Server-set: the registry witnessed who declared it and when (§22.6.1).
		DeclaredBy: actor, DeclaredAt: now,
	}
	if in.EffectiveFrom != nil {
		p.EffectiveFrom = *in.EffectiveFrom
	}
	if err := domain.ValidateChangePlanEnvelope(p); err != nil {
		return nil, err
	}
	if p.ProtocolArtifactID != "" {
		if err := s.checkProtocol(ctx, m.ID, p.ProtocolArtifactID); err != nil {
			return nil, err
		}
	}
	if err := s.store.CreateChangePlan(ctx, p, in.Supersedes); err != nil {
		return nil, err
	}

	// The envelope goes on the event as data, so "what was pre-authorised, from when" is
	// reconstructible from the audit trail alone.
	data, _ := json.Marshal(map[string]any{
		"planId":          p.ID,
		"ref":             p.Ref,
		"allowedVerdicts": p.AllowedVerdicts,
		"allowedMethods":  p.AllowedMethods,
		"effectiveFrom":   p.EffectiveFrom,
		"supersedes":      in.Supersedes,
	})
	s.audit(ctx, actor, "change_plan.declare", "model", m.ID, m.Name+" change plan declared", data)
	if in.Supersedes != "" {
		data, _ := json.Marshal(map[string]any{
			"planId": in.Supersedes, "supersededBy": p.ID, "effectiveTo": p.EffectiveFrom,
		})
		s.audit(ctx, actor, "change_plan.supersede", "model", m.ID, m.Name+" change plan superseded", data)
	}
	return p, nil
}

// checkProtocol holds the plan's document reference to account: there is no foreign key (see
// the change_plan migration), so this is where it is checked. It must be a DOC artifact on one
// of this model's versions — the PCCP is the model's paperwork, not another model's.
func (s *Service) checkProtocol(ctx context.Context, modelID, artifactID string) error {
	a, err := s.store.GetArtifactByID(ctx, artifactID)
	if domain.IsNotFound(err) {
		return invalidField("artifact '"+artifactID+"' not found", "protocolArtifactId")
	}
	if err != nil {
		return err
	}
	v, err := s.store.GetVersionByID(ctx, a.VersionID)
	if err != nil {
		return err
	}
	if v.ModelID != modelID {
		return invalidField("artifact '"+artifactID+"' belongs to another model", "protocolArtifactId")
	}
	if a.Kind != domain.KindDoc {
		return invalidField("protocolArtifactId must be an artifact of kind DOC, not "+string(a.Kind), "protocolArtifactId")
	}
	return nil
}

// ListChangePlans returns a model's plans, newest effective_from first — superseded ones
// included, since which plan was in force when is the whole question (§22.6.1).
func (s *Service) ListChangePlans(ctx context.Context, model string) ([]*domain.ChangePlan, error) {
	m, err := s.store.GetModel(ctx, model)
	if err != nil {
		return nil, err
	}
	return s.store.ListChangePlans(ctx, m.ID)
}

// NamedChangePlan is a plan with its model's name, for a reader listing plans across models.
type NamedChangePlan struct {
	*domain.ChangePlan
	Model string `json:"model"`
}

// AllChangePlans returns every plan on the install, newest effective_from first, each named —
// the console's plan register. Two reads, not one per model.
func (s *Service) AllChangePlans(ctx context.Context) ([]*NamedChangePlan, error) {
	plans, err := s.store.ListChangePlans(ctx, "")
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	out := make([]*NamedChangePlan, 0, len(plans))
	for _, p := range plans {
		if _, ok := names[p.ModelID]; !ok {
			if m, err := s.store.GetModel(ctx, p.ModelID); err == nil {
				names[p.ModelID] = m.Name
			}
		}
		out = append(out, &NamedChangePlan{ChangePlan: p, Model: names[p.ModelID]})
	}
	return out, nil
}

// VersionConformance answers §22.4 for one version: one item per derived_from edge. A version
// with none has nothing to compare, which is 409 no_predecessor (§22.7.3) rather than an
// empty list a caller could mistake for "within plan".
func (s *Service) VersionConformance(ctx context.Context, model, version string) ([]*domain.ConformanceItem, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListPlanDerivations(ctx, v.ModelID, v.ID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, domain.Precondition("version has no derived_from edge to judge against a plan",
			map[string]any{"reason": "no_predecessor"})
	}
	plans, err := s.store.ListChangePlans(ctx, v.ModelID)
	if err != nil {
		return nil, err
	}
	items := make([]*domain.ConformanceItem, 0, len(rows))
	for _, d := range rows {
		items = append(items, conformanceItemOf(d, plans))
	}
	return items, nil
}

// ConformanceQueue is §22.7.2 — "what shipped outside its plan, or cannot be judged?".
//
// statuses narrows to any of the given values; empty returns every row, so the total is
// always obtainable (the /v1/reviews rule). Filtering happens here, not in SQL, because the
// verdict is a lookup over hashes rather than a column; paging is applied last.
func (s *Service) ConformanceQueue(ctx context.Context, statuses []domain.Conformance, o domain.ListOptions) ([]*domain.ConformanceItem, string, error) {
	want := map[domain.Conformance]bool{}
	for _, c := range statuses {
		if !domain.ValidConformance(c) {
			e := domain.Invalid("unknown status '" + string(c) + "'")
			e.Details = map[string]any{"field": "status", "allowedValues": domain.Conformances()}
			return nil, "", e
		}
		want[c] = true
	}

	all, err := s.store.ListChangePlans(ctx, "")
	if err != nil {
		return nil, "", err
	}
	plans := map[string][]*domain.ChangePlan{}
	for _, p := range all {
		plans[p.ModelID] = append(plans[p.ModelID], p)
	}
	rows, err := s.store.ListPlanDerivations(ctx, "", "")
	if err != nil {
		return nil, "", err
	}
	items := make([]*domain.ConformanceItem, 0, len(rows))
	for _, d := range rows {
		it := conformanceItemOf(d, plans[d.ModelID])
		if len(want) > 0 && !want[it.Conformance] {
			continue
		}
		items = append(items, it)
	}

	page, next := domain.Page(items, func(i *domain.ConformanceItem) (int64, string) {
		return i.PublishedAt, i.EdgeID
	}, o.PageToken, o.PageSize)
	return page, next, nil
}

// conformanceItemOf turns a store row into a §22.7.2 item. It is the one place an item is
// built: the per-version read, the queue and the console all go through it, and the judgement
// itself is domain.ConformanceOf.
func conformanceItemOf(d *domain.PlanDerivationRow, plans []*domain.ChangePlan) *domain.ConformanceItem {
	c := domain.Classify(d.FromHashes, d.ToHashes)
	method := declaredMethod(d.Edge.Properties)
	status, p, reasons := domain.ConformanceOf(plans, d.PublishedAt, c.Verdict, method)
	it := &domain.ConformanceItem{
		Model: d.Model, Version: d.Version, VersionID: d.VersionID, EdgeID: d.Edge.ID,
		DerivedFromRef: d.Edge.DstRef,
		Conformance:    status, Reasons: reasons,
		Verdict: c.Verdict, Candidates: c.Candidates, Missing: c.Missing,
		DeclaredMethod: method,
		Hashes:         c.Hashes,
		Basis: domain.ReviewBasis{
			FromHashes: presentHashes(d.FromHashes),
			ToHashes:   presentHashes(d.ToHashes),
		},
		PublishedAt: d.PublishedAt,
	}
	if d.ParentVersion != "" {
		it.DerivedFrom = &domain.ReviewSide{Model: d.ParentModel, Version: d.ParentVersion}
	}
	if p != nil {
		it.Plan = &domain.PlanRef{ID: p.ID, Ref: p.Ref}
		it.AllowedVerdicts, it.AllowedMethods = p.AllowedVerdicts, p.AllowedMethods
	}
	return it
}
