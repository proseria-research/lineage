package modelapi

import (
	"net/http"
	"strings"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// Model-insight endpoints (§11.6). Producers write facts here; consumers and the console
// read them. Nothing in this file opens an artifact.

// insightSchemaDoc serves the versioned submission schema. It is the contract a producer
// builds against, so it is published from the registry itself rather than only in docs —
// a producer can fetch the exact schema the server it is talking to enforces (§11d).
func (r *Router) insightSchemaDoc(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/schema+json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(insightSchema)
}

// writeInsight backs both PATCH (merge per field — the default) and PUT (full replace).
func (r *Router) writeInsight(replace bool) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var in core.InsightWrite
		if err := decode(req, &in); err != nil {
			api.WriteError(w, err)
			return
		}
		force := req.URL.Query().Get("force") == "true"
		got, err := r.svc.WriteInsight(req.Context(), r.actor(req),
			req.PathValue("model"), req.PathValue("version"), in, replace, force)
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, got)
	}
}

func (r *Router) getInsight(w http.ResponseWriter, req *http.Request) {
	inc := includeSet(req)
	got, err := r.svc.GetInsight(req.Context(), req.PathValue("model"), req.PathValue("version"), inc["layers"])
	if err != nil {
		api.WriteError(w, err)
		return
	}
	// Attribution is only returned when asked for: it roughly doubles the payload and most
	// readers want the values (§11.6.3).
	if !inc["sources"] {
		got.FieldSources = nil
	}
	api.WriteJSON(w, http.StatusOK, got)
}

func (r *Router) addEvaluation(w http.ResponseWriter, req *http.Request) {
	var in core.EvaluationInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	e, err := r.svc.AddEvaluation(req.Context(), r.actor(req),
		req.PathValue("model"), req.PathValue("version"), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, e)
}

func (r *Router) listEvaluations(w http.ResponseWriter, req *http.Request) {
	items, err := r.svc.ListEvaluations(req.Context(), req.PathValue("model"), req.PathValue("version"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": coalesce(items)})
}

func (r *Router) putFootprint(w http.ResponseWriter, req *http.Request) {
	var in core.FootprintInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	f, err := r.svc.PutFootprint(req.Context(), r.actor(req),
		req.PathValue("model"), req.PathValue("version"), req.PathValue("scenario"), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, f)
}

func (r *Router) listFootprints(w http.ResponseWriter, req *http.Request) {
	items, err := r.svc.ListFootprints(req.Context(), req.PathValue("model"), req.PathValue("version"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": coalesce(items)})
}

// modelDiff compares two versions of one model: ?from=&to=.
func (r *Router) modelDiff(w http.ResponseWriter, req *http.Request) {
	model := req.PathValue("model")
	q := req.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	if from == "" || to == "" {
		api.WriteError(w, domain.Invalid("diff requires both ?from= and ?to= version names"))
		return
	}
	d, err := r.svc.DiffVersions(req.Context(), model, from, model, to)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, d)
}

// globalDiff compares across models using model@version refs, e.g.
// /v1/diff?from=teacher@2&to=student@1 (§11.6).
func (r *Router) globalDiff(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	fromModel, fromVer, err := splitRef(q.Get("from"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	toModel, toVer, err := splitRef(q.Get("to"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	d, err := r.svc.DiffVersions(req.Context(), fromModel, fromVer, toModel, toVer)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, d)
}

func splitRef(ref string) (model, version string, err error) {
	m, v, ok := strings.Cut(ref, "@")
	if !ok || m == "" || v == "" {
		return "", "", domain.Invalid("expected a model@version reference, got '" + ref + "'")
	}
	return m, v, nil
}

// includeSet parses ?include=a,b into a lookup.
func includeSet(req *http.Request) map[string]bool {
	out := map[string]bool{}
	for part := range strings.SplitSeq(req.URL.Query().Get("include"), ",") {
		if p := strings.TrimSpace(part); p != "" {
			out[p] = true
		}
	}
	return out
}

// coalesce renders an empty collection as [] rather than null, matching the other list
// endpoints (§03.3).
func coalesce[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
