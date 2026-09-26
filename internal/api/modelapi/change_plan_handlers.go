package modelapi

import (
	"net/http"
	"strings"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// Change-control-plan endpoints (§22.7). The queue reports; nothing here refuses a publish or
// a promotion, and there is no endpoint that could (§22.5).

// declareChangePlan records a plan (§22.7.1). effectiveTo, declaredBy and declaredAt are
// absent from ChangePlanInput and `decode` refuses unknown fields, so supplying one is 400.
func (r *Router) declareChangePlan(w http.ResponseWriter, req *http.Request) {
	var in core.ChangePlanInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	p, err := r.svc.DeclareChangePlan(req.Context(), r.actor(req), req.PathValue("model"), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, p)
}

// listChangePlans returns every plan the model has had, superseded ones included. Not
// paginated: plans are rare by construction — one per clearance.
func (r *Router) listChangePlans(w http.ResponseWriter, req *http.Request) {
	items, err := r.svc.ListChangePlans(req.Context(), req.PathValue("model"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// versionConformance is §22.4 for one version, one item per derived_from edge.
func (r *Router) versionConformance(w http.ResponseWriter, req *http.Request) {
	items, err := r.svc.VersionConformance(req.Context(), req.PathValue("model"), req.PathValue("version"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// conformanceQueue is the §22.7.2 feed. `status` takes one value or several, comma-separated
// or repeated — `outside_plan,undetermined` is the "needs a human" view. Omitting it returns
// every row, for the /v1/reviews reason.
func (r *Router) conformanceQueue(w http.ResponseWriter, req *http.Request) {
	var statuses []domain.Conformance
	for _, v := range req.URL.Query()["status"] {
		for s := range strings.SplitSeq(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				statuses = append(statuses, domain.Conformance(s))
			}
		}
	}
	for _, s := range statuses {
		if !domain.ValidConformance(s) {
			api.WriteError(w, badEnum("status", domain.Conformances()))
			return
		}
	}
	items, next, err := r.svc.ConformanceQueue(req.Context(), statuses, listOpts(req))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}
