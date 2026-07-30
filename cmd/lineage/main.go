// Command lineage is the single Lineage binary: it serves the Admin UI (:8080) and the
// Model API (:8081) over one shared domain core, plus an ops port for health/metrics.
// See docs/01-architecture-overview.md.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	"github.com/proseria-research/lineage/internal/adapters/storage/s3"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	pgstore "github.com/proseria-research/lineage/internal/adapters/store/postgres"
	sqlitestore "github.com/proseria-research/lineage/internal/adapters/store/sqlite"
	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/api/adminui"
	"github.com/proseria-research/lineage/internal/api/modelapi"
	"github.com/proseria-research/lineage/internal/config"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
	"github.com/proseria-research/lineage/internal/observability"
	"github.com/proseria-research/lineage/internal/observability/metrics"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	cfg := config.Load()
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("lineage ")

	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		log.Printf("lineage %s", version)
		return
	}

	// `lineage migrate` opens the store (which runs the embedded migrator) and exits — the
	// Helm pre-upgrade hook Job (§08.5). App pods start only after it succeeds.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate(cfg)
		return
	}

	// Request access logs are structured JSON on stderr (§09.4).
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Wire adapters into the ports (§01 dependency rule). The store is chosen per-dialect
	// at startup (§02.7); memory is a dependency-free fallback.
	store, err := openStore(cfg)
	if err != nil {
		log.Fatalf("open store (%s): %v", cfg.DBEngine, err)
	}
	log.Printf("metadata store: %s", cfg.DBEngine)
	backend, err := openStorage(cfg)
	if err != nil {
		log.Fatalf("open storage (%s): %v", cfg.StorageDriver, err)
	}
	log.Printf("storage backend: %s (signing=%v)", cfg.StorageDriver, backend.Capabilities().Signing)
	backends := map[string]domain.StorageBackend{backend.Name(): backend}
	cache := memcache.New()
	bus := events.New()

	// Telemetry (§09.2): the metric registry is a Meter for the core and a RED recorder for
	// the API middleware; domain + DB gauges are refreshed at scrape time.
	m := metrics.NewApp()
	svc := core.New(store, backends, backend.Name(), cache, bus, core.WithMeter(m))
	m.BindDomainGauges(func(ctx context.Context) metrics.DomainStats {
		st, _ := svc.Stats(ctx)
		return metrics.DomainStats{
			Models: st.Models, Versions: st.Versions, Artifacts: st.Artifacts,
			Deployments: st.Deployments, Stages: stageStrings(st.Stages),
		}
	})
	if d, ok := store.(interface{ DB() *sql.DB }); ok {
		m.BindDBGauges(d.DB())
	}

	// Optional artifact GC sweeper (§05.8); no-op unless LINEAGE_STORAGE_GC=sweep.
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	svc.RunGC(rootCtx, core.GCConfig{
		Enabled:  cfg.GC.Mode == "sweep",
		Prefix:   cfg.GC.Prefix,
		Grace:    cfg.GC.Grace,
		Interval: cfg.GC.Interval,
	})
	if cfg.GC.Mode == "sweep" {
		log.Printf("gc: sweep enabled (grace=%s interval=%s prefix=%q)", cfg.GC.Grace, cfg.GC.Interval, cfg.GC.Prefix)
	}

	ready := observability.Ready(observability.StoreReady(store), observability.StorageReady(backend))
	servers := []*http.Server{
		{Addr: cfg.ModelAPIAddr, Handler: api.Telemetry("model-api", m, cfg.ActorHeader, modelapi.New(svc, cfg.ActorHeader).Handler())},
		{Addr: cfg.AdminAddr, Handler: api.Telemetry("admin-ui", m, cfg.ActorHeader, adminui.New(svc).Handler())},
		{Addr: cfg.MetricsAddr, Handler: observability.Handler(m.Registry(), ready)},
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
	cancelRoot() // stop the GC sweeper
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(ctx)
	}
}

// runMigrate applies pending migrations and exits (§08.5). openStore runs the embedded
// per-dialect migrator; SQLite also migrates at normal startup, so this is chiefly for Postgres.
func runMigrate(cfg config.Config) {
	store, err := openStore(cfg)
	if err != nil {
		log.Fatalf("migrate (%s): %v", cfg.DBEngine, err)
	}
	if c, ok := store.(interface{ Close() error }); ok {
		_ = c.Close()
	}
	log.Printf("migrations applied (%s)", cfg.DBEngine)
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

// openStorage selects the StorageBackend adapter from config (§05.3). fs is the
// zero-config dev/air-gapped default; s3 covers any S3-compatible object store.
func openStorage(cfg config.Config) (domain.StorageBackend, error) {
	switch cfg.StorageDriver {
	case "s3":
		return s3.New("default", s3.Config{
			Bucket: cfg.S3.Bucket, Region: cfg.S3.Region, Endpoint: cfg.S3.Endpoint,
			AccessKey: cfg.S3.AccessKey, SecretKey: cfg.S3.SecretKey,
			SessionToken: cfg.S3.SessionToken, PathStyle: cfg.S3.PathStyle,
		})
	case "fs", "":
		return fs.New("default", cfg.StorageRoot), nil
	default:
		return nil, errUnknownEngine(cfg.StorageDriver)
	}
}

type errUnknownEngine string

func (e errUnknownEngine) Error() string { return "unknown driver/engine: " + string(e) }

func stageStrings(m map[domain.Stage]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}
