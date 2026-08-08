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
