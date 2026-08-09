package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
	"github.com/proseria-research/lineage/internal/adapters/events"
	"github.com/proseria-research/lineage/internal/adapters/storage/fs"
	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/api/modelapi"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// The seed exists so a fresh `make run` shows a registry with something in it. A fixture that
// silently stops exercising a feature is worse than no fixture — the console looks empty and
// the reader concludes the feature is broken. So the dataset is run end-to-end here, through
// the real Model API, and the compliance surfaces it is supposed to populate are asserted.

func seeded(t *testing.T) *httptest.Server {
	t.Helper()
	backend := fs.New("default", t.TempDir())
	svc := core.New(memstore.New(),
		map[string]domain.StorageBackend{backend.Name(): backend}, backend.Name(),
		memcache.New(), events.New())
	srv := httptest.NewServer(modelapi.New(svc, "X-Lineage-Actor").Handler())
	t.Cleanup(srv.Close)

	c := &client{base: srv.URL, actor: "seed@lineage.dev", http: &http.Client{Timeout: 30 * time.Second}}
	if err := run(c); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string, out any) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func TestSeedRunsCleanAndPopulatesCompliance(t *testing.T) {
	srv := seeded(t)

	// ---- Classifications land, and nothing starts stale ----
	//
	// Classifying happens after every version and edge exists, precisely so the demo does not
	// open on a worklist full of drift it manufactured itself (§16.5).
	var inv struct {
		Items []struct {
			Name           string
			Classification *struct {
				State             string
				StaleReasons      []string
				EUSystemRiskClass string `json:"euSystemRiskClass"`
				EUGpaiTier        string `json:"euGpaiTier"`
				ReviewDueAt       *int64 `json:"reviewDueAt"`
			}
		}
	}
	get(t, srv, "/v1/models?include=classification&pageSize=100", &inv)

	byName := map[string]string{}
	for _, m := range inv.Items {
		if m.Classification == nil {
			continue
		}
		byName[m.Name] = m.Classification.State
		if m.Classification.State != "current" {
			t.Errorf("%s seeds %s (%v) — the fixture should not manufacture drift",
				m.Name, m.Classification.State, m.Classification.StaleReasons)
		}
		if d := m.Classification.ReviewDueAt; d != nil && *d <= time.Now().UnixMilli() {
			t.Errorf("%s seeds a review date already in the past", m.Name)
		}
	}
	if len(byName) < 2 {
		t.Fatalf("expected at least two classified models, got %v", byName)
	}

	// ---- The Art. 25 queue has something in it ----

	var q struct {
		Items []struct {
			Model, Version, Verdict, Status, DeclaredMethod string
			EUSystemRiskClass                               string `json:"euSystemRiskClass"`
			EUGpaiTier                                      string `json:"euGpaiTier"`
			Hashes                                          map[string]struct {
				From, To string
				Changed  *bool
			}
		}
	}
	get(t, srv, "/v1/reviews?status=open", &q)
	if len(q.Items) == 0 {
		t.Fatal("the seeded registry should open the Art. 25 queue with something in it")
	}
	for _, it := range q.Items {
		if it.Status != "open" {
			t.Errorf("%s@%s: status %q from ?status=open", it.Model, it.Version, it.Status)
		}
		if it.Verdict == "identical" {
			t.Errorf("%s@%s: identical must never queue", it.Model, it.Version)
		}
		// Condition 3: every queued item is on a model somebody classified governed.
		high := it.EUSystemRiskClass == "high_annex_iii" || it.EUSystemRiskClass == "high_annex_i"
		gpai := it.EUGpaiTier != "" && it.EUGpaiTier != "none"
		if !high && !gpai {
			t.Errorf("%s@%s queued with class=%q tier=%q", it.Model, it.Version, it.EUSystemRiskClass, it.EUGpaiTier)
		}
	}

	// The int8 row is the one worth seeing: a `recast` measurement beside a declared
	// `quantize`, on a GPAI model, with all four hash levels on both sides so the console
	// draws two complete fingerprints.
	var int8 *struct {
		Model, Version, Verdict, Status, DeclaredMethod string
		EUSystemRiskClass                               string `json:"euSystemRiskClass"`
		EUGpaiTier                                      string `json:"euGpaiTier"`
		Hashes                                          map[string]struct {
			From, To string
			Changed  *bool
		}
	}
	for i, it := range q.Items {
		if it.Model == "sentiment-classifier" && it.Version == "2.2.0-int8" {
			int8 = &q.Items[i]
		}
	}
	if int8 == nil {
		t.Fatal("the quantized sentiment version should be queued — it is the demo's clearest case")
	}
	if int8.Verdict != "recast" || int8.DeclaredMethod != "quantize" {
		t.Fatalf("int8 row: verdict=%q declaredMethod=%q", int8.Verdict, int8.DeclaredMethod)
	}
	for _, level := range []string{"topology", "shape", "dtype", "weights"} {
		h := int8.Hashes[level]
		if h.From == "" || h.To == "" {
			t.Errorf("int8 row: %s missing a side (from=%q to=%q)", level, h.From, h.To)
		}
	}

	// ---- Re-seeding refuses rather than half-applying ----
	//
	// Seeding is additive and never deletes (§19.4). The second run must fail on the first
	// model rather than duplicating anything.
	c := &client{base: srv.URL, actor: "seed@lineage.dev", http: &http.Client{Timeout: 30 * time.Second}}
	if err := run(c); err == nil {
		t.Fatal("re-seeding a populated registry should refuse")
	}
}
