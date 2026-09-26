package adminui_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/api/adminui"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// The console reads insight through the BFF, so the panel's two states — reported and
// never reported — have to be distinguishable in the payload (§11.8).
func insightSetup(t *testing.T) *httptest.Server {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New())
	ctx := context.Background()
	if _, err := svc.CreateModel(ctx, "seed", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"fp16", "int8"} {
		if _, _, err := svc.PublishVersion(ctx, "seed", "m", core.PublishVersionInput{Name: v}); err != nil {
			t.Fatal(err)
		}
	}
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	write := func(version, dtype, weights string, disk int64) {
		t.Helper()
		layers := []domain.LayerBlock{{Path: "model.layers.*.self_attn.q_proj", OpType: "Linear", RepeatCount: 32}}
		_, err := svc.WriteInsight(ctx, "scanner", "m", version, core.InsightWrite{
			SchemaVersion: core.InsightSchemaVersion, Source: domain.SourceDerived, Reporter: "scanner",
			Facts: map[string]json.RawMessage{
				"hashes":          raw(map[string]string{"topology": "t", "shape": "s", "dtype": dtype, "weights": weights}),
				"paramCountTotal": raw(8030261248),
				"diskBytes":       raw(disk),
			},
			Layers: &layers,
		}, false, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	write("fp16", "d16", "w1", 16060522496)
	write("int8", "d8", "w2", 5730000000)

	yes := true
	for _, tc := range []struct {
		version string
		value   float64
	}{{"fp16", 0.681}, {"int8", 0.664}} {
		if _, err := svc.AddEvaluation(ctx, "harness", "m", tc.version, core.EvaluationInput{
			Suite: "mmlu", Metric: "acc", Split: "5shot", Value: tc.value,
			HigherIsBetter: &yes, HarnessVersion: "0.4.2",
		}); err != nil {
			t.Fatal(err)
		}
	}
	total := int64(11811160064)
	if _, err := svc.PutFootprint(ctx, "loadtest", "m", "int8", "bs1-4k", core.FootprintInput{
		Source: domain.FootprintMeasured, TotalBytes: &total,
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(adminui.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestVersionDetailCarriesInsight(t *testing.T) {
	srv := insightSetup(t)
	got := getJSON(t, srv, "/api/models/m/versions/int8")

	ins, _ := got["insight"].(map[string]any)
	if ins == nil {
		t.Fatalf("version detail should carry insight: %v", got)
	}
	if ins["paramCountTotal"] != float64(8030261248) {
		t.Fatalf("paramCountTotal: %v", ins["paramCountTotal"])
	}
	// Layers are hydrated for the breakdown table, with repeats still collapsed.
	layers, _ := ins["layers"].([]any)
	if len(layers) != 1 {
		t.Fatalf("expected the layer breakdown, got %v", ins["layers"])
	}
	if l0, _ := layers[0].(map[string]any); l0["repeatCount"] != float64(32) {
		t.Fatalf("repeats should stay collapsed: %v", layers[0])
	}
	// Attribution reaches the console so every value can be labelled with its source.
	sources, _ := ins["fieldSources"].(map[string]any)
	if pc, _ := sources["paramCountTotal"].(map[string]any); pc["reporter"] != "scanner" {
		t.Fatalf("fieldSources should reach the console: %v", sources)
	}
	if fps, _ := got["footprints"].([]any); len(fps) != 1 {
		t.Fatalf("expected one footprint, got %v", got["footprints"])
	}
	if evs, _ := got["evaluations"].([]any); len(evs) != 1 {
		t.Fatalf("expected one evaluation, got %v", got["evaluations"])
	}
}

// A version nobody has reported on must come back with insight: null, so the panel can say
// "not reported" instead of rendering zeroes.
func TestVersionDetailWithoutInsight(t *testing.T) {
	srv := setup(t) // the shared fixture publishes a version with no insight
	got := getJSON(t, srv, "/api/models/fraud-detector/versions/1.4.0")
	if v, present := got["insight"]; !present || v != nil {
		t.Fatalf("insight should be present-and-null for an unreported version, got %#v", v)
	}
	if fps, _ := got["footprints"].([]any); fps == nil {
		t.Fatal("footprints should be [] rather than null")
	}
	if evs, _ := got["evaluations"].([]any); evs == nil {
		t.Fatal("evaluations should be [] rather than null")
	}
}

func TestCompareEndpoint(t *testing.T) {
	srv := insightSetup(t)
	got := getJSON(t, srv, "/api/models/m/compare?from=fp16&to=int8")

	if got["verdict"] != "recast" {
		t.Fatalf("verdict = %v, want recast", got["verdict"])
	}
	metrics, _ := got["metrics"].([]any)
	if len(metrics) != 1 {
		t.Fatalf("expected one comparable metric: %v", got["metrics"])
	}
	m0, _ := metrics[0].(map[string]any)
	if m0["direction"] != "worse" || m0["comparable"] != true {
		t.Fatalf("the accuracy tradeoff should read as worse: %v", m0)
	}
	// One side has a footprint and the other does not; the row must be marked rather than
	// silently differenced.
	fps, _ := got["footprints"].([]any)
	if len(fps) != 1 {
		t.Fatalf("expected the one-sided footprint row: %v", got["footprints"])
	}
	if f0, _ := fps[0].(map[string]any); f0["comparable"] != false {
		t.Fatalf("one-sided footprint should not be comparable: %v", fps[0])
	}

	resp, err := http.Get(srv.URL + "/api/models/m/compare?from=fp16")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("compare without ?to should be 400, got %d", resp.StatusCode)
	}
}
