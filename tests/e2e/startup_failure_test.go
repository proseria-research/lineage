//go:build e2e

package e2e_test

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestServerFailsFastOnInvalidConfiguration(t *testing.T) {
	t.Run("unknown database engine", func(t *testing.T) {
		cfg := sqliteFSConfig(t.TempDir())
		cfg.env["LINEAGE_DB_ENGINE"] = "not-a-database"
		output, err := runServerUntilExit(t, cfg, "")
		if err == nil || !strings.Contains(output, "unknown driver/engine: not-a-database") {
			t.Fatalf("invalid database exit=%v output=%s", err, output)
		}
	})

	t.Run("unknown storage driver", func(t *testing.T) {
		cfg := sqliteFSConfig(t.TempDir())
		cfg.env["LINEAGE_STORAGE_DRIVER"] = "not-storage"
		output, err := runServerUntilExit(t, cfg, "")
		if err == nil || !strings.Contains(output, "unknown driver/engine: not-storage") {
			t.Fatalf("invalid storage exit=%v output=%s", err, output)
		}
	})

	t.Run("occupied model API port", func(t *testing.T) {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		cfg := sqliteFSConfig(t.TempDir())
		output, err := runServerUntilExit(t, cfg, listener.Addr().String())
		if err == nil || !strings.Contains(output, "address already in use") {
			t.Fatalf("occupied port exit=%v output=%s", err, output)
		}
	})
}

func runServerUntilExit(t *testing.T, cfg serverConfig, modelAddr string) (string, error) {
	t.Helper()
	if modelAddr == "" {
		modelAddr = freeAddress(t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, testBinary)
	cmd.Dir = repositoryRoot
	cmd.Env = append(configEnvironment(cfg),
		"LINEAGE_MODEL_API_ADDR="+modelAddr,
		"LINEAGE_ADMIN_ADDR="+freeAddress(t),
		"LINEAGE_METRICS_ADDR="+freeAddress(t),
	)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("invalid server configuration did not fail within 5s; output=%s", output)
	}
	return string(output), err
}
