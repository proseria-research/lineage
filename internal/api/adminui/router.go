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
	// Legal hold (§19.8). Placing one is a human act about a matter — the Admin surface is
	// where humans act, and it calls the same core operations the Model API does.
	mux.HandleFunc("POST /api/models/{model}/hold", r.hold(true))
	mux.HandleFunc("POST /api/models/{model}/release", r.hold(false))
	mux.HandleFunc("POST /api/models/{model}/versions/{version}/hold", r.hold(true))
	mux.HandleFunc("POST /api/models/{model}/versions/{version}/release", r.hold(false))
	mux.HandleFunc("GET /api/models/{model}/compare", r.compare)
	// The Art. 25 queue (§17.7). Recording a review is a human judgement about a legal
	// question, so the console can do it — through the same core operation, frozen verdict
	// and audit event included.
	mux.HandleFunc("GET /api/reviews", r.reviews)
	mux.HandleFunc("POST /api/models/{model}/versions/{version}/reviews", r.recordReview)
	mux.HandleFunc("GET /api/activity", r.activity)
	// §19.8 — install-level evidence integrity. The status strip is cheap; the recompute is
	// a separate call because it reads every audit row ever written.
	mux.HandleFunc("GET /api/evidence", r.evidence)
	mux.HandleFunc("POST /api/evidence:verify", r.verifyEvidence)
	// Everything else is the embedded console (assets + client-side-routing fallback).
	mux.Handle("GET /", spaHandler())
	return mux
}
