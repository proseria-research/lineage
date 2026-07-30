// Package observability provides health probes and a metrics stub on the ops port (§09).
package observability

import (
	"context"
	"net/http"

	"github.com/proseria-research/lineage/internal/domain"
)

// ReadinessChecker verifies a dependency is reachable (DB, cache, storage) for /readyz.
type ReadinessChecker func(ctx context.Context) error

// Handler returns a mux with /healthz, /readyz, and /metrics (§09.2, §09.3).
func Handler(ready ReadinessChecker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, req *http.Request) {
		if ready != nil {
			if err := ready(req.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		// TODO: Prometheus registry (§09.2). Placeholder exposition format.
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# HELP lineage_up 1 if the process is up\n# TYPE lineage_up gauge\nlineage_up 1\n"))
	})
	return mux
}

// StoreReady adapts a MetadataStore into a readiness check (a cheap list).
func StoreReady(store domain.MetadataStore) ReadinessChecker {
	return func(ctx context.Context) error {
		_, _, err := store.ListModels(ctx, domain.ListOptions{PageSize: 1})
		return err
	}
}
