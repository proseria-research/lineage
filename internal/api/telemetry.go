package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPRecorder is the metrics sink the middleware feeds (implemented by metrics.App). Kept as
// a local interface so the api package doesn't depend on the metrics package (§01).
type HTTPRecorder interface {
	RecordHTTP(surface, route, method, status string, seconds float64)
}

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID returns the correlation id assigned to the request, if any (§09.4).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Telemetry wraps a handler with correlation ids, RED metrics, and a structured JSON access
// log (§09.4). surface is "model-api" | "admin-ui". Secrets, signed URLs, and bytes are never
// logged. rec may be nil (metrics disabled).
func Telemetry(surface string, rec HTTPRecorder, actorHeader string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := requestID(r)
		w.Header().Set("X-Request-Id", reqID)
		r2 := r.WithContext(context.WithValue(r.Context(), requestIDKey, reqID))

		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r2)

		route := routeLabel(r2.Pattern)
		dur := time.Since(start)
		if rec != nil {
			rec.RecordHTTP(surface, route, r.Method, strconv.Itoa(sw.status), dur.Seconds())
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
			"traceId", traceID(r),
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

// requestID reuses an inbound X-Request-Id or the traceparent trace-id, else mints one.
func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-Id"); id != "" {
		return id
	}
	if tid := traceID(r); tid != "" {
		return tid
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// traceID extracts the 32-hex trace-id from a W3C traceparent header (00-<trace>-<span>-<flags>).
func traceID(r *http.Request) string {
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
