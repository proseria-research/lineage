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
	"strings"
	"syscall"
	"time"

	"github.com/proseria-research/lineage/internal/adapters/cache/memory"
	rediscache "github.com/proseria-research/lineage/internal/adapters/cache/redis"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	"github.com/proseria-research/lineage/internal/adapters/storage/oci"
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
	"github.com/proseria-research/lineage/internal/observability/tracing"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	cfg, cfgErr := config.Load()
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("□ ") // the Lineage mark leads every operational log line

	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		log.Printf("lineage %s", version)
		return
	}
	// After --version (which must work regardless) and before anything opens a database: a
	// config value nobody can interpret is a startup failure, not something to run with a
	// guess at (§19.4).
	if cfgErr != nil {
		log.Fatalf("configuration: %v", cfgErr)
	}

	// `lineage migrate` opens the store (which runs the embedded migrator) and exits — the
	// Helm pre-upgrade hook Job (§08.5). App pods start only after it succeeds.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate(cfg)
		return
	}

	log.Printf("Lineage %s — self-hostable AI model registry", version)

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
	caps := backend.Capabilities()
	log.Printf("storage backend: %s (signGet=%v signPut=%v multipart=%v)",
		cfg.StorageDriver, caps.Signing, caps.SignPut, caps.Multipart)
	backends := map[string]domain.StorageBackend{backend.Name(): backend}
	cache, err := openCache(cfg)
	if err != nil {
		log.Fatalf("open cache (%s): %v", cfg.Cache.Engine, err)
	}
	log.Printf("resolution cache: %s", cfg.Cache.Engine)
	bus := events.New()

	// Telemetry (§09.2, §09.4): the metric registry is a Meter for the core and a RED recorder
	// for the API middleware; domain + DB gauges are refreshed at scrape time. Tracing is off
	// unless a collector endpoint is configured.
	m := metrics.NewApp()
	tracer, flushTraces, err := tracing.Start(context.Background(), tracing.Config{
		Endpoint:    cfg.Tracing.Endpoint,
		ServiceName: cfg.Tracing.ServiceName,
		Version:     version,
		SampleRatio: cfg.Tracing.SampleRatio,
	})
	if err != nil {
		// A missing collector must not stop the registry from serving; run untraced and say so.
		log.Printf("tracing disabled: %v", err)
	}
	if tracer.Enabled() {
		log.Printf("tracing: OTLP → %s (sample=%.2f)", cfg.Tracing.Endpoint, cfg.Tracing.SampleRatio)
	}

	// Spans wrap the store through the port, so SQLite/Postgres/memory are all covered and the
	// adapters stay telemetry-free. Readiness keeps the undecorated store: probing every few
	// seconds is not worth a span each time.
	svc := core.New(tracing.Store(store, tracer), backends, backend.Name(), cache, bus,
		core.WithMeter(m), core.WithTracer(tracer),
		core.WithRetention(cfg.Retention), core.WithAttestation(cfg.Attestation))
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

	// Background jobs share one cancellable root, stopped on shutdown.
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	// Merkle epoch sealer (§19.5); no-op when attestation is off. Enabling it and running
	// the sealer are one decision, made here — an install that stamped epochs nothing sealed
	// would report a growing unsealed backlog forever.
	svc.RunSealer(rootCtx)
	if cfg.Attestation.Enabled {
		log.Printf("attestation: sealing enabled (interval=%ds grace=%ds)",
			cfg.Attestation.SealIntervalSeconds, cfg.Attestation.SealGraceSeconds)
	}

	// Optional artifact GC sweeper (§05.8); no-op unless LINEAGE_STORAGE_GC=sweep.
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
		{Addr: cfg.ModelAPIAddr, Handler: api.Telemetry("model-api", m, tracer, cfg.ActorHeader, modelapi.New(svc, cfg.ActorHeader).Handler())},
		{Addr: cfg.AdminAddr, Handler: api.Telemetry("admin-ui", m, tracer, cfg.ActorHeader, adminui.New(svc, cfg.ActorHeader).WithClientInfo(adminui.ClientInfo{
			ModelAPIURL: cfg.PublicModelAPIURL, ModelAPIPort: portOf(cfg.ModelAPIAddr), DocsURL: cfg.DocsURL,
		}).Handler())},
		{Addr: cfg.MetricsAddr, Handler: observability.Handler(m.Registry(), ready, map[string]any{"retention": svc.Retention(), "auditAttestation": svc.Attestation()})},
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
	cancelRoot() // stop the sealer and the GC sweeper
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(ctx)
	}
	// Flush buffered spans last: the batch processor holds the final requests' spans, and
	// dropping them would lose exactly the traces from a shutdown worth investigating.
	if err := flushTraces(ctx); err != nil {
		log.Printf("tracing flush: %v", err)
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

// openCache selects the ResolutionCache adapter from config (§04.4). memory is the
// dependency-free default; redis is the shared prod cache.
func openCache(cfg config.Config) (domain.ResolutionCache, error) {
	switch cfg.Cache.Engine {
	case "redis":
		return rediscache.New(cfg.Cache.RedisAddr, cfg.Cache.RedisPassword, cfg.Cache.RedisDB)
	case "memory", "":
		return memcache.New(), nil
	default:
		return nil, errUnknownEngine(cfg.Cache.Engine)
	}
}

// openStorage selects the StorageBackend adapter from config (§05.3). fs is the
// zero-config dev/air-gapped default; s3 covers any S3-compatible object store; oci stores
// each version as one manifest in an OCI registry.
func openStorage(cfg config.Config) (domain.StorageBackend, error) {
	switch cfg.StorageDriver {
	case "oci":
		return oci.New("default", oci.Config{
			Registry: cfg.OCI.Registry, Repository: cfg.OCI.Repository,
			Username: cfg.OCI.Username, Password: cfg.OCI.Password,
			PlainHTTP: cfg.OCI.PlainHTTP,
		})
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

// portOf is the port in a listen address like ":8081" or "0.0.0.0:8081", for the console to
// guess the Model API's URL when LINEAGE_PUBLIC_MODEL_API_URL is not set.
func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return ""
}
