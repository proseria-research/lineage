// Package modelapi serves the machine-facing Model API on /v1 (§03/§04). No auth —
// infra handles it; the actor comes from a trusted header for audit (§00 axiom 4).
package modelapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

type Router struct {
	svc         *core.Service
	actorHeader string
}

func New(svc *core.Service, actorHeader string) *Router {
	return &Router{svc: svc, actorHeader: actorHeader}
}

// Handler registers routes on a Go 1.22+ ServeMux (method + path patterns).
func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/models", r.createModel)
	mux.HandleFunc("GET /v1/models", r.listModels)
	mux.HandleFunc("GET /v1/models/{model}", r.getModel)
	mux.HandleFunc("GET /v1/models/{model}/resolve", r.resolve)
	mux.HandleFunc("POST /v1/models/{model}/versions", r.publishVersion)
	mux.HandleFunc("GET /v1/models/{model}/versions", r.listVersions)
	mux.HandleFunc("GET /v1/models/{model}/versions/{version}", r.getVersion)
	// Colon-action lives in the trailing segment (e.g. "1.4.0:transition"), parsed below.
	mux.HandleFunc("POST /v1/models/{model}/versions/{action}", r.versionAction)
	mux.HandleFunc("GET /v1/openapi.json", r.openapi)
	return mux
}

func (r *Router) actor(req *http.Request) string { return req.Header.Get(r.actorHeader) }

func (r *Router) createModel(w http.ResponseWriter, req *http.Request) {
	var in core.CreateModelInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	m, err := r.svc.CreateModel(req.Context(), r.actor(req), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, m)
}

func (r *Router) listModels(w http.ResponseWriter, req *http.Request) {
	items, next, err := r.svc.ListModels(req.Context(), listOpts(req))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}

func (r *Router) getModel(w http.ResponseWriter, req *http.Request) {
	m, err := r.svc.GetModel(req.Context(), req.PathValue("model"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, m)
}

func (r *Router) publishVersion(w http.ResponseWriter, req *http.Request) {
	var in core.PublishVersionInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	v, arts, err := r.svc.PublishVersion(req.Context(), r.actor(req), req.PathValue("model"), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, map[string]any{"version": v, "artifacts": arts})
}

func (r *Router) listVersions(w http.ResponseWriter, req *http.Request) {
	items, next, err := r.svc.ListVersions(req.Context(), req.PathValue("model"), listOpts(req))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}

func (r *Router) getVersion(w http.ResponseWriter, req *http.Request) {
	v, err := r.svc.GetVersion(req.Context(), req.PathValue("model"), req.PathValue("version"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, v)
}

// versionAction handles the `{version}:transition` colon-action (§03.7).
func (r *Router) versionAction(w http.ResponseWriter, req *http.Request) {
	seg := req.PathValue("action")
	version, action, ok := strings.Cut(seg, ":")
	if !ok {
		api.WriteError(w, domain.Invalid("unknown version action '"+seg+"'"))
		return
	}
	switch action {
	case "transition":
		var body struct {
			To     domain.Stage `json:"to"`
			Reason string       `json:"reason"`
		}
		if err := decode(req, &body); err != nil {
			api.WriteError(w, err)
			return
		}
		v, err := r.svc.Transition(req.Context(), r.actor(req), req.PathValue("model"), version, body.To, body.Reason)
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, v)
	default:
		api.WriteError(w, domain.Invalid("unknown action ':"+action+"'"))
	}
}

func (r *Router) resolve(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	sel := domain.Selector{Stage: domain.Stage(q.Get("stage")), Version: q.Get("version")}
	for k, vs := range q {
		if strings.HasPrefix(k, "label.") && len(vs) > 0 {
			sel.LabelKey, sel.LabelValue = strings.TrimPrefix(k, "label."), vs[0]
		}
	}
	res, err := r.svc.Resolve(req.Context(), req.PathValue("model"), sel)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	if res.Digest != "" {
		w.Header().Set("ETag", `"`+res.Digest+`"`)
	}
	api.WriteJSON(w, http.StatusOK, res)
}

func (r *Router) openapi(w http.ResponseWriter, _ *http.Request) {
	// TODO: serve the generated OpenAPI contract (§00.11.6, §10). Placeholder for now.
	api.WriteJSON(w, http.StatusOK, map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "Lineage Model API", "version": "v1"},
		"paths":   map[string]any{},
	})
}

// ---- helpers ----

func decode(req *http.Request, v any) error {
	if req.Body == nil {
		return domain.Invalid("missing request body")
	}
	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return domain.Invalid("invalid JSON: " + err.Error())
	}
	return nil
}

func listOpts(req *http.Request) domain.ListOptions {
	q := req.URL.Query()
	o := domain.ListOptions{
		OrderBy: q.Get("orderBy"), Q: q.Get("q"),
		Filters: map[string]string{}, Labels: map[string]string{},
	}
	if s := q.Get("state"); s != "" {
		o.Filters["state"] = s
	}
	if s := q.Get("stage"); s != "" {
		o.Filters["stage"] = s
	}
	for k, vs := range q {
		if strings.HasPrefix(k, "label.") && len(vs) > 0 {
			o.Labels[strings.TrimPrefix(k, "label.")] = vs[0]
		}
	}
	return o
}
