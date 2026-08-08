// Package observability provides the ops-port surface: health probes and Prometheus metrics
// (§09.2, §09.3). The metric registry itself is the dependency-free metrics subpackage.
package observability

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/proseria-research/lineage/internal/domain"
	"github.com/proseria-research/lineage/internal/observability/metrics"
)

// ReadinessChecker verifies a dependency is reachable (DB, storage) for /readyz.
type ReadinessChecker func(ctx context.Context) error

// Handler returns a mux with /healthz, /readyz, and /metrics (§09.2, §09.3). reg may be nil.
//
// health, if non-nil, is merged into /healthz's body — §19.4 requires the configured
// retention floor to be echoed there, so an operator can read what the process is actually
// running under rather than what a chart was believed to set.
func Handler(reg *metrics.Registry, ready ReadinessChecker, health map[string]any) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		// The body is now JSON rather than the bare "ok" it used to be. Probes read the
		// status code, so this is safe for Kubernetes; `status` is kept as a field so a
		// human or a script has the same word to look for.
		body := map[string]any{"status": "ok"}
		for k, v := range health {
			body[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
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
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if reg != nil {
			reg.Render(w)
			return
		}
		_, _ = w.Write([]byte("# HELP lineage_up 1 if the process is up\n# TYPE lineage_up gauge\nlineage_up 1\n"))
	})
	return mux
}

// Ready composes several readiness checks; the first failure gates traffic (§09.3).
func Ready(checks ...ReadinessChecker) ReadinessChecker {
	return func(ctx context.Context) error {
		for _, c := range checks {
			if c == nil {
				continue
			}
			if err := c(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}

// StoreReady checks the metadata store is reachable (a cheap list).
func StoreReady(store domain.MetadataStore) ReadinessChecker {
	return func(ctx context.Context) error {
		_, _, err := store.ListModels(ctx, domain.ListOptions{PageSize: 1})
		return err
	}
}

// StorageReady checks the default storage backend answers a Stat (proves reachability and,
// for signing backends, credentials). A missing object (exists=false, no error) is healthy.
func StorageReady(backend domain.StorageBackend) ReadinessChecker {
	return func(ctx context.Context) error {
		_, err := backend.Stat(ctx, "readyz-probe/.keep")
		return err
	}
}
