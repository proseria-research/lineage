package modelapi

import (
	"net/http"
	"strings"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// Model-risk validation endpoints (§20.9). The tier itself rides the classification path with
// `mrm` in the regime slot. Nothing here refuses a publish or a promotion.

// recordValidation appends one validation (§20.9.1).
//
// `validatedBy` and `validatedAt` are absent from ValidationInput and `decode` refuses unknown
// fields, so a client sending either gets 400 rather than a silent no-op — the value of both
// is that the registry witnessed them.
func (r *Router) recordValidation(w http.ResponseWriter, req *http.Request) {
	var in core.ValidationInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	v, err := r.svc.RecordValidation(req.Context(), r.actor(req),
		req.PathValue("model"), req.PathValue("version"), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, v)
}

// listValidations returns a version's validation history, newest first, with that version's
// §20.7 state. Not paginated: validations are rare by construction — one per validation cycle.
func (r *Router) listValidations(w http.ResponseWriter, req *http.Request) {
	out, err := r.svc.ListValidations(req.Context(), req.PathValue("model"), req.PathValue("version"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// validationAction serves POST …/validations/{id}:clearConditions. ':' rides the wildcard
// segment, as on the version and audit action routes.
func (r *Router) validationAction(w http.ResponseWriter, req *http.Request) {
	seg := req.PathValue("idAction")
	id, action, ok := strings.Cut(seg, ":")
	if !ok || action != "clearConditions" {
		api.WriteError(w, domain.Invalid("unknown validation action '"+seg+"'"))
		return
	}
	v, err := r.svc.ClearValidationConditions(req.Context(), r.actor(req),
		req.PathValue("model"), req.PathValue("version"), id)
	writeOr(w, http.StatusOK, v, err)
}
