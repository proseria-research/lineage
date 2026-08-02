//go:build e2e

package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

type uploadTicket struct {
	UploadID      string            `json:"uploadId"`
	URL           string            `json:"url"`
	Method        string            `json:"method"`
	Headers       map[string]string `json:"headers"`
	StreamThrough bool              `json:"streamThrough"`
	ContentURL    string            `json:"contentUrl"`
}

func createModelVersion(t *testing.T, s *serverProcess, model, version string) {
	t.Helper()
	s.json(t, http.MethodPost, s.modelURL+"/v1/models", map[string]any{
		"name": model, "owner": "e2e", "description": "created by the process harness",
	}, http.StatusCreated)
	s.json(t, http.MethodPost, s.modelURL+"/v1/models/"+model+"/versions", map[string]any{
		"name": version, "author": actor, "description": "E2E version",
	}, http.StatusCreated)
}

func initiateUpload(t *testing.T, s *serverProcess, model, version, name string, size int) uploadTicket {
	t.Helper()
	raw := s.json(t, http.MethodPost, s.modelURL+"/v1/models/"+model+"/versions/"+version+"/artifacts:initiateUpload", map[string]any{
		"name": name, "kind": "MODEL", "sizeBytes": size, "mediaType": "application/octet-stream",
		"modelFormat": map[string]string{"name": "raw", "version": "1"},
	}, http.StatusAccepted)
	ticket := uploadTicket{
		UploadID:      stringValue(raw["uploadId"]),
		URL:           stringValue(raw["url"]),
		Method:        stringValue(raw["method"]),
		StreamThrough: boolValue(raw["streamThrough"]),
		ContentURL:    stringValue(raw["contentUrl"]),
	}
	if headers, ok := raw["headers"].(map[string]any); ok {
		ticket.Headers = make(map[string]string, len(headers))
		for key, value := range headers {
			ticket.Headers[key] = stringValue(value)
		}
	}
	if ticket.UploadID == "" {
		t.Fatalf("initiate returned no uploadId: %+v", raw)
	}
	return ticket
}

func putUpload(t *testing.T, s *serverProcess, ticket uploadTicket, payload []byte) {
	t.Helper()
	method, url, headers, want := ticket.Method, ticket.URL, ticket.Headers, http.StatusOK
	if ticket.StreamThrough {
		method, url, want = http.MethodPut, s.modelURL+ticket.ContentURL, http.StatusNoContent
	}
	if method == "" {
		method = http.MethodPut
	}
	if url == "" {
		t.Fatalf("upload ticket has no destination: %+v", ticket)
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/octet-stream"
	r := s.request(t, method, url, payload, headers)
	if r.status != want {
		t.Fatalf("upload = %d, want %d; body=%s", r.status, want, r.body)
	}
}

func finalizeUpload(t *testing.T, s *serverProcess, model, version string, ticket uploadTicket, digest string, want int) map[string]any {
	t.Helper()
	return s.json(t, http.MethodPost, s.modelURL+"/v1/models/"+model+"/versions/"+version+"/artifacts:finalizeUpload", map[string]any{
		"uploadId": ticket.UploadID, "digest": digest,
	}, want)
}

func uploadArtifact(t *testing.T, s *serverProcess, model, version, name string, payload []byte) string {
	t.Helper()
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	ticket := initiateUpload(t, s, model, version, name, len(payload))
	putUpload(t, s, ticket, payload)
	finalizeUpload(t, s, model, version, ticket, digest, http.StatusCreated)
	return digest
}

func transition(t *testing.T, s *serverProcess, model, version, stage string) {
	t.Helper()
	s.json(t, http.MethodPost, s.modelURL+"/v1/models/"+model+"/versions/"+version+":transition", map[string]string{
		"to": stage, "reason": "E2E " + stage,
	}, http.StatusOK)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func boolValue(value any) bool {
	boolean, _ := value.(bool)
	return boolean
}
