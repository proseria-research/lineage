// Package adminui serves the human web console's backend-for-frontend on :8080 (§06).
// It calls the same core in-process — no new business logic, UI-shaped reads only.
package adminui

import (
	"net/http"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

type Router struct{ svc *core.Service }

func New(svc *core.Service) *Router { return &Router{svc: svc} }

func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", r.index)
	mux.HandleFunc("GET /api/overview", r.overview)
	mux.HandleFunc("GET /api/models", r.models)
	return mux
}

func (r *Router) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><title>Lineage</title>
<h1>Lineage — Admin UI</h1><p>BFF placeholder. See <code>/api/overview</code>. The web console SPA mounts here (§06).</p>`))
}

func (r *Router) overview(w http.ResponseWriter, req *http.Request) {
	models, _, err := r.svc.ListModels(req.Context(), domain.ListOptions{})
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"models": len(models)})
}

func (r *Router) models(w http.ResponseWriter, req *http.Request) {
	items, next, err := r.svc.ListModels(req.Context(), domain.ListOptions{})
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}
