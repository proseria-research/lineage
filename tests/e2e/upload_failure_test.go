//go:build e2e

package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectedUploadLeavesNoArtifactOrBytes(t *testing.T) {
	stateDir := t.TempDir()
	s := startServer(t, stateDir)
	const model, version = "upload-atomicity", "1.0.0"
	createModelVersion(t, s, model, version)
	payload := []byte("valid artifact payload")
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	t.Run("digest mismatch", func(t *testing.T) {
		ticket := initiateUpload(t, s, model, version, "digest.bin", len(payload))
		putUpload(t, s, ticket, payload)
		finalizeUpload(t, s, model, version, ticket, "sha256:"+strings.Repeat("0", 64), http.StatusUnprocessableEntity)
		assertArtifactAbsent(t, s, stateDir, model, version, "digest.bin")

		// A rejected attempt must release both the pending reservation and the filename.
		retry := initiateUpload(t, s, model, version, "digest.bin", len(payload))
		putUpload(t, s, retry, payload)
		finalizeUpload(t, s, model, version, retry, digest, http.StatusCreated)
	})

	t.Run("declared size mismatch", func(t *testing.T) {
		ticket := initiateUpload(t, s, model, version, "size.bin", len(payload)+7)
		putUpload(t, s, ticket, payload)
		finalizeUpload(t, s, model, version, ticket, digest, http.StatusUnprocessableEntity)
		assertArtifactAbsent(t, s, stateDir, model, version, "size.bin")
	})

	t.Run("finalize without upload", func(t *testing.T) {
		ticket := initiateUpload(t, s, model, version, "missing.bin", len(payload))
		finalizeUpload(t, s, model, version, ticket, digest, http.StatusUnprocessableEntity)
		assertArtifactAbsent(t, s, stateDir, model, version, "missing.bin")
	})
}

func assertArtifactAbsent(t *testing.T, s *serverProcess, stateDir, model, version, name string) {
	t.Helper()
	listed := s.json(t, http.MethodGet, s.modelURL+"/v1/models/"+model+"/versions/"+version+"/artifacts", nil, http.StatusOK)
	items, _ := listed["items"].([]any)
	for _, raw := range items {
		artifact, _ := raw.(map[string]any)
		if artifact["name"] == name {
			t.Fatalf("rejected artifact %q still has metadata: %+v", name, artifact)
		}
	}
	path := filepath.Join(stateDir, "artifacts", model, version, name)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("rejected artifact bytes remain at %s (err=%v)", path, err)
	}
}
