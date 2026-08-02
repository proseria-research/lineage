//go:build e2e

package e2e_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadinessTracksStorageFailureWhileLivenessStaysUp(t *testing.T) {
	var unavailable atomic.Bool
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if unavailable.Load() {
			http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
			return
		}
		// Readiness stats a sentinel that should not exist. S3 404 means reachable and absent.
		w.WriteHeader(http.StatusNotFound)
	}))
	defer storage.Close()

	cfg := sqliteFSConfig(t.TempDir())
	cfg.env["LINEAGE_STORAGE_DRIVER"] = "s3"
	cfg.env["LINEAGE_S3_ENDPOINT"] = storage.URL
	cfg.env["LINEAGE_S3_BUCKET"] = "readiness"
	cfg.env["LINEAGE_S3_REGION"] = "us-east-1"
	cfg.env["LINEAGE_S3_ACCESS_KEY"] = "e2e"
	cfg.env["LINEAGE_S3_SECRET_KEY"] = "e2e-secret"
	cfg.env["LINEAGE_S3_PATH_STYLE"] = "true"
	s := startServerWith(t, cfg)

	unavailable.Store(true)
	eventually(t, 3*time.Second, func() bool {
		return s.request(t, http.MethodGet, s.opsURL+"/readyz", nil, nil).status == http.StatusServiceUnavailable
	})
	assertText(t, s.request(t, http.MethodGet, s.opsURL+"/healthz", nil, nil), http.StatusOK, "ok")

	unavailable.Store(false)
	eventually(t, 3*time.Second, func() bool {
		return s.request(t, http.MethodGet, s.opsURL+"/readyz", nil, nil).status == http.StatusOK
	})
}
