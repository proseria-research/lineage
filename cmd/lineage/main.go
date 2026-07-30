// Command lineage is the single Lineage binary: it serves the Admin UI (:8080) and the
// Model API (:8081) over one shared domain core, plus an ops port for health/metrics.
// See docs/01-architecture-overview.md.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	pgstore "github.com/proseria-research/lineage/internal/adapters/store/postgres"
	sqlitestore "github.com/proseria-research/lineage/internal/adapters/store/sqlite"
	"github.com/proseria-research/lineage/internal/api/adminui"
	"github.com/proseria-research/lineage/internal/api/modelapi"
	"github.com/proseria-research/lineage/internal/config"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
	"github.com/proseria-research/lineage/internal/observability"
)

func main() {
	cfg := config.Load()
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("lineage ")

	// Wire adapters into the ports (§01 dependency rule). The store is chosen per-dialect
	// at startup (§02.7); memory is a dependency-free fallback.
	store, err := openStore(cfg)
	if err != nil {
		log.Fatalf("open store (%s): %v", cfg.DBEngine, err)
	}
	log.Printf("metadata store: %s", cfg.DBEngine)
	backend := fs.New("default", cfg.StorageRoot)
	backends := map[string]domain.StorageBackend{backend.Name(): backend}
	cache := memcache.New()
	bus := events.New()

	svc := core.New(store, backends, backend.Name(), cache, bus)

	servers := []*http.Server{
		{Addr: cfg.ModelAPIAddr, Handler: logging("model-api", modelapi.New(svc, cfg.ActorHeader).Handler())},
		{Addr: cfg.AdminAddr, Handler: logging("admin-ui", adminui.New(svc).Handler())},
		{Addr: cfg.MetricsAddr, Handler: observability.Handler(observability.StoreReady(store))},
	}
	names := []string{"model-api " + cfg.ModelAPIAddr, "admin-ui " + cfg.AdminAddr, "ops " + cfg.MetricsAddr}

	for i, s := range servers {
		go func(s *http.Server, name string) {
			log.Printf("listening: %s", name)
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("server %s: %v", name, err)
			}
		}(s, names[i])
	}

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Print("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(ctx)
	}
}

// openStore selects the MetadataStore adapter from config (§02.7).
func openStore(cfg config.Config) (domain.MetadataStore, error) {
	switch cfg.DBEngine {
	case "postgres":
		return pgstore.New(cfg.DBPath)
	case "memory":
		return memstore.New(), nil
	case "sqlite", "":
		return sqlitestore.New(cfg.DBPath)
	default:
		return nil, errUnknownEngine(cfg.DBEngine)
	}
}

type errUnknownEngine string

func (e errUnknownEngine) Error() string { return "unknown db engine: " + string(e) }

// logging is a minimal structured-ish request log middleware (real one: §09.4).
func logging(surface string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		log.Printf("surface=%s method=%s path=%s status=%d latency=%s",
			surface, r.Method, r.URL.Path, sw.status, time.Since(start))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
