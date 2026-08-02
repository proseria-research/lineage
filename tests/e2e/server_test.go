//go:build e2e

package e2e_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const actor = "e2e@lineage.test"

type response struct {
	status int
	header http.Header
	body   []byte
}

func (s *serverProcess) request(t *testing.T, method, url string, body []byte, headers map[string]string) response {
	t.Helper()
	result, err := s.doRequest(method, url, body, headers)
	if err != nil {
		t.Fatalf("%s %s: %v\n--- process logs ---\n%s", method, url, err, s.logs.String())
	}
	return result
}

func (s *serverProcess) doRequest(method, url string, body []byte, headers map[string]string) (response, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return response{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, err
	}
	return response{status: resp.StatusCode, header: resp.Header.Clone(), body: data}, nil
}

func (s *serverProcess) json(t *testing.T, method, url string, body any, want int) map[string]any {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := s.request(t, method, url, encoded, map[string]string{"X-Lineage-Actor": actor})
	if r.status != want {
		t.Fatalf("%s %s = %d, want %d; body=%s", method, url, r.status, want, r.body)
	}
	if len(r.body) == 0 {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(r.body, &decoded); err != nil {
		t.Fatalf("decode %s %s: %v; body=%s", method, url, err, r.body)
	}
	return decoded
}

func TestRealServerLifecycle(t *testing.T) {
	stateDir := t.TempDir()
	s := startServer(t, stateDir)

	assertText(t, s.request(t, http.MethodGet, s.opsURL+"/healthz", nil, nil), http.StatusOK, "ok")
	assertText(t, s.request(t, http.MethodGet, s.opsURL+"/readyz", nil, nil), http.StatusOK, "ready")

	s.json(t, http.MethodPost, s.modelURL+"/v1/models", map[string]any{
		"name": "e2e-model", "description": "created by the process harness", "owner": "e2e",
	}, http.StatusCreated)
	s.json(t, http.MethodPost, s.modelURL+"/v1/models/e2e-model/versions", map[string]any{
		"name": "1.0.0", "description": "durable E2E version", "author": actor,
	}, http.StatusCreated)

	payload := []byte("LINEAGE-E2E real artifact bytes\n")
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	ticket := s.json(t, http.MethodPost, s.modelURL+"/v1/models/e2e-model/versions/1.0.0/artifacts:initiateUpload", map[string]any{
		"name": "model.bin", "kind": "MODEL", "sizeBytes": len(payload), "mediaType": "application/octet-stream",
		"modelFormat": map[string]string{"name": "raw", "version": "1"},
	}, http.StatusAccepted)
	uploadID, _ := ticket["uploadId"].(string)
	contentURL, _ := ticket["contentUrl"].(string)
	if uploadID == "" || contentURL == "" || ticket["streamThrough"] != true {
		t.Fatalf("expected filesystem stream-through ticket, got %+v", ticket)
	}
	upload := s.request(t, http.MethodPut, s.modelURL+contentURL, payload, map[string]string{"Content-Type": "application/octet-stream"})
	if upload.status != http.StatusNoContent {
		t.Fatalf("upload = %d, want 204; body=%s", upload.status, upload.body)
	}
	s.json(t, http.MethodPost, s.modelURL+"/v1/models/e2e-model/versions/1.0.0/artifacts:finalizeUpload", map[string]any{
		"uploadId": uploadID, "digest": digest,
	}, http.StatusCreated)

	transition := s.modelURL + "/v1/models/e2e-model/versions/1.0.0:transition"
	s.json(t, http.MethodPost, transition, map[string]string{"to": "staging", "reason": "E2E validation"}, http.StatusOK)
	s.json(t, http.MethodPost, transition, map[string]string{"to": "production", "reason": "E2E promotion"}, http.StatusOK)

	resolved := s.json(t, http.MethodGet, s.modelURL+"/v1/models/e2e-model/resolve?stage=production", nil, http.StatusOK)
	if resolved["version"] != "1.0.0" || resolved["stage"] != "production" || resolved["digest"] != digest {
		t.Fatalf("unexpected resolution: %+v", resolved)
	}
	contentPath := s.modelURL + "/v1/models/e2e-model/versions/1.0.0/artifacts/model.bin/content"
	assertBytes(t, s.request(t, http.MethodGet, contentPath, nil, nil), http.StatusOK, payload)

	admin := s.json(t, http.MethodGet, s.adminURL+"/api/models/e2e-model/versions/1.0.0", nil, http.StatusOK)
	version, _ := admin["version"].(map[string]any)
	if version["stage"] != "production" {
		t.Fatalf("admin BFF did not observe production state: %+v", admin)
	}
	audit := s.json(t, http.MethodGet, s.modelURL+"/v1/models/e2e-model/audit?pageSize=50", nil, http.StatusOK)
	if !containsActor(audit["items"], actor) {
		t.Fatalf("audit did not preserve actor %q: %+v", actor, audit)
	}
	metrics := s.request(t, http.MethodGet, s.opsURL+"/metrics", nil, nil)
	assertText(t, metrics, http.StatusOK, "lineage_up 1")
	assertText(t, metrics, http.StatusOK, "lineage_models 1")

	// Stop and restart the actual process against the same SQLite database and artifact root.
	// Resolution and byte delivery must survive, not merely in-memory metadata.
	s.stop(t)
	restarted := startServer(t, stateDir)
	resolved = restarted.json(t, http.MethodGet, restarted.modelURL+"/v1/models/e2e-model/resolve?stage=production", nil, http.StatusOK)
	if resolved["version"] != "1.0.0" || resolved["digest"] != digest {
		t.Fatalf("resolution did not survive restart: %+v", resolved)
	}
	restartedContent := restarted.modelURL + "/v1/models/e2e-model/versions/1.0.0/artifacts/model.bin/content"
	assertBytes(t, restarted.request(t, http.MethodGet, restartedContent, nil, nil), http.StatusOK, payload)
	if !strings.Contains(restarted.logs.String(), "metadata store: sqlite") {
		t.Fatalf("restart did not report SQLite startup\n%s", restarted.logs.String())
	}
}

func assertText(t *testing.T, got response, wantStatus int, contains string) {
	t.Helper()
	if got.status != wantStatus || !strings.Contains(string(got.body), contains) {
		t.Fatalf("response = %d %q, want %d containing %q", got.status, got.body, wantStatus, contains)
	}
}

func assertBytes(t *testing.T, got response, wantStatus int, want []byte) {
	t.Helper()
	if got.status != wantStatus || !bytes.Equal(got.body, want) {
		t.Fatalf("response = %d %q, want %d %q", got.status, got.body, wantStatus, want)
	}
}

func containsActor(raw any, want string) bool {
	items, _ := raw.([]any)
	for _, item := range items {
		event, _ := item.(map[string]any)
		if fmt.Sprint(event["actor"]) == want {
			return true
		}
	}
	return false
}
