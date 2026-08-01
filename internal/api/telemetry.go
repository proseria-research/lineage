package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// HTTPRecorder is the metrics sink the middleware feeds (implemented by metrics.App). Kept as
// a local interface so the api package doesn't depend on the metrics package (§01).
type HTTPRecorder interface {
	RecordHTTP(surface, route, method, status string, seconds float64)
}

// ServerTracer starts the span for an inbound request, continuing any upstream W3C trace
// (implemented by tracing.Tracer). Local interface for the same reason as HTTPRecorder — the
// api package sees the port, never the OTel SDK.
type ServerTracer interface {
	StartHTTP(r *http.Request, name string) (context.Context, domain.Span)
}

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID returns the correlation id assigned to the request, if any (§09.4).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Telemetry wraps a handler with correlation ids, RED metrics, a server span, and a structured
// JSON access log (§09.4). surface is "model-api" | "admin-ui". Secrets, signed URLs, and bytes
// are never logged or put on a span. rec may be nil (metrics disabled) and tr may be nil
// (tracing disabled) — the two are independent.
func Telemetry(surface string, rec HTTPRecorder, tr ServerTracer, actorHeader string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// The span opens before the mux has matched, so it starts under the method and is
		// renamed to the templated route below — span names stay bounded, like the metric labels.
		ctx, sp := r.Context(), domain.Span(domain.NopSpan{})
		if tr != nil {
			ctx, sp = tr.StartHTTP(r, r.Method)
		}
		defer sp.End()

		reqID := requestIDFor(r, sp)
		w.Header().Set("X-Request-Id", reqID)
		r2 := r.WithContext(context.WithValue(ctx, requestIDKey, reqID))

		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r2)

		route := routeLabel(r2.Pattern)
		dur := time.Since(start)
		if rec != nil {
			rec.RecordHTTP(surface, route, r.Method, strconv.Itoa(sw.status), dur.Seconds())
		}

		sp.SetName(r.Method + " " + route)
		sp.SetString("http.request.method", r.Method)
		sp.SetString("http.route", route)
		sp.SetString("lineage.surface", surface)
		sp.SetInt("http.response.status_code", int64(sw.status))
		if sw.status >= 500 {
			// The handler already turned the cause into a problem document; the span carries the
			// fact of failure so a trace search on errors finds it.
			sp.RecordError(fmt.Errorf("%s %s responded %d", r.Method, route, sw.status))
		}

		slog.Info("request",
			"surface", surface,
			"method", r.Method,
			"route", route,
			"path", r.URL.Path,
			"status", sw.status,
			"latencyMs", dur.Milliseconds(),
			"requestId", reqID,
			"actor", r.Header.Get(actorHeader),
			"traceId", traceIDFor(r, sp),
		)
	})
}

// routeLabel is the matched ServeMux pattern without its method prefix (bounded cardinality),
// e.g. "GET /v1/models/{model}" → "/v1/models/{model}"; empty (no match) → "unmatched".
func routeLabel(pattern string) string {
	if pattern == "" {
		return "unmatched"
	}
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		return pattern[i+1:]
	}
	return pattern
}

// requestIDFor reuses an inbound X-Request-Id, else the trace id, else mints one.
func requestIDFor(r *http.Request, sp domain.Span) string {
	if id := r.Header.Get("X-Request-Id"); id != "" {
		return id
	}
	if tid := traceIDFor(r, sp); tid != "" {
		return tid
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// traceIDFor prefers the live span's trace id — with tracing on it is authoritative, since a
// request with no upstream traceparent still gets a freshly minted trace. It falls back to the
// inbound header so correlation keeps working with tracing disabled (§09.8).
func traceIDFor(r *http.Request, sp domain.Span) string {
	if tid := sp.TraceID(); tid != "" {
		return tid
	}
	return traceparentID(r)
}

// traceparentID extracts the 32-hex trace-id from a W3C traceparent header
// (00-<trace>-<span>-<flags>).
func traceparentID(r *http.Request) string {
	tp := r.Header.Get("traceparent")
	parts := strings.Split(tp, "-")
	if len(parts) == 4 && len(parts[1]) == 32 {
		return parts[1]
	}
	return ""
}

// statusRecorder captures the response status for logging/metrics and passes Flush through
// so streaming responses (e.g. /content) still flush.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.written = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.written {
		s.written = true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
