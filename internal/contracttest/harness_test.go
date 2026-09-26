package contracttest_test

// The server half. Unlike the client half (the package proper), this file may import
// anything: it builds a real registry — real router, real store, real core — behind an
// httptest server, the way cmd/lineage composes one.

import (
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	pgstore "github.com/proseria-research/lineage/internal/adapters/store/postgres"
	sqlitestore "github.com/proseria-research/lineage/internal/adapters/store/sqlite"
	"github.com/proseria-research/lineage/internal/adapters/store/sqlstore"
	"github.com/proseria-research/lineage/internal/api/adminui"
	"github.com/proseria-research/lineage/internal/api/modelapi"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// dialects is every MetadataStore adapter a production install can run on (§02.7).
var dialects = []string{"sqlite", "postgres"}

// registry is one running install under test.
type registry struct {
	ModelAPI *httptest.Server // /v1
	Admin    *httptest.Server // console BFF
	Svc      *core.Service    // for the server-side half of a test only — never the client
}

type registryOpts struct {
	actorHeader string
	backends    map[string]domain.StorageBackend
	defBackend  string
}

// newRegistry stands up a registry on the named dialect, configured as the chart ships it:
// a retention floor and audit attestation on (§19.4).
func newRegistry(t *testing.T, dialect string, o registryOpts) *registry {
	t.Helper()
	if o.actorHeader == "" {
		o.actorHeader = "X-Lineage-Actor"
	}
	if o.backends == nil {
		b := fs.New("default", t.TempDir())
		o.backends, o.defBackend = map[string]domain.StorageBackend{b.Name(): b}, b.Name()
	}
	store := openStore(t, dialect)
	svc := core.New(store, o.backends, o.defBackend, memcache.New(), events.New(),
		core.WithRetention(domain.RetentionConfig{MinAuditAgeDays: 3650, MinArchivedVersionDays: 3650}),
		core.WithAttestation(domain.DefaultAttestation))
	r := &registry{
		ModelAPI: httptest.NewServer(modelapi.New(svc, o.actorHeader).Handler()),
		Admin:    httptest.NewServer(adminui.New(svc, o.actorHeader).Handler()),
		Svc:      svc,
	}
	t.Cleanup(r.ModelAPI.Close)
	t.Cleanup(r.Admin.Close)
	return r
}

func openStore(t *testing.T, dialect string) *sqlstore.Store {
	t.Helper()
	var (
		s   *sqlstore.Store
		err error
	)
	switch dialect {
	case "sqlite":
		s, err = sqlitestore.New(filepath.Join(t.TempDir(), "lineage.db"))
	case "postgres":
		s, err = pgstore.New(freshPGDatabase(t))
	default:
		t.Fatalf("unknown dialect %q", dialect)
	}
	if err != nil {
		t.Fatalf("open %s store: %v", dialect, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ---- Postgres: one server per package run, one database per test ----

var (
	pgOnce  sync.Once
	pgAdmin string // DSN of a database we may CREATE DATABASE from
	pgErr   error
	pgStop  func() error
	pgSeq   atomic.Int64
)

// TestMain stops the embedded Postgres, if one was started, after every test has run.
func TestMain(m *testing.M) {
	code := m.Run()
	if pgStop != nil {
		_ = pgStop()
	}
	os.Exit(code)
}

// freshPGDatabase returns the DSN of a new, empty database: LINEAGE_TEST_PG's server if set,
// otherwise an embedded Postgres started on first use. Skips if neither is available, as the
// pgstore suite does.
func freshPGDatabase(t *testing.T) string {
	t.Helper()
	pgOnce.Do(startPG)
	if pgErr != nil {
		t.Skipf("postgres unavailable (set LINEAGE_TEST_PG to use an external one): %v", pgErr)
	}
	admin, err := sql.Open("pgx", pgAdmin)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("contract_%d_%d", os.Getpid(), pgSeq.Add(1))
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, _ := url.Parse(pgAdmin)
	u.Path = "/" + name
	return u.String()
}

func startPG() {
	if dsn := os.Getenv("LINEAGE_TEST_PG"); dsn != "" {
		pgAdmin = dsn
		return
	}
	// Its own port and runtime directory: `go test ./...` runs packages in parallel, and the
	// pgstore suite runs its own embedded server on 15432 with the default runtime path.
	port, err := freePort()
	if err != nil {
		pgErr = err
		return
	}
	dir, err := os.MkdirTemp("", "lineage-contract-pg-")
	if err != nil {
		pgErr = err
		return
	}
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Username("lineage").Password("lineage").Database("lineage").
		Port(port).RuntimePath(dir).Logger(io.Discard))
	if err := pg.Start(); err != nil {
		pgErr = err
		_ = os.RemoveAll(dir)
		return
	}
	pgStop = func() error {
		defer os.RemoveAll(dir)
		return pg.Stop()
	}
	pgAdmin = fmt.Sprintf("postgres://lineage:lineage@localhost:%d/lineage?sslmode=disable", port)
}

func freePort() (uint32, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}

// noRedirect is an http.Client that surfaces a 302 instead of following it.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
