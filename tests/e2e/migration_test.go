//go:build e2e

package e2e_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestMigrationCommandIsIdempotent(t *testing.T) {
	stateDir := t.TempDir()
	cfg := sqliteFSConfig(stateDir)
	for attempt := 1; attempt <= 2; attempt++ {
		output, err := runLineageCommand(cfg, "migrate")
		if err != nil {
			t.Fatalf("migrate attempt %d: %v\n%s", attempt, err, output)
		}
		if !strings.Contains(output, "migrations applied (sqlite)") {
			t.Fatalf("migrate attempt %d did not report success: %s", attempt, output)
		}
	}

	s := startServerWith(t, cfg)
	createModelVersion(t, s, "migration-survivor", "1.0.0")
	s.stop(t)
	if output, err := runLineageCommand(cfg, "migrate"); err != nil {
		t.Fatalf("post-write migrate: %v\n%s", err, output)
	}

	restarted := startServerWith(t, cfg)
	model := restarted.json(t, http.MethodGet, restarted.modelURL+"/v1/models/migration-survivor", nil, http.StatusOK)
	if model["name"] != "migration-survivor" {
		t.Fatalf("data did not survive idempotent migration: %+v", model)
	}
	versions := restarted.json(t, http.MethodGet, restarted.modelURL+"/v1/models/migration-survivor/versions", nil, http.StatusOK)
	items, _ := versions["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("version did not survive idempotent migration: %+v", versions)
	}
}
