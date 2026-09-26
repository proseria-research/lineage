package modelapi

import (
	"net/http"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// Risk-classification endpoints (§16.8). The regime is a path segment, which is what makes
// the write safe: a client submitting one regime's assessment cannot see or touch another's
// row, so it cannot move an anchor the drift predicate measures against (§16.3.2).

func (r *Router) putClassification(w http.ResponseWriter, req *http.Request) {
	regime := domain.Regime(req.PathValue("regime"))
	// Reject an unknown regime before reading the body, so the caller is told the path is
	// wrong rather than being handed a validation error about fields it did supply.
	if !domain.ValidRegime(regime) {
		e := domain.Invalid("unknown regime")
		e.Details = map[string]any{"field": "regime", "allowedValues": regimeNames()}
		api.WriteError(w, e)
		return
	}

	var in core.ClassificationInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	// classifiedAt / classifiedBy / source are absent from ClassificationInput, and `decode`
	// refuses unknown fields, so a client that sends one gets a 400 rather than a silent
	// no-op (§16.6). On a legal attribution field that is the kinder failure: the caller has
	// misunderstood who owns the value, and every other /v1 write would tell them so.
	v, err := r.svc.SetClassification(req.Context(), r.actor(req), req.PathValue("model"), regime, in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, v)
}

func (r *Router) getClassification(w http.ResponseWriter, req *http.Request) {
	regime := domain.Regime(req.PathValue("regime"))
	if !domain.ValidRegime(regime) {
		e := domain.Invalid("unknown regime")
		e.Details = map[string]any{"field": "regime", "allowedValues": regimeNames()}
		api.WriteError(w, e)
		return
	}
	v, err := r.svc.GetClassification(req.Context(), req.PathValue("model"), regime)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, v)
}

func (r *Router) listClassifications(w http.ResponseWriter, req *http.Request) {
	items, err := r.svc.ListClassifications(req.Context(), req.PathValue("model"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	// Not paginated: there is one row per regime and regimes are a closed, small set.
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func regimeNames() []string {
	out := make([]string, len(domain.Regimes))
	for i, k := range domain.Regimes {
		out[i] = string(k)
	}
	return out
}

// ---- Inventory (§16.8.2, §20.9.2) ----

// inventoryReq carries each regime lens's query parameters. The computed states are separate
// from the store filters because they are not columns.
type inventoryReq struct {
	eu     bool
	filter domain.ClassificationFilter
	state  domain.ClassificationState

	mrm      bool
	mrmTier  domain.MRMTier
	mrmState domain.ClassificationState
}

// inventoryQuery reports whether this request asked for classification data at all, and for
// which regimes. A lens is on when any of its filters is set, when ?include= names it
// (`classification` for the EU lens, `mrm` for the MRM one), or when ?regime= does. Both lenses
// at once return the models that pass both.
func inventoryQuery(req *http.Request) (inventoryReq, bool) {
	q := req.URL.Query()
	inc := includeSet(req)
	regime := domain.Regime(q.Get("regime"))
	inv := inventoryReq{
		filter: domain.ClassificationFilter{
			Regime:            domain.RegimeEUAIAct,
			EUSystemRiskClass: domain.EUSystemRiskClass(q.Get("euSystemRiskClass")),
			EUGpaiTier:        domain.EUGpaiTier(q.Get("euGpaiTier")),
		},
		state:    domain.ClassificationState(q.Get("classificationState")),
		mrmTier:  domain.MRMTier(q.Get("mrmTier")),
		mrmState: domain.ClassificationState(q.Get("mrmState")),
	}
	inv.eu = inv.filter.Active() || inv.state != "" || inc["classification"] || regime == domain.RegimeEUAIAct
	inv.mrm = inv.mrmTier != "" || inv.mrmState != "" || inc["mrm"] || regime == domain.RegimeMRM
	if regime != "" && !domain.ValidRegime(regime) {
		// Routed to the EU lens so the regime check below names the parameter.
		inv.eu, inv.filter.Regime = true, regime
	}
	return inv, inv.eu || inv.mrm
}

func (r *Router) listInventory(w http.ResponseWriter, req *http.Request, inv inventoryReq) {
	// Each value is checked against its own enum, so the error names the parameter the
	// caller got wrong rather than a generic "bad filter".
	if !domain.ValidRegime(inv.filter.Regime) {
		api.WriteError(w, badEnum("regime", regimeNames()))
		return
	}
	if v := inv.filter.EUSystemRiskClass; v != "" && !domain.ValidEUSystemRiskClass(v) {
		api.WriteError(w, badEnum("euSystemRiskClass", domain.EUSystemRiskClasses()))
		return
	}
	if v := inv.filter.EUGpaiTier; v != "" && !domain.ValidEUGpaiTier(v) {
		api.WriteError(w, badEnum("euGpaiTier", domain.EUGpaiTiers()))
		return
	}
	if v := inv.state; v != "" && !validClassificationState(v) {
		api.WriteError(w, badEnum("classificationState", classificationStates()))
		return
	}
	if v := inv.mrmTier; v != "" && !domain.ValidMRMTier(v) {
		api.WriteError(w, badEnum("mrmTier", domain.MRMTiers()))
		return
	}
	if v := inv.mrmState; v != "" && !domain.ValidMRMState(v) {
		api.WriteError(w, badEnum("mrmState", domain.MRMStates()))
		return
	}

	var q core.InventoryQuery
	if inv.eu {
		q.EU = &core.EUInventory{Filter: inv.filter, State: inv.state}
	}
	if inv.mrm {
		q.MRM = &core.MRMInventory{Tier: inv.mrmTier, State: inv.mrmState}
	}
	items, next, err := r.svc.Inventory(req.Context(), listOpts(req), q)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}

func badEnum(field string, allowed []string) *domain.Error {
	e := domain.Invalid("unknown " + field)
	e.Details = map[string]any{"field": field, "allowedValues": allowed}
	return e
}

func classificationStates() []string {
	return []string{
		string(domain.ClassificationUnclassified),
		string(domain.ClassificationStale),
		string(domain.ClassificationCurrent),
	}
}

func validClassificationState(s domain.ClassificationState) bool {
	for _, k := range classificationStates() {
		if k == string(s) {
			return true
		}
	}
	return false
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
