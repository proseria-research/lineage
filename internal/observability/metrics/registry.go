// Package metrics is a tiny, dependency-free Prometheus registry: counters, gauges, and
// histograms with labels, rendered in the text exposition format (0.0.4). Hand-rolled to
// keep the self-hostable binary's dependency footprint minimal (§09.2), in the same spirit
// as the hand-rolled SigV4 signer.
package metrics

import (
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry holds metric families and renders them. Safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	families []*family
	onScrape []func()
}

func NewRegistry() *Registry { return &Registry{} }

// OnScrape registers a callback run just before exposition — used to refresh gauges that
// reflect live state (domain totals, DB pool stats) without polling between scrapes.
func (r *Registry) OnScrape(fn func()) {
	r.mu.Lock()
	r.onScrape = append(r.onScrape, fn)
	r.mu.Unlock()
}

type metricKind string

const (
	kindCounter   metricKind = "counter"
	kindGauge     metricKind = "gauge"
	kindHistogram metricKind = "histogram"
)

type family struct {
	name   string
	help   string
	kind   metricKind
	labels []string

	mu     sync.RWMutex
	series map[string]*sample // keyed by joined label values
	bounds []float64          // histogram bucket upper bounds (kind == histogram)
}

type sample struct {
	labelValues []string
	val         f64   // counter/gauge value
	buckets     []f64 // histogram per-bound counts (non-cumulative)
	sum         f64   // histogram sum
	count       atomic.Uint64
}

func (r *Registry) newFamily(name, help string, kind metricKind, labels []string, bounds []float64) *family {
	f := &family{name: name, help: help, kind: kind, labels: labels, series: map[string]*sample{}, bounds: bounds}
	r.mu.Lock()
	r.families = append(r.families, f)
	r.mu.Unlock()
	return f
}

func (f *family) sampleFor(vals []string) *sample {
	key := strings.Join(vals, "\x1f")
	f.mu.RLock()
	s := f.series[key]
	f.mu.RUnlock()
	if s != nil {
		return s
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if s = f.series[key]; s != nil {
		return s
	}
	s = &sample{labelValues: append([]string(nil), vals...)}
	if f.kind == kindHistogram {
		s.buckets = make([]f64, len(f.bounds))
	}
	f.series[key] = s
	return s
}

// ---- Counter ----

type CounterVec struct{ f *family }

func (r *Registry) NewCounterVec(name, help string, labels ...string) *CounterVec {
	return &CounterVec{f: r.newFamily(name, help, kindCounter, labels, nil)}
}
func (r *Registry) NewCounter(name, help string) *Counter {
	return &Counter{s: r.newFamily(name, help, kindCounter, nil, nil).sampleFor(nil)}
}

type Counter struct{ s *sample }

func (c *Counter) Inc()                            { c.s.val.add(1) }
func (c *Counter) Add(v float64)                   { c.s.val.add(v) }
func (v *CounterVec) With(vals ...string) *Counter { return &Counter{s: v.f.sampleFor(vals)} }

// ---- Gauge ----

type GaugeVec struct{ f *family }

func (r *Registry) NewGaugeVec(name, help string, labels ...string) *GaugeVec {
	return &GaugeVec{f: r.newFamily(name, help, kindGauge, labels, nil)}
}
func (r *Registry) NewGauge(name, help string) *Gauge {
	return &Gauge{s: r.newFamily(name, help, kindGauge, nil, nil).sampleFor(nil)}
}

type Gauge struct{ s *sample }

func (g *Gauge) Set(v float64)                 { g.s.val.set(v) }
func (g *Gauge) Add(v float64)                 { g.s.val.add(v) }
func (v *GaugeVec) With(vals ...string) *Gauge { return &Gauge{s: v.f.sampleFor(vals)} }

// ---- Histogram ----

// DefaultDurationBuckets are seconds-scale latency buckets.
var DefaultDurationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

type HistogramVec struct{ f *family }

func (r *Registry) NewHistogramVec(name, help string, buckets []float64, labels ...string) *HistogramVec {
	if len(buckets) == 0 {
		buckets = DefaultDurationBuckets
	}
	return &HistogramVec{f: r.newFamily(name, help, kindHistogram, labels, buckets)}
}
func (r *Registry) NewHistogram(name, help string, buckets []float64) *Histogram {
	hv := r.NewHistogramVec(name, help, buckets)
	return hv.With()
}

type Histogram struct {
	s      *sample
	bounds []float64
}

func (v *HistogramVec) With(vals ...string) *Histogram {
	return &Histogram{s: v.f.sampleFor(vals), bounds: v.f.bounds}
}

func (h *Histogram) Observe(v float64) {
	h.s.sum.add(v)
	h.s.count.Add(1)
	for i, b := range h.bounds {
		if v <= b {
			h.s.buckets[i].add(1)
			return
		}
	}
}

// ---- exposition ----

// Render writes all families in the Prometheus text format after running scrape hooks.
func (r *Registry) Render(w io.Writer) {
	r.mu.RLock()
	hooks := append([]func(){}, r.onScrape...)
	fams := append([]*family(nil), r.families...)
	r.mu.RUnlock()
	for _, fn := range hooks {
		fn()
	}

	var b strings.Builder
	for _, f := range fams {
		b.WriteString("# HELP " + f.name + " " + f.help + "\n")
		b.WriteString("# TYPE " + f.name + " " + string(f.kind) + "\n")

		f.mu.RLock()
		series := make([]*sample, 0, len(f.series))
		for _, s := range f.series {
			series = append(series, s)
		}
		f.mu.RUnlock()
		sort.Slice(series, func(i, j int) bool {
			return strings.Join(series[i].labelValues, "\x1f") < strings.Join(series[j].labelValues, "\x1f")
		})

		for _, s := range series {
			switch f.kind {
			case kindHistogram:
				var cum float64
				for i, bound := range f.bounds {
					cum += s.buckets[i].get()
					b.WriteString(f.name + "_bucket" + labelStr(f.labels, s.labelValues, "le", formatFloat(bound)) + " " + formatFloat(cum) + "\n")
				}
				total := float64(s.count.Load())
				b.WriteString(f.name + "_bucket" + labelStr(f.labels, s.labelValues, "le", "+Inf") + " " + formatFloat(total) + "\n")
				b.WriteString(f.name + "_sum" + labelStr(f.labels, s.labelValues, "", "") + " " + formatFloat(s.sum.get()) + "\n")
				b.WriteString(f.name + "_count" + labelStr(f.labels, s.labelValues, "", "") + " " + formatFloat(total) + "\n")
			default:
				b.WriteString(f.name + labelStr(f.labels, s.labelValues, "", "") + " " + formatFloat(s.val.get()) + "\n")
			}
		}
	}
	_, _ = io.WriteString(w, b.String())
}

// labelStr renders {a="1",b="2"} plus an optional extra label (le for histograms).
func labelStr(names, values []string, extraName, extraVal string) string {
	if len(names) == 0 && extraName == "" {
		return ""
	}
	var parts []string
	for i, n := range names {
		v := ""
		if i < len(values) {
			v = values[i]
		}
		parts = append(parts, n+`="`+escape(v)+`"`)
	}
	if extraName != "" {
		parts = append(parts, extraName+`="`+escape(extraVal)+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func formatFloat(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// f64 is an atomic float64 (bit-pattern), mirroring how Prometheus stores counter/gauge values.
type f64 struct{ bits atomic.Uint64 }

func (f *f64) add(v float64) {
	for {
		old := f.bits.Load()
		nv := math.Float64frombits(old) + v
		if f.bits.CompareAndSwap(old, math.Float64bits(nv)) {
			return
		}
	}
}
func (f *f64) set(v float64) { f.bits.Store(math.Float64bits(v)) }
func (f *f64) get() float64  { return math.Float64frombits(f.bits.Load()) }
