package metrics

import (
	"context"
	"database/sql"

	"github.com/proseria-research/lineage/internal/domain"
)

// App is the process-wide metric set: the instruments, the domain.Meter implementation, and
// the HTTP RED recorder. Injected where telemetry is emitted (core via WithMeter, the API
// middleware via RecordHTTP).
type App struct {
	reg *Registry

	httpReqs *CounterVec
	httpDur  *HistogramVec

	resolveCache      *CounterVec
	signedURLs        *Counter
	versionsPublished *Counter
	transitions       *CounterVec
	demotions         *Counter
	uploadFinalize    *Histogram
	digestMismatch    *Counter
}

var _ domain.Meter = (*App)(nil)

func NewApp() *App {
	r := NewRegistry()
	a := &App{
		reg:               r,
		httpReqs:          r.NewCounterVec("lineage_http_requests_total", "HTTP requests (RED).", "surface", "route", "method", "status"),
		httpDur:           r.NewHistogramVec("lineage_http_request_duration_seconds", "HTTP request duration in seconds (RED).", nil, "surface", "route", "method"),
		resolveCache:      r.NewCounterVec("lineage_resolve_cache_total", "Resolve cache lookups by result.", "result"),
		signedURLs:        r.NewCounter("lineage_signed_urls_total", "Signed artifact URLs minted."),
		versionsPublished: r.NewCounter("lineage_versions_published_total", "Model versions published."),
		transitions:       r.NewCounterVec("lineage_stage_transitions_total", "Stage transitions by target stage.", "to"),
		demotions:         r.NewCounter("lineage_singleton_demotions_total", "Incumbents auto-demoted on singleton promotion."),
		uploadFinalize:    r.NewHistogram("lineage_upload_finalize_seconds", "Artifact finalize (verify + record) duration.", nil),
		digestMismatch:    r.NewCounter("lineage_digest_mismatch_total", "Finalize digest-mismatch rejections."),
	}
	r.NewGauge("lineage_up", "1 if the process is up.").Set(1)
	return a
}

func (a *App) Registry() *Registry { return a.reg }

// ---- domain.Meter ----

func (a *App) ResolveServed(hit bool) {
	res := "miss"
	if hit {
		res = "hit"
	}
	a.resolveCache.With(res).Inc()
}
func (a *App) VersionPublished() { a.versionsPublished.Inc() }
func (a *App) StageTransitioned(to domain.Stage, demoted bool) {
	a.transitions.With(string(to)).Inc()
	if demoted {
		a.demotions.Inc()
	}
}
func (a *App) UploadFinalized(sec float64, mismatch bool) {
	a.uploadFinalize.Observe(sec)
	if mismatch {
		a.digestMismatch.Inc()
	}
}
func (a *App) SignedURLMinted() { a.signedURLs.Inc() }

// RecordHTTP is called by the API middleware for every request (RED, §09.2).
func (a *App) RecordHTTP(surface, route, method, status string, seconds float64) {
	a.httpReqs.With(surface, route, method, status).Inc()
	a.httpDur.With(surface, route, method).Observe(seconds)
}

// ---- scrape-time gauges ----

// DomainStats is a registry snapshot for the domain gauges.
type DomainStats struct {
	Models, Versions, Artifacts, Deployments int
	Stages                                   map[string]int
}

// BindDomainGauges registers gauges refreshed from snapshot() at each scrape (§09.2).
func (a *App) BindDomainGauges(snapshot func(context.Context) DomainStats) {
	models := a.reg.NewGauge("lineage_models", "Total models.")
	versions := a.reg.NewGauge("lineage_versions", "Total model versions.")
	artifacts := a.reg.NewGauge("lineage_artifacts", "Total artifacts.")
	deployments := a.reg.NewGauge("lineage_deployments", "Total deployments.")
	byStage := a.reg.NewGaugeVec("lineage_versions_by_stage", "Versions per stage.", "stage")
	a.reg.OnScrape(func() {
		s := snapshot(context.Background())
		models.Set(float64(s.Models))
		versions.Set(float64(s.Versions))
		artifacts.Set(float64(s.Artifacts))
		deployments.Set(float64(s.Deployments))
		for st, n := range s.Stages {
			byStage.With(st).Set(float64(n))
		}
	})
}

// BindDBGauges exposes connection-pool stats at each scrape (§09.2). No-op for the memory store.
func (a *App) BindDBGauges(db *sql.DB) {
	if db == nil {
		return
	}
	open := a.reg.NewGauge("lineage_db_connections_open", "Open DB connections.")
	inUse := a.reg.NewGauge("lineage_db_connections_in_use", "In-use DB connections.")
	idle := a.reg.NewGauge("lineage_db_connections_idle", "Idle DB connections.")
	a.reg.OnScrape(func() {
		st := db.Stats()
		open.Set(float64(st.OpenConnections))
		inUse.Set(float64(st.InUse))
		idle.Set(float64(st.Idle))
	})
}
