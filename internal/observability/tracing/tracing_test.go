package tracing_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
	"github.com/proseria-research/lineage/internal/observability/tracing"
)

// collector is a stub OTLP/HTTP endpoint: it records what the exporter actually posted, so the
// tests below exercise the real export path rather than a mock of it.
type collector struct {
	srv *httptest.Server

	mu    sync.Mutex
	paths []string
	bytes int
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.paths = append(c.paths, r.URL.Path)
		c.bytes += len(body)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *collector) got() ([]string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.paths...), c.bytes
}

func TestDisabledWithoutEndpoint(t *testing.T) {
	tr, shutdown, err := tracing.Start(context.Background(), tracing.Config{ServiceName: "lineage"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	if tr.Enabled() {
		t.Fatal("no endpoint configured, tracer should be disabled")
	}
	ctx, sp := tr.Start(context.Background(), "core.Resolve")
	defer sp.End()
	if sp.TraceID() != "" {
		t.Errorf("disabled span reported trace id %q, want empty", sp.TraceID())
	}
	if ctx != context.Background() {
		t.Error("disabled tracer should return the context unchanged")
	}
	// Recording on a no-op span must not panic — the core calls these unconditionally.
	sp.SetName("x")
	sp.SetString("k", "v")
	sp.SetInt("n", 1)
	sp.RecordError(errors.New("boom"))
}

func TestSpansExportOverOTLP(t *testing.T) {
	c := newCollector(t)
	tr, shutdown, err := tracing.Start(context.Background(), tracing.Config{
		Endpoint: c.srv.URL, ServiceName: "lineage", Version: "test", SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !tr.Enabled() {
		t.Fatal("endpoint configured, tracer should be enabled")
	}

	_, sp := tr.Start(context.Background(), "core.Resolve")
	sp.SetString("lineage.model", "fraud-detector")
	sp.SetInt("lineage.size_bytes", 42)
	if sp.TraceID() == "" {
		t.Error("recording span should carry a trace id")
	}
	sp.End()

	// Shutdown flushes the batch processor; without it the span is still queued.
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	paths, n := c.got()
	if len(paths) == 0 {
		t.Fatal("collector received no export request")
	}
	if paths[0] != "/v1/traces" {
		t.Errorf("exported to %q, want /v1/traces", paths[0])
	}
	if n == 0 {
		t.Error("export request had an empty body")
	}
}

// A caller's traceparent must be adopted, otherwise a request crossing into Lineage starts a
// second, disconnected trace and the correlation §09.4 promises is broken.
func TestStartHTTPContinuesUpstreamTrace(t *testing.T) {
	c := newCollector(t)
	tr, shutdown, err := tracing.Start(context.Background(), tracing.Config{
		Endpoint: c.srv.URL, ServiceName: "lineage", SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	const upstream = "4bf92f3577b34da6a3ce929d0e0e4736"
	r := httptest.NewRequest(http.MethodGet, "/v1/models/fraud-detector", nil)
	r.Header.Set("traceparent", "00-"+upstream+"-00f067aa0ba902b7-01")

	_, sp := tr.StartHTTP(r, http.MethodGet)
	defer sp.End()

	if got := sp.TraceID(); got != upstream {
		t.Errorf("trace id = %q, want the upstream %q", got, upstream)
	}
}

func TestStartHTTPMintsTraceWithoutUpstream(t *testing.T) {
	c := newCollector(t)
	tr, shutdown, err := tracing.Start(context.Background(), tracing.Config{
		Endpoint: c.srv.URL, ServiceName: "lineage", SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	_, sp := tr.StartHTTP(r, http.MethodGet)
	defer sp.End()

	if sp.TraceID() == "" {
		t.Error("a request with no traceparent should still get a minted trace id")
	}
}

func TestDisabledStartHTTPReturnsRequestContext(t *testing.T) {
	tr, _, err := tracing.Start(context.Background(), tracing.Config{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx, sp := tr.StartHTTP(r, http.MethodGet)
	defer sp.End()
	if ctx != r.Context() {
		t.Error("disabled StartHTTP should hand back the request context unchanged")
	}
}

// Ensure the port's own no-op satisfies the same contract the core relies on.
var _ domain.Tracer = domain.NopTracer{}
