package domain

import "context"

// Tracer starts spans at the boundaries worth timing (§09.4): the inbound request, the core
// operation, the store call. The real backend is an OpenTelemetry adapter exporting over
// OTLP; a no-op is used when tracing is off — so the core keeps depending only on this port,
// never on the OTel SDK (§01 dependency rule), exactly as it does for Meter.
type Tracer interface {
	// Start begins a span and returns a context carrying it. The caller must End the span.
	Start(ctx context.Context, name string) (context.Context, Span)
}

// Span is one timed operation. Attribute setters take the narrow types we actually record;
// a general attribute value type would leak the SDK's model into the port.
type Span interface {
	SetName(name string)
	SetString(key, value string)
	SetInt(key string, value int64)
	// RecordError marks the span failed. A nil error is ignored, so callers can defer it.
	RecordError(err error)
	// TraceID is the 32-hex trace id, or "" when tracing is off — the log correlation field.
	TraceID() string
	End()
}

// NopTracer is the default do-nothing Tracer: it returns the context unchanged, so a
// disabled tracer costs one interface call and no allocation.
type NopTracer struct{}

func (NopTracer) Start(ctx context.Context, _ string) (context.Context, Span) {
	return ctx, NopSpan{}
}

// NopSpan is the span a NopTracer hands back.
type NopSpan struct{}

func (NopSpan) SetName(string)           {}
func (NopSpan) SetString(string, string) {}
func (NopSpan) SetInt(string, int64)     {}
func (NopSpan) RecordError(error)        {}
func (NopSpan) TraceID() string          { return "" }
func (NopSpan) End()                     {}
