//go:build e2e

package e2e_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestResolveHTTPContractSurvivesRestart(t *testing.T) {
	stateDir := t.TempDir()
	s := startServer(t, stateDir)
	const model, version, artifact = "delivery-contract", "1.0.0", "model.bin"
	payload := []byte("0123456789-lineage-delivery-contract")
	createModelVersion(t, s, model, version)
	digest := uploadArtifact(t, s, model, version, artifact, payload)
	transition(t, s, model, version, "staging")
	transition(t, s, model, version, "production")

	assertDeliveryContract(t, s, model, version, artifact, digest, payload)
	s.stop(t)

	restarted := startServer(t, stateDir)
	assertDeliveryContract(t, restarted, model, version, artifact, digest, payload)
}

func assertDeliveryContract(t *testing.T, s *serverProcess, model, version, artifact, digest string, payload []byte) {
	t.Helper()
	resolveURL := s.modelURL + "/v1/models/" + model + "/resolve?stage=production"
	resolved := s.request(t, http.MethodGet, resolveURL, nil, nil)
	assertText(t, resolved, http.StatusOK, `"version":"`+version+`"`)
	wantETag := `"` + digest + `"`
	if resolved.header.Get("ETag") != wantETag {
		t.Fatalf("resolve ETag = %q, want %q", resolved.header.Get("ETag"), wantETag)
	}
	if !strings.Contains(resolved.header.Get("Cache-Control"), "no-cache") {
		t.Fatalf("resolve Cache-Control = %q, want revalidation", resolved.header.Get("Cache-Control"))
	}
	conditional := s.request(t, http.MethodGet, resolveURL, nil, map[string]string{"If-None-Match": wantETag})
	if conditional.status != http.StatusNotModified || len(conditional.body) != 0 {
		t.Fatalf("conditional resolve = %d body=%q, want empty 304", conditional.status, conditional.body)
	}

	contentURL := s.modelURL + "/v1/models/" + model + "/versions/" + version + "/artifacts/" + artifact + "/content"
	content := s.request(t, http.MethodGet, contentURL, nil, nil)
	assertBytes(t, content, http.StatusOK, payload)
	if content.header.Get("ETag") != wantETag {
		t.Fatalf("content ETag = %q, want %q", content.header.Get("ETag"), wantETag)
	}
	ranged := s.request(t, http.MethodGet, contentURL, nil, map[string]string{"Range": "bytes=3-8"})
	assertBytes(t, ranged, http.StatusPartialContent, payload[3:9])
	if ranged.header.Get("Content-Range") != "bytes 3-8/"+strconv.Itoa(len(payload)) {
		t.Fatalf("Content-Range = %q", ranged.header.Get("Content-Range"))
	}
	content304 := s.request(t, http.MethodGet, contentURL, nil, map[string]string{"If-None-Match": wantETag})
	if content304.status != http.StatusNotModified || len(content304.body) != 0 {
		t.Fatalf("conditional content = %d body=%q, want empty 304", content304.status, content304.body)
	}
}
