package metrics

import (
	"strings"
	"testing"
)

func render(r *Registry) string {
	var b strings.Builder
	r.Render(&b)
	return b.String()
}

func TestCounterAndGaugeExposition(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("lineage_test_total", "test counter", "a")
	c.With("x").Inc()
	c.With("x").Add(2)
	c.With("y").Inc()
	g := r.NewGauge("lineage_g", "test gauge")
	g.Set(5)

	out := render(r)
	for _, want := range []string{
		"# TYPE lineage_test_total counter",
		`lineage_test_total{a="x"} 3`,
		`lineage_test_total{a="y"} 1`,
		"# TYPE lineage_g gauge",
		"lineage_g 5",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q\n---\n%s", want, out)
		}
	}
}

func TestHistogramCumulativeBuckets(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogram("lineage_h", "test hist", []float64{1, 5, 10})
	for _, v := range []float64{0.5, 3, 7, 20} {
		h.Observe(v)
	}
	out := render(r)
	for _, want := range []string{
		"# TYPE lineage_h histogram",
		`lineage_h_bucket{le="1"} 1`,
		`lineage_h_bucket{le="5"} 2`,
		`lineage_h_bucket{le="10"} 3`,
		`lineage_h_bucket{le="+Inf"} 4`,
		"lineage_h_sum 30.5",
		"lineage_h_count 4",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("histogram exposition missing %q\n---\n%s", want, out)
		}
	}
}

func TestOnScrapeRefreshesGauges(t *testing.T) {
	r := NewRegistry()
	g := r.NewGauge("lineage_live", "live gauge")
	n := 0
	r.OnScrape(func() { n++; g.Set(float64(n)) })

	if out := render(r); !strings.Contains(out, "lineage_live 1") {
		t.Fatalf("first scrape gauge wrong:\n%s", out)
	}
	if out := render(r); !strings.Contains(out, "lineage_live 2") {
		t.Fatalf("scrape hook should refresh gauge:\n%s", out)
	}
}

func TestLabelEscaping(t *testing.T) {
	r := NewRegistry()
	r.NewCounterVec("lineage_esc_total", "esc", "path").With(`a"b\c`).Inc()
	out := render(r)
	if !strings.Contains(out, `path="a\"b\\c"`) {
		t.Fatalf("label not escaped: %s", out)
	}
}
