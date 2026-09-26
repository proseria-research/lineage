package core

import (
	"context"
	"encoding/json"

	"github.com/proseria-research/lineage/internal/domain"
)

// Risk-classification writes and reads (§16). The registry records what a person declared
// and reports when that declaration has gone out of date. It never decides a class, never
// downgrades one, and never blocks anything because one is stale (§16.2).

// ClassificationInput is the client-settable half of a classification.
//
// There is no field for ClassifiedAt or ClassifiedBy. §16.6 says the server sets them and a
// request value is ignored — implementing that as "there is nowhere to put one" is stronger
// than implementing it as a line of code that strips them, because it cannot be forgotten by
// the next person to add a field.
//
// Every regime's enum group shares the one input, because the path picks the regime and the
// body is that regime's fields plus the shared ones (§20.9). A field from another regime's
// group is refused by validation rather than dropped.
type ClassificationInput struct {
	EUSystemRiskClass domain.EUSystemRiskClass `json:"euSystemRiskClass"`
	EUGpaiTier        domain.EUGpaiTier        `json:"euGpaiTier"`
	MRMTier           domain.MRMTier           `json:"mrmTier"`
	IntendedPurpose   string                   `json:"intendedPurpose"`
	Basis             string                   `json:"basis"`
	ReviewDueAt       *int64                   `json:"reviewDueAt"`
}

// SetClassification replaces this model's assessment under one regime (§16.8: PUT, not
// PATCH — an old basis must never sit underneath a brand-new class).
//
// The regime comes from the path, so writing one regime cannot see or touch another's row,
// and in particular cannot move another's ClassifiedAt anchor (§16.3.2).
func (s *Service) SetClassification(ctx context.Context, actor, model string, regime domain.Regime, in ClassificationInput) (*domain.ClassificationView, error) {
	m, err := s.store.GetModel(ctx, model)
	if err != nil {
		return nil, err
	}

	now := domain.NowMillis()
	c := domain.RiskClassification{
		ModelID:           m.ID,
		Regime:            regime,
		EUSystemRiskClass: in.EUSystemRiskClass,
		EUGpaiTier:        in.EUGpaiTier,
		MRMTier:           in.MRMTier,
		IntendedPurpose:   in.IntendedPurpose,
		Basis:             in.Basis,
		ReviewDueAt:       in.ReviewDueAt,
		// Server-set. ClassifiedAt is the anchor every §16.5 clause measures against, so a
		// client that could backdate it could hide a staleness.
		ClassifiedAt: now,
		ClassifiedBy: actor,
	}
	c.ApplyDefaults()
	if verr := domain.ValidateRiskClassification(c, now); verr != nil {
		return nil, verr
	}
	fields := map[string]any{"regime": string(regime), "reviewDueAt": c.ReviewDueAt}
	answer := string(c.EUSystemRiskClass)
	if regime == domain.RegimeMRM {
		fields["mrmTier"] = string(c.MRMTier)
		answer = string(c.MRMTier)
	} else {
		fields["euSystemRiskClass"] = string(c.EUSystemRiskClass)
		fields["euGpaiTier"] = string(c.EUGpaiTier)
	}
	data, _ := json.Marshal(fields)
	// The regime is recorded on the event, not just in the summary: an auditor reconstructing
	// who classified what under which rulebook reads structured data, not prose (§16.8).
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.PutClassification(ctx, &c); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "classification.set", "model", m.ID,
			model+" classified "+answer+" under "+string(regime), data)
	}); err != nil {
		return nil, err
	}

	return s.viewFor(ctx, m.ID, &c, now)
}

// GetClassification returns one regime's row with its computed state.
//
// A model with no row for this regime is a not_found. That is the `unclassified` state
// (§16.4), and the caller distinguishes it from `stale` by the code — this endpoint answers
// "show me the assessment", and there is no assessment to show. The inventory query is where
// `unclassified` appears as a state alongside the others (§16.8.2).
func (s *Service) GetClassification(ctx context.Context, model string, regime domain.Regime) (*domain.ClassificationView, error) {
	m, err := s.store.GetModel(ctx, model)
	if err != nil {
		return nil, err
	}
	c, err := s.store.GetClassification(ctx, m.ID, regime)
	if err != nil {
		return nil, err
	}
	return s.viewFor(ctx, m.ID, c, domain.NowMillis())
}

// ListClassifications returns every regime's row for a model, each with its own state.
// States are computed per row against that row's own ClassifiedAt, which is the whole point
// of the per-regime key (§16.3.2).
func (s *Service) ListClassifications(ctx context.Context, model string) ([]*domain.ClassificationView, error) {
	m, err := s.store.GetModel(ctx, model)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListClassifications(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	now := domain.NowMillis()

	out := make([]*domain.ClassificationView, 0, len(rows))
	for _, c := range rows {
		v, err := s.viewFor(ctx, m.ID, c, now)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// viewFor computes a row's state under its own regime's predicate. The regimes do not share
// facts: §16.5 measures model-level drift against the classification, §20.7 measures a
// version's validation, so each branch gathers only what its predicate reads.
func (s *Service) viewFor(ctx context.Context, modelID string, c *domain.RiskClassification, now int64) (*domain.ClassificationView, error) {
	if c.Regime == domain.RegimeMRM {
		facts, err := s.mrmFacts(ctx, modelID)
		if err != nil {
			return nil, err
		}
		return newMRMView(c, facts, now), nil
	}
	facts, err := s.driftFacts(ctx, modelID)
	if err != nil {
		return nil, err
	}
	return newView(c, facts, now), nil
}

// driftFacts is the store's aggregate plus clause 4's (§16.5, `17.4`).
//
// The store cannot supply clause 4. Whether a derivation is an *open* item depends on the
// §11.4 verdict, which is a lookup over four hashes rather than a column, so answering it in
// SQL would mean writing the verdict table a second time in a second language. Core computes
// it from the same reviewItemOf every other caller uses, and this function is the only place
// the two halves are joined — so no read path can accidentally evaluate three clauses out of
// four.
//
// The regime is pinned to eu_ai_act rather than threaded through, because the queue's own
// condition 3 reads the EU class (`17.4`). DriftFacts stays regime-independent as
// ComplianceStore promises: a model has one set of open items, and every regime's row compares
// that same fact against its own anchor. `20.7` brings its own clauses, not a second reading
// of this one.
func (s *Service) driftFacts(ctx context.Context, modelID string) (domain.DriftFacts, error) {
	f, err := s.store.DriftFactsFor(ctx, modelID)
	if err != nil {
		return domain.DriftFacts{}, err
	}
	open, err := s.openReviewFacts(ctx, domain.RegimeEUAIAct, modelID)
	if err != nil {
		return domain.DriftFacts{}, err
	}
	f.LatestOpenReviewCreatedAt = open[modelID]
	return f, nil
}

func newView(c *domain.RiskClassification, facts domain.DriftFacts, now int64) *domain.ClassificationView {
	state, reasons := domain.ClassificationStateOf(c, facts, now)
	return &domain.ClassificationView{RiskClassification: *c, State: state, StaleReasons: reasons}
}

// ListInventory answers §16.8.2 — "which high-risk models do we have?" — in one call. It is
// Inventory with only the EU lens.
func (s *Service) ListInventory(ctx context.Context, o domain.ListOptions, f domain.ClassificationFilter, state domain.ClassificationState) ([]*domain.ModelInventoryItem, string, error) {
	return s.Inventory(ctx, o, InventoryQuery{EU: &EUInventory{Filter: f, State: state}})
}

// InventoryQuery selects which regimes' lenses an inventory read carries, and each lens's
// filters. A nil lens is not asked for: its join does not run and its field stays absent.
// Asking for both returns the models that pass both — the console's one table with two
// regimes' columns (§20.10).
type InventoryQuery struct {
	EU  *EUInventory
	MRM *MRMInventory
}

// EUInventory is the §16.8.2 lens.
type EUInventory struct {
	Filter domain.ClassificationFilter
	State  domain.ClassificationState
}

// MRMInventory is the §20.9.2 lens. State is one of the §20.7 ladder values.
type MRMInventory struct {
	Tier  domain.MRMTier
	State domain.ClassificationState
}

// Inventory is the model list with one or more regimes' classification attached and
// filtered on.
//
// Stored enums are pushed into SQL; the computed states are applied here, over the same pure
// predicates every other read path uses — the stores return facts, not states, so each
// predicate exists once.
//
// Paging happens last, after every state filter, so a page is never short and never skips a
// match. That is why both store queries are unpaginated.
func (s *Service) Inventory(ctx context.Context, o domain.ListOptions, q InventoryQuery) ([]*domain.ModelInventoryItem, string, error) {
	var items []*domain.ModelInventoryItem
	if q.EU != nil {
		eu, err := s.euInventory(ctx, o, q.EU.Filter, q.EU.State)
		if err != nil {
			return nil, "", err
		}
		items = eu
	}
	if q.MRM != nil {
		mrm, err := s.mrmInventory(ctx, o, *q.MRM)
		if err != nil {
			return nil, "", err
		}
		if q.EU == nil {
			items = mrm
		} else {
			// Both lenses: keep the EU order and the models that passed both filters.
			byID := make(map[string]*domain.ModelInventoryItem, len(mrm))
			for _, it := range mrm {
				byID[it.ID] = it
			}
			kept := items[:0]
			for _, it := range items {
				if m, ok := byID[it.ID]; ok {
					it.MRM = m.MRM
					kept = append(kept, it)
				}
			}
			items = kept
		}
	}

	page, next := domain.Page(items, func(i *domain.ModelInventoryItem) (int64, string) {
		return i.CreatedAt, i.ID
	}, o.PageToken, o.PageSize)
	return page, next, nil
}

func (s *Service) euInventory(ctx context.Context, o domain.ListOptions, f domain.ClassificationFilter, state domain.ClassificationState) ([]*domain.ModelInventoryItem, error) {
	if f.Regime == "" {
		f.Regime = domain.RegimeEUAIAct
	}
	// The §16.5 predicate reads the EU group, so this lens is the EU regime's only. The MRM
	// lens has its own query and predicate (§20.7).
	if f.Regime != domain.RegimeEUAIAct {
		return nil, domain.Invalid("the classification filters apply to eu_ai_act; use mrmTier and mrmState for mrm")
	}
	rows, err := s.store.ListInventory(ctx, o, f)
	if err != nil {
		return nil, err
	}
	// Clause 4 for the whole inventory in one query rather than one per row. The scan is
	// bounded by ListDerivations' inner join on classification, so it covers the classified
	// models — which is the same set this list is about.
	open, err := s.openReviewFacts(ctx, f.Regime, "")
	if err != nil {
		return nil, err
	}

	now := domain.NowMillis()
	items := make([]*domain.ModelInventoryItem, 0, len(rows))
	for _, r := range rows {
		r.Facts.LatestOpenReviewCreatedAt = open[r.Model.ID]
		st, reasons := domain.ClassificationStateOf(r.Classification, r.Facts, now)
		if state != "" && st != state {
			continue
		}
		item := &domain.ModelInventoryItem{Model: r.Model}
		if r.Classification != nil {
			item.Classification = &domain.ClassificationView{
				RiskClassification: *r.Classification, State: st, StaleReasons: reasons,
			}
		}
		items = append(items, item)
	}
	return items, nil
}
