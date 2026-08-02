//go:build e2e

package e2e_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGCWithRealFilesystemStateHonoursReferencesAndPrefix(t *testing.T) {
	stateDir := t.TempDir()
	const model, version = "managed", "1.0.0"
	seed := startServer(t, stateDir)
	createModelVersion(t, seed, model, version)
	payload := []byte("referenced bytes")
	uploadArtifact(t, seed, model, version, "referenced.bin", payload)
	seed.stop(t)

	root := filepath.Join(stateDir, "artifacts")
	orphan := filepath.Join(root, model, "orphan.bin")
	outside := filepath.Join(root, "outside", "keep.bin")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside prefix"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := sqliteFSConfig(stateDir)
	cfg.env["LINEAGE_STORAGE_GC"] = "sweep"
	cfg.env["LINEAGE_GC_PREFIX"] = model
	cfg.env["LINEAGE_GC_GRACE"] = "0s"
	cfg.env["LINEAGE_GC_INTERVAL"] = "20ms"
	sweeper := startServerWith(t, cfg)
	eventually(t, 3*time.Second, func() bool {
		_, err := os.Stat(orphan)
		return os.IsNotExist(err)
	})
	assertBytes(t, sweeper.request(t, http.MethodGet,
		sweeper.modelURL+"/v1/models/"+model+"/versions/"+version+"/artifacts/referenced.bin/content", nil, nil),
		http.StatusOK, payload)
	if got, err := os.ReadFile(outside); err != nil || string(got) != "outside prefix" {
		t.Fatalf("GC crossed its configured prefix: body=%q err=%v", got, err)
	}
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition was not met within %s", timeout)
}
