package core_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// fakeTracer records the spans the core opens and the attributes it hangs on them.
type fakeTracer struct {
	mu    sync.Mutex
	names []string
	attrs map[string]string // last value seen per key, across all spans
	errs  []string
}

func newFakeTracer() *fakeTracer { return &fakeTracer{attrs: map[string]string{}} }

func (f *fakeTracer) Start(ctx context.Context, name string) (context.Context, domain.Span) {
	f.mu.Lock()
	f.names = append(f.names, name)
	f.mu.Unlock()
	return ctx, &fakeCoreSpan{f: f, name: name}
}

func (f *fakeTracer) started(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.names, name)
}

func (f *fakeTracer) attr(k string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attrs[k]
}

func (f *fakeTracer) failed(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.errs, name)
}

type fakeCoreSpan struct {
	f    *fakeTracer
	name string
}

func (s *fakeCoreSpan) SetName(n string) { s.name = n }
func (s *fakeCoreSpan) SetString(k, v string) {
	s.f.mu.Lock()
	s.f.attrs[k] = v
	s.f.mu.Unlock()
}
func (s *fakeCoreSpan) SetInt(string, int64) {}
func (s *fakeCoreSpan) TraceID() string      { return "trace" }
func (s *fakeCoreSpan) End()                 {}
func (s *fakeCoreSpan) RecordError(err error) {
	if err == nil {
		return
	}
	s.f.mu.Lock()
	s.f.errs = append(s.f.errs, s.name)
	s.f.mu.Unlock()
}

func tracedService(tr domain.Tracer) *core.Service {
	fb := newFakeBackend()
	return core.New(memstore.New(), map[string]domain.StorageBackend{fb.Name(): fb}, fb.Name(),
		memcache.New(), events.New(), core.WithTracer(tr))
}

func TestTracerHooksFire(t *testing.T) {
	ctx := context.Background()
	ft := newFakeTracer()
	svc := tracedService(ft)

	if _, err := svc.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.PublishVersion(ctx, "me", "m", core.PublishVersionInput{
		Name:      "1.0.0",
		Artifacts: []core.ArtifactInput{{Name: "model.bin", URI: "s3://bucket/m/1.0.0", Digest: "sha256:abc", SizeBytes: 4}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transition(ctx, "me", "m", "1.0.0", domain.StageStaging, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Resolve(ctx, "m", domain.Selector{Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"core.PublishVersion", "core.Transition", "core.Resolve"} {
		if !ft.started(want) {
			t.Errorf("span %q never opened", want)
		}
	}
	if got := ft.attr("lineage.model"); got != "m" {
		t.Errorf("lineage.model = %q, want m", got)
	}
	if got := ft.attr("lineage.stage.to"); got != string(domain.StageStaging) {
		t.Errorf("lineage.stage.to = %q, want staging", got)
	}
}

// The cache attribute is what makes two identical resolves distinguishable in a trace (§04.4).
func TestResolveSpanRecordsCacheOutcome(t *testing.T) {
	ctx := context.Background()
	ft := newFakeTracer()
	svc := tracedService(ft)

	if _, err := svc.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.PublishVersion(ctx, "me", "m", core.PublishVersionInput{
		Name:      "1.0.0",
		Artifacts: []core.ArtifactInput{{Name: "model.bin", URI: "s3://bucket/m/1.0.0", Digest: "sha256:abc", SizeBytes: 4}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Resolve(ctx, "m", domain.Selector{Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if got := ft.attr("lineage.cache"); got != "miss" {
		t.Errorf("first resolve recorded cache=%q, want miss", got)
	}
	if _, err := svc.Resolve(ctx, "m", domain.Selector{Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if got := ft.attr("lineage.cache"); got != "hit" {
		t.Errorf("second resolve recorded cache=%q, want hit", got)
	}
}

// A failed operation must mark its span. This is the case the named-return defer in the core
// exists for: an early return that forgets to record would silently look like success.
func TestCoreSpansRecordFailures(t *testing.T) {
	ctx := context.Background()
	ft := newFakeTracer()
	svc := tracedService(ft)

	if _, err := svc.Resolve(ctx, "no-such-model", domain.Selector{Version: "1.0.0"}); err == nil {
		t.Fatal("expected resolve of an unknown model to fail")
	}
	if !ft.failed("core.Resolve") {
		t.Error("failed resolve did not mark its span")
	}

	if _, _, err := svc.PublishVersion(ctx, "me", "no-such-model", core.PublishVersionInput{Name: "1.0.0"}); err == nil {
		t.Fatal("expected publish against an unknown model to fail")
	}
	if !ft.failed("core.PublishVersion") {
		t.Error("failed publish did not mark its span")
	}
}

// Tracing off must not change behaviour: the default service traces into a no-op.
func TestUntracedServiceStillWorks(t *testing.T) {
	ctx := context.Background()
	fb := newFakeBackend()
	svc := core.New(memstore.New(), map[string]domain.StorageBackend{fb.Name(): fb}, fb.Name(),
		memcache.New(), events.New())

	if _, err := svc.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Resolve(ctx, "m", domain.Selector{Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
}
