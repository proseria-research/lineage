//go:build e2e

package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestProblemResponsesAndRequestIDs(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.json(t, http.MethodPost, s.modelURL+"/v1/models", map[string]string{"name": "duplicate"}, http.StatusCreated)

	cases := []struct {
		name, method, path, body, code string
		status                         int
	}{
		{"not found", http.MethodGet, "/v1/models/missing", "", "not_found", http.StatusNotFound},
		{"malformed JSON", http.MethodPost, "/v1/models", `{`, "invalid_argument", http.StatusBadRequest},
		{"duplicate", http.MethodPost, "/v1/models", `{"name":"duplicate"}`, "already_exists", http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			if tc.body != "" {
				body = []byte(tc.body)
			}
			result := s.request(t, tc.method, s.modelURL+tc.path, body, map[string]string{"X-Lineage-Actor": actor})
			if result.status != tc.status {
				t.Fatalf("status=%d want=%d body=%s", result.status, tc.status, result.body)
			}
			if !strings.HasPrefix(result.header.Get("Content-Type"), "application/problem+json") {
				t.Fatalf("Content-Type=%q", result.header.Get("Content-Type"))
			}
			if result.header.Get("X-Request-Id") == "" {
				t.Fatal("problem response has no X-Request-Id")
			}
			var problem map[string]any
			if err := json.Unmarshal(result.body, &problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem["code"] != tc.code || int(problem["status"].(float64)) != tc.status || problem["detail"] == "" {
				t.Fatalf("problem body=%+v", problem)
			}
			if !strings.HasSuffix(stringValue(problem["type"]), "/"+tc.code) {
				t.Fatalf("problem type=%v", problem["type"])
			}
		})
	}
}
