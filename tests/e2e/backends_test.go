//go:build e2e

package e2e_test

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresS3ProcessWorkflow(t *testing.T) {
	dsn := os.Getenv("LINEAGE_E2E_POSTGRES_DSN")
	endpoint := os.Getenv("LINEAGE_E2E_S3_ENDPOINT")
	bucket := os.Getenv("LINEAGE_E2E_S3_BUCKET")
	if dsn == "" || endpoint == "" || bucket == "" {
		t.Skip("set LINEAGE_E2E_POSTGRES_DSN and LINEAGE_E2E_S3_{ENDPOINT,BUCKET,...} to run production backends")
	}

	cfg := sqliteFSConfig(t.TempDir())
	cfg.env["LINEAGE_DB_ENGINE"] = "postgres"
	cfg.env["LINEAGE_DB_PATH"] = dsn
	cfg.env["LINEAGE_STORAGE_DRIVER"] = "s3"
	cfg.env["LINEAGE_S3_ENDPOINT"] = endpoint
	cfg.env["LINEAGE_S3_BUCKET"] = bucket
	cfg.env["LINEAGE_S3_REGION"] = envOr("LINEAGE_E2E_S3_REGION", "us-east-1")
	cfg.env["LINEAGE_S3_ACCESS_KEY"] = os.Getenv("LINEAGE_E2E_S3_KEY")
	cfg.env["LINEAGE_S3_SECRET_KEY"] = os.Getenv("LINEAGE_E2E_S3_SECRET")
	cfg.env["LINEAGE_S3_PATH_STYLE"] = envOr("LINEAGE_E2E_S3_PATH_STYLE", "true")

	model := fmt.Sprintf("backend-e2e-%d", time.Now().UnixNano())
	const version, artifact = "1.0.0", "model.bin"
	payload := []byte("real postgres and S3 process workflow")
	s := startServerWith(t, cfg)
	createModelVersion(t, s, model, version)
	digest := uploadArtifact(t, s, model, version, artifact, payload)
	transition(t, s, model, version, "staging")
	transition(t, s, model, version, "production")
	resolved := s.json(t, http.MethodGet, s.modelURL+"/v1/models/"+model+"/resolve?stage=production", nil, http.StatusOK)
	if resolved["digest"] != digest {
		t.Fatalf("S3 resolution digest = %v, want %s", resolved["digest"], digest)
	}
	artifacts, _ := resolved["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("S3 resolution artifacts = %+v", artifacts)
	}
	resolvedArtifact, _ := artifacts[0].(map[string]any)
	if !strings.HasPrefix(stringValue(resolvedArtifact["storageUri"]), "s3://"+bucket+"/") || stringValue(resolvedArtifact["signedUrl"]) == "" {
		t.Fatalf("S3 resolution did not expose native and signed refs: %+v", resolvedArtifact)
	}
	contentURL := s.modelURL + "/v1/models/" + model + "/versions/" + version + "/artifacts/" + artifact + "/content"
	assertBytes(t, s.request(t, http.MethodGet, contentURL, nil, nil), http.StatusOK, payload)
	s.stop(t)

	restarted := startServerWith(t, cfg)
	persisted := restarted.json(t, http.MethodGet, restarted.modelURL+"/v1/models/"+model+"/resolve?stage=production", nil, http.StatusOK)
	if persisted["digest"] != digest {
		t.Fatalf("Postgres/S3 state did not survive restart: %+v", persisted)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
