// Package tracing is the OpenTelemetry adapter behind domain.Tracer (§09.4). It is the only
// package that imports the OTel SDK: the core and the API see the port, this package owns the
// provider, the OTLP exporter, and W3C propagation.
//
// Tracing is off unless an endpoint is configured. Off means a disabled Tracer handing back
// no-op spans — no provider, no exporter goroutines, no background batching.
package tracing

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/proseria-research/lineage/internal/domain"
)

// Config selects the collector and the sampling rate (§08.6 renders it from values).
type Config struct {
	Endpoint    string  // OTLP/HTTP collector; empty disables tracing entirely
	ServiceName string  // resource service.name
	Version     string  // resource service.version
	SampleRatio float64 // head sampling, parent-based; 1 = always, 0 = never
}

// Shutdown flushes pending spans and stops the provider. Always non-nil, so callers can defer
// it without a nil check.
type Shutdown func(context.Context) error

// Start builds the tracer. With no endpoint it returns a disabled Tracer and a no-op shutdown,
// which is the default posture: correlation IDs alone (§09.8) until a collector exists.
//
// It always returns a *Tracer, enabled or not, so callers wire one value into both the core
// port and the HTTP middleware without branching on whether tracing is on.
func Start(ctx context.Context, cfg Config) (*Tracer, Shutdown, error) {
	noop := func(context.Context) error { return nil }
	if cfg.Endpoint == "" {
		return &Tracer{}, noop, nil
	}

	exp, err := newExporter(ctx, cfg.Endpoint)
	if err != nil {
		return &Tracer{}, noop, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.Version),
	))
	if err != nil {
		return &Tracer{}, noop, fmt.Errorf("otlp resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		// Parent-based: an upstream sampling decision wins, so a trace is never half-recorded.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	return &Tracer{t: tp.Tracer(cfg.ServiceName), prop: otel.GetTextMapPropagator()}, tp.Shutdown, nil
}

// newExporter accepts either a bare host:port or a full URL. A bare host:port is treated as
// plaintext, which is the in-cluster collector case (§08); https:// opts into TLS.
func newExporter(ctx context.Context, endpoint string) (*otlptrace.Exporter, error) {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	}
	return otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
}

// Tracer implements domain.Tracer over the OTel SDK and, for the HTTP edge, extracts an
// upstream W3C context so a caller's trace continues through Lineage. Its zero value is a
// disabled tracer that hands back no-op spans.
type Tracer struct {
	t    trace.Tracer
	prop propagation.TextMapPropagator
}

var _ domain.Tracer = (*Tracer)(nil)

// Enabled reports whether spans are actually being recorded and exported.
func (x *Tracer) Enabled() bool { return x != nil && x.t != nil }

func (x *Tracer) Start(ctx context.Context, name string) (context.Context, domain.Span) {
	if !x.Enabled() {
		return ctx, domain.NopSpan{}
	}
	ctx, s := x.t.Start(ctx, name)
	return ctx, span{s}
}

// StartHTTP begins the server span for an inbound request, continuing the upstream trace when
// `traceparent` is present. The final span name is set by the caller once the mux has matched
// a route, so span names stay templated (bounded cardinality, same rule as the metric labels).
func (x *Tracer) StartHTTP(r *http.Request, name string) (context.Context, domain.Span) {
	if !x.Enabled() {
		return r.Context(), domain.NopSpan{}
	}
	ctx := x.prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, s := x.t.Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer))
	return ctx, span{s}
}

type span struct{ s trace.Span }

func (w span) SetName(name string)      { w.s.SetName(name) }
func (w span) SetString(k, v string)    { w.s.SetAttributes(attribute.String(k, v)) }
func (w span) SetInt(k string, v int64) { w.s.SetAttributes(attribute.Int64(k, v)) }
func (w span) End()                     { w.s.End() }
func (w span) TraceID() string {
	sc := w.s.SpanContext()
	if !sc.HasTraceID() {
		return ""
	}
	return sc.TraceID().String()
}

func (w span) RecordError(err error) {
	if err == nil {
		return
	}
	w.s.RecordError(err)
	w.s.SetStatus(codes.Error, err.Error())
}
