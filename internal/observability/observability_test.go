package observability_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	memstore "github.com/proseria-research/lineage/internal/adapters/store/memory"
	"github.com/proseria-research/lineage/internal/observability"
	"github.com/proseria-research/lineage/internal/observability/metrics"
)

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func TestHealthMetricsAndReadiness(t *testing.T) {
	m := metrics.NewApp()
	m.RecordHTTP("model-api", "/v1/models", "GET", "200", 0.01)
	store := memstore.New()
	h := observability.Handler(m.Registry(), observability.StoreReady(store))

	if code, _ := get(t, h, "/healthz"); code != 200 {
		t.Fatalf("healthz = %d", code)
	}
	if code, _ := get(t, h, "/readyz"); code != 200 {
		t.Fatalf("readyz (store ok) = %d", code)
	}
	code, body := get(t, h, "/metrics")
	if code != 200 {
		t.Fatalf("metrics = %d", code)
	}
	for _, want := range []string{"lineage_up 1", `lineage_http_requests_total{`, "lineage_http_request_duration_seconds_count"} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q\n%s", want, body)
		}
	}
}

func TestReadinessGatesOnFailure(t *testing.T) {
	failing := func(context.Context) error { return errors.New("db down") }
	h := observability.Handler(nil, observability.Ready(failing))
	if code, body := get(t, h, "/readyz"); code != http.StatusServiceUnavailable || !strings.Contains(body, "db down") {
		t.Fatalf("readyz should be 503 with reason, got %d %q", code, body)
	}
}
