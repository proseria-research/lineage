package tracing_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/domain"
	"github.com/proseria-research/lineage/internal/observability/tracing"
)

// recorder is a domain.Tracer that keeps what was traced, so these tests assert on span names
// and outcomes without standing up an exporter.
type recorder struct {
	mu     sync.Mutex
	names  []string
	failed []string
}

func (r *recorder) Start(ctx context.Context, name string) (context.Context, domain.Span) {
	r.mu.Lock()
	r.names = append(r.names, name)
	r.mu.Unlock()
	return ctx, &recSpan{r: r, name: name}
}

func (r *recorder) spans() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...)
}

func (r *recorder) errored() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.failed...)
}

type recSpan struct {
	r    *recorder
	name string
}

func (s *recSpan) SetName(n string)         { s.name = n }
func (s *recSpan) SetString(string, string) {}
func (s *recSpan) SetInt(string, int64)     {}
func (s *recSpan) TraceID() string          { return "trace" }
func (s *recSpan) End()                     {}
func (s *recSpan) RecordError(err error) {
	if err == nil {
		return
	}
	s.r.mu.Lock()
	s.r.failed = append(s.r.failed, s.name)
	s.r.mu.Unlock()
}

func has(list []string, want string) bool { return slices.Contains(list, want) }

// With tracing off the decorator must not be in the call path at all.
func TestStoreNotWrappedWhenTracingDisabled(t *testing.T) {
	inner := memstore.New()
	off, _, err := tracing.Start(context.Background(), tracing.Config{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := tracing.Store(inner, off); got != domain.MetadataStore(inner) {
		t.Error("disabled tracer should leave the store undecorated")
	}
	if got := tracing.Store(inner, domain.NopTracer{}); got != domain.MetadataStore(inner) {
		t.Error("NopTracer should leave the store undecorated")
	}
	if got := tracing.Store(inner, nil); got != domain.MetadataStore(inner) {
		t.Error("nil tracer should leave the store undecorated")
	}
}

func TestStoreSpansWrapPortCalls(t *testing.T) {
	rec := &recorder{}
	st := tracing.Store(memstore.New(), rec)
	ctx := context.Background()

	m := &domain.Model{ID: domain.NewID(), Name: "fraud-detector", CreatedAt: domain.NowMillis()}
	if err := st.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if _, err := st.GetModel(ctx, "fraud-detector"); err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if _, _, err := st.ListModels(ctx, domain.ListOptions{PageSize: 10}); err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	spans := rec.spans()
	for _, want := range []string{"store.CreateModel", "store.GetModel", "store.ListModels"} {
		if !has(spans, want) {
			t.Errorf("missing span %q; got %v", want, spans)
		}
	}
	if got := rec.errored(); len(got) != 0 {
		t.Errorf("no call failed, but these spans recorded an error: %v", got)
	}
}

// A failing store call must mark its span, or an error trace looks identical to a clean one.
func TestStoreSpanRecordsError(t *testing.T) {
	rec := &recorder{}
	st := tracing.Store(memstore.New(), rec)

	if _, err := st.GetModel(context.Background(), "does-not-exist"); err == nil {
		t.Fatal("expected a not-found error")
	}
	if got := rec.errored(); !has(got, "store.GetModel") {
		t.Errorf("failed call did not mark its span; errored spans = %v", got)
	}
}
