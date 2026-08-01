package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/domain"
)

// fakeTracer captures the one server span the middleware creates.
type fakeTracer struct{ last *fakeSpan }

func (f *fakeTracer) StartHTTP(r *http.Request, name string) (context.Context, domain.Span) {
	f.last = &fakeSpan{name: name, attrs: map[string]string{}, ints: map[string]int64{}}
	return r.Context(), f.last
}

type fakeSpan struct {
	name    string
	attrs   map[string]string
	ints    map[string]int64
	errored bool
	ended   bool
}

func (s *fakeSpan) SetName(n string)         { s.name = n }
func (s *fakeSpan) SetString(k, v string)    { s.attrs[k] = v }
func (s *fakeSpan) SetInt(k string, v int64) { s.ints[k] = v }
func (s *fakeSpan) TraceID() string          { return "0af7651916cd43dd8448eb211c80319c" }
func (s *fakeSpan) End()                     { s.ended = true }
func (s *fakeSpan) RecordError(err error)    { s.errored = s.errored || err != nil }

func serve(t *testing.T, tr api.ServerTracer, h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /v1/models/{model}", h)
	w := httptest.NewRecorder()
	api.Telemetry("model-api", nil, tr, "X-Lineage-Actor", mux).ServeHTTP(w, r)
	return w
}

// The span opens before the mux matches, so it must be renamed to the templated route —
// otherwise every distinct model name becomes its own span name.
func TestServerSpanUsesTemplatedRoute(t *testing.T) {
	tr := &fakeTracer{}
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	serve(t, tr, h, httptest.NewRequest(http.MethodGet, "/v1/models/fraud-detector", nil))

	sp := tr.last
	if sp == nil {
		t.Fatal("no span was started")
	}
	if want := "GET /v1/models/{model}"; sp.name != want {
		t.Errorf("span name = %q, want %q", sp.name, want)
	}
	if got := sp.attrs["http.route"]; got != "/v1/models/{model}" {
		t.Errorf("http.route = %q, want the templated pattern", got)
	}
	if got := sp.attrs["lineage.surface"]; got != "model-api" {
		t.Errorf("lineage.surface = %q, want model-api", got)
	}
	if got := sp.ints["http.response.status_code"]; got != 200 {
		t.Errorf("status attribute = %d, want 200", got)
	}
	if sp.errored {
		t.Error("a 200 response marked the span as errored")
	}
	if !sp.ended {
		t.Error("span was never ended")
	}
}

func TestServerSpanMarksServerErrors(t *testing.T) {
	tr := &fakeTracer{}
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	serve(t, tr, h, httptest.NewRequest(http.MethodGet, "/v1/models/fraud-detector", nil))

	if !tr.last.errored {
		t.Error("a 500 response should mark the span errored")
	}
}

// A 4xx is the caller's fault, not a server failure; marking it errored would drown the
// signal §09.5 alerts on.
func TestClientErrorsDoNotMarkSpan(t *testing.T) {
	tr := &fakeTracer{}
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	serve(t, tr, h, httptest.NewRequest(http.MethodGet, "/v1/models/fraud-detector", nil))

	if tr.last.errored {
		t.Error("a 404 should not be recorded as a span error")
	}
}

// With no tracer the middleware still has to serve, echo a request id, and not panic.
func TestTelemetryWorksWithoutTracer(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	w := serve(t, nil, h, httptest.NewRequest(http.MethodGet, "/v1/models/fraud-detector", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Header().Get("X-Request-Id") == "" {
		t.Error("X-Request-Id was not echoed")
	}
}

// An inbound X-Request-Id wins over a minted one, so a caller's id survives end to end.
func TestInboundRequestIDIsPreserved(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r := httptest.NewRequest(http.MethodGet, "/v1/models/fraud-detector", nil)
	r.Header.Set("X-Request-Id", "caller-supplied")
	w := serve(t, &fakeTracer{}, h, r)

	if got := w.Header().Get("X-Request-Id"); got != "caller-supplied" {
		t.Errorf("X-Request-Id = %q, want the inbound value", got)
	}
}

// With tracing on and no inbound id, the request id falls back to the span's trace id so logs
// and traces join on one value (§09.4).
func TestRequestIDFallsBackToTraceID(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	w := serve(t, &fakeTracer{}, h, httptest.NewRequest(http.MethodGet, "/v1/models/fraud-detector", nil))

	if got := w.Header().Get("X-Request-Id"); got != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("X-Request-Id = %q, want the span trace id", got)
	}
}
