package modelapi

import (
	"net/http"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// Modification-review endpoints (§17.6). The queue is a list, never a gate: nothing here
// refuses a publish, a promotion or a delete, and there is no endpoint that could (§17.1).

// recordReview closes a queue item by recording what a human concluded (§17.6.1).
//
// `verdictAtReview` is absent from ReviewInput and `decode` refuses unknown fields, so a
// client sending one gets 400 rather than a silent no-op. The whole value of the field is that
// the registry witnessed the verdict rather than accepting a claim about it, and a request
// that tries to supply it has misunderstood that badly enough to be worth telling.
func (r *Router) recordReview(w http.ResponseWriter, req *http.Request) {
	var in core.ReviewInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	rev, err := r.svc.RecordReview(req.Context(), r.actor(req),
		req.PathValue("model"), req.PathValue("version"), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, rev)
}

// listVersionReviews returns every review recorded against a version, newest first. Not
// paginated: reviews are rare by construction — one per derivation, plus re-reviews.
func (r *Router) listVersionReviews(w http.ResponseWriter, req *http.Request) {
	items, err := r.svc.ListVersionReviews(req.Context(), req.PathValue("model"), req.PathValue("version"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// reviewQueue is the §17.6.2 feed. `status` narrows to open or closed; omitting it returns
// both rather than defaulting to open — a caller that wants only open items says so, and a
// default would make the total silently unobtainable.
func (r *Router) reviewQueue(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	regime := domain.Regime(orDefault(q.Get("regime"), string(domain.RegimeEUAIAct)))
	if !domain.ValidRegime(regime) {
		api.WriteError(w, badEnum("regime", regimeNames()))
		return
	}
	status := domain.ReviewStatus(q.Get("status"))
	if status != "" && !domain.ValidReviewStatus(status) {
		api.WriteError(w, badEnum("status", []string{string(domain.ReviewOpen), string(domain.ReviewClosed)}))
		return
	}

	items, next, err := r.svc.ListReviewQueue(req.Context(), regime, status, listOpts(req))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}
