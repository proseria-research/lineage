package modelapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// Handlers for the resource-CRUD half of the Model API (§03): model/version PATCH + archive
// + guarded DELETE, artifact GET/PATCH/DELETE, lineage, deployments, and the audit feed.

func (r *Router) force(req *http.Request) bool { return req.URL.Query().Get("force") == "true" }

// ---- Models ----

func (r *Router) patchModel(w http.ResponseWriter, req *http.Request) {
	var in core.PatchModelInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	m, err := r.svc.PatchModel(req.Context(), r.actor(req), req.PathValue("model"), in)
	writeOr(w, http.StatusOK, m, err)
}

func (r *Router) deleteModel(w http.ResponseWriter, req *http.Request) {
	if err := r.svc.DeleteModel(req.Context(), r.actor(req), req.PathValue("model"), r.force(req)); err != nil {
		api.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// modelAction handles `{model}:archive` (§03.4), parsed from the trailing segment.
func (r *Router) modelAction(w http.ResponseWriter, req *http.Request) {
	seg := req.PathValue("modelAction")
	model, action, ok := strings.Cut(seg, ":")
	if !ok {
		api.WriteError(w, domain.Invalid("unknown model action '"+seg+"'"))
		return
	}
	switch action {
	case "archive":
		m, err := r.svc.ArchiveModel(req.Context(), r.actor(req), model)
		writeOr(w, http.StatusOK, m, err)
	case "hold", "release":
		r.holdAction(w, req, domain.SubjectModel, action, model)
	default:
		api.WriteError(w, domain.Invalid("unknown action ':"+action+"'"))
	}
}

func (r *Router) modelAudit(w http.ResponseWriter, req *http.Request) {
	o, err := auditOpts(req)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	items, next, err := r.svc.ListModelAudit(req.Context(), req.PathValue("model"), o)
	writePage(w, items, next, err)
}

// ---- Versions ----

func (r *Router) patchVersion(w http.ResponseWriter, req *http.Request) {
	var in core.PatchVersionInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	v, err := r.svc.PatchVersion(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), in)
	writeOr(w, http.StatusOK, v, err)
}

func (r *Router) deleteVersion(w http.ResponseWriter, req *http.Request) {
	if err := r.svc.DeleteVersion(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), r.force(req)); err != nil {
		api.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Artifacts ----

func (r *Router) listArtifacts(w http.ResponseWriter, req *http.Request) {
	arts, err := r.svc.ListArtifacts(req.Context(), req.PathValue("model"), req.PathValue("version"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": arts})
}

func (r *Router) getArtifact(w http.ResponseWriter, req *http.Request) {
	a, err := r.svc.GetArtifact(req.Context(), req.PathValue("model"), req.PathValue("version"), req.PathValue("artifact"))
	writeOr(w, http.StatusOK, a, err)
}

func (r *Router) patchArtifact(w http.ResponseWriter, req *http.Request) {
	var in core.PatchArtifactInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	a, err := r.svc.PatchArtifact(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), req.PathValue("artifact"), in)
	writeOr(w, http.StatusOK, a, err)
}

func (r *Router) deleteArtifact(w http.ResponseWriter, req *http.Request) {
	if err := r.svc.DeleteArtifact(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), req.PathValue("artifact")); err != nil {
		api.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Lineage ----

func (r *Router) addLineage(w http.ResponseWriter, req *http.Request) {
	var in core.LineageInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	e, err := r.svc.AddLineage(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), in)
	writeOr(w, http.StatusCreated, e, err)
}

func (r *Router) listLineage(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	// With ?direction=, traverse the graph (ancestry / impact, §07.3); else the flat edge list.
	if dir := q.Get("direction"); dir != "" {
		query := core.LineageQuery{Direction: domain.LineageDirection(dir), Relations: parseRelations(q.Get("relations"))}
		if d, err := strconv.Atoi(q.Get("depth")); err == nil {
			query.Depth = d
		}
		g, err := r.svc.TraverseLineage(req.Context(), req.PathValue("model"), req.PathValue("version"), query)
		writeOr(w, http.StatusOK, g, err)
		return
	}
	edges, err := r.svc.ListLineage(req.Context(), req.PathValue("model"), req.PathValue("version"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": edges})
}

func parseRelations(csv string) map[domain.LineageRelation]bool {
	if csv == "" {
		return nil
	}
	out := map[domain.LineageRelation]bool{}
	for rel := range strings.SplitSeq(csv, ",") {
		if rel = strings.TrimSpace(rel); rel != "" {
			out[domain.LineageRelation(rel)] = true
		}
	}
	return out
}

func (r *Router) deleteLineage(w http.ResponseWriter, req *http.Request) {
	if err := r.svc.DeleteLineage(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), req.PathValue("edgeId")); err != nil {
		api.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Deployments ----

func (r *Router) createDeployment(w http.ResponseWriter, req *http.Request) {
	var in core.DeploymentInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	d, err := r.svc.CreateDeployment(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), in)
	writeOr(w, http.StatusCreated, d, err)
}

func (r *Router) listDeployments(w http.ResponseWriter, req *http.Request) {
	ds, err := r.svc.ListDeployments(req.Context(), req.PathValue("model"), req.PathValue("version"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": ds})
}

func (r *Router) getDeployment(w http.ResponseWriter, req *http.Request) {
	d, err := r.svc.GetDeployment(req.Context(), req.PathValue("id"))
	writeOr(w, http.StatusOK, d, err)
}

func (r *Router) patchDeployment(w http.ResponseWriter, req *http.Request) {
	var in core.DeploymentPatch
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	d, err := r.svc.PatchDeployment(req.Context(), r.actor(req), req.PathValue("id"), in)
	writeOr(w, http.StatusOK, d, err)
}

func (r *Router) deleteDeployment(w http.ResponseWriter, req *http.Request) {
	if err := r.svc.DeleteDeployment(req.Context(), r.actor(req), req.PathValue("id")); err != nil {
		api.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Audit feed ----

func (r *Router) auditFeed(w http.ResponseWriter, req *http.Request) {
	o, err := auditOpts(req)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	q := req.URL.Query()
	items, next, err := r.svc.ListAudit(req.Context(), q.Get("subjectType"), q.Get("subjectId"), o)
	writePage(w, items, next, err)
}

// auditOpts adds `asOf` (§03.3) to the list options: the history as it stood at an instant,
// so a report pinned to a date reads the same rows however much has happened since. A value
// nobody can interpret is refused, not read as "unbounded" — that would silently widen the
// report it was meant to pin.
func auditOpts(req *http.Request) (domain.ListOptions, error) {
	o := listOpts(req)
	if v := req.URL.Query().Get("asOf"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			e := domain.Invalid("asOf must be a positive epoch-millis integer")
			e.Details = map[string]any{"field": "asOf"}
			return o, e
		}
		o.AsOf = n
	}
	return o, nil
}

// ---- shared write helpers ----

func writeOr(w http.ResponseWriter, status int, v any, err error) {
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, status, v)
}

func writePage[T any](w http.ResponseWriter, items []T, next string, err error) {
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}
