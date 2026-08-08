// Package adminui serves the human web console's backend-for-frontend on :8080 (§06).
// It calls the same core in-process — no new business logic, UI-shaped reads only — and
// serves the embedded Vite/React console (static.go).
package adminui

import (
	"net/http"

	"github.com/proseria-research/lineage/internal/core"
)

type Router struct{ svc *core.Service }

func New(svc *core.Service) *Router { return &Router{svc: svc} }

func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()
	// UI-shaped BFF (§06.3); more specific than "/", so these win over the SPA handler.
	mux.HandleFunc("GET /api/overview", r.overview)
	mux.HandleFunc("GET /api/models", r.models)
	mux.HandleFunc("GET /api/models/{model}", r.modelDetail)
	mux.HandleFunc("GET /api/models/{model}/versions/{version}", r.versionDetail)
	mux.HandleFunc("POST /api/models/{model}/versions/{version}/transition", r.transition)
	mux.HandleFunc("GET /api/models/{model}/versions/{version}/graph", r.lineageGraph)
	mux.HandleFunc("PUT /api/models/{model}/classifications/{regime}", r.setClassification)
	mux.HandleFunc("GET /api/models/{model}/compare", r.compare)
	mux.HandleFunc("GET /api/activity", r.activity)
	// Everything else is the embedded console (assets + client-side-routing fallback).
	mux.Handle("GET /", spaHandler())
	return mux
}
