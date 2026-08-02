//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	startupTimeout  = 20 * time.Second
	shutdownTimeout = 12 * time.Second
)

var (
	repositoryRoot string
	testBinary     string
	buildTemp      string
)

// TestMain builds the exact process the tests exercise. Set LINEAGE_E2E_BINARY to use a
// prebuilt binary (for example, a release candidate produced by CI) instead.
func TestMain(m *testing.M) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "e2e: cannot locate repository root")
		os.Exit(1)
	}
	repositoryRoot = filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))

	var err error
	if supplied := os.Getenv("LINEAGE_E2E_BINARY"); supplied != "" {
		testBinary, err = filepath.Abs(supplied)
	} else {
		buildTemp, err = os.MkdirTemp("", "lineage-e2e-build-*")
		if err == nil {
			testBinary = filepath.Join(buildTemp, "lineage")
			err = buildLineage(testBinary)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e setup: %v\n", err)
		if buildTemp != "" {
			_ = os.RemoveAll(buildTemp)
		}
		os.Exit(1)
	}

	code := m.Run()
	if buildTemp != "" {
		_ = os.RemoveAll(buildTemp)
	}
	os.Exit(code)
}

func buildLineage(output string) error {
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-X main.version=e2e", "-o", output, "./cmd/lineage")
	cmd.Dir = repositoryRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build real lineage binary: %w\n%s", err, out)
	}
	return nil
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type serverProcess struct {
	modelURL string
	adminURL string
	opsURL   string
	client   *http.Client

	cmd     *exec.Cmd
	logs    *lockedBuffer
	done    chan struct{}
	waitErr error
	mu      sync.Mutex
	stopped bool
}

// startServer launches a real Lineage process with SQLite and filesystem storage rooted in
// stateDir. Reusing stateDir across launches verifies migrations and durable restart behavior.
func startServer(t *testing.T, stateDir string) *serverProcess {
	t.Helper()
	modelAddr := freeAddress(t)
	adminAddr := freeAddress(t)
	opsAddr := freeAddress(t)

	logs := &lockedBuffer{}
	cmd := exec.Command(testBinary)
	cmd.Dir = repositoryRoot
	cmd.Stdout = logs
	cmd.Stderr = logs
	cmd.Env = append(cleanEnvironment(os.Environ()),
		"LINEAGE_MODEL_API_ADDR="+modelAddr,
		"LINEAGE_ADMIN_ADDR="+adminAddr,
		"LINEAGE_METRICS_ADDR="+opsAddr,
		"LINEAGE_DB_ENGINE=sqlite",
		"LINEAGE_DB_PATH="+filepath.Join(stateDir, "lineage.db"),
		"LINEAGE_STORAGE_DRIVER=fs",
		"LINEAGE_STORAGE_ROOT="+filepath.Join(stateDir, "artifacts"),
		"LINEAGE_CACHE_ENGINE=memory",
		"LINEAGE_STORAGE_GC=retain",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lineage: %v", err)
	}

	s := &serverProcess{
		modelURL: "http://" + modelAddr,
		adminURL: "http://" + adminAddr,
		opsURL:   "http://" + opsAddr,
		client:   &http.Client{Timeout: 5 * time.Second},
		cmd:      cmd,
		logs:     logs,
		done:     make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.waitErr = err
		s.mu.Unlock()
		close(s.done)
	}()

	t.Cleanup(func() { s.stop(t) })
	s.waitReady(t)
	return s
}

func cleanEnvironment(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "LINEAGE_") || strings.HasPrefix(key, "OTEL_") {
			continue
		}
		clean = append(clean, item)
	}
	return clean
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release test port: %v", err)
	}
	return address
}

func (s *serverProcess) waitReady(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	checks := []string{s.opsURL + "/readyz", s.modelURL + "/v1/openapi.json", s.adminURL + "/api/overview"}
	for {
		allReady := true
		for _, url := range checks {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			resp, err := s.client.Do(req)
			if err != nil {
				allReady = false
				break
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				allReady = false
				break
			}
		}
		if allReady {
			return
		}

		select {
		case <-s.done:
			t.Fatalf("lineage exited during startup: %v\n--- process logs ---\n%s", s.processError(), s.logs.String())
		case <-ctx.Done():
			t.Fatalf("lineage was not ready within %s\n--- process logs ---\n%s", startupTimeout, s.logs.String())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// stop asks the binary to run its production graceful-shutdown path. A stuck process is
// killed after the same order of timeout as production, and its captured logs are reported.
func (s *serverProcess) stop(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()

	select {
	case <-s.done:
		t.Errorf("lineage exited before shutdown: %v\n--- process logs ---\n%s", s.processError(), s.logs.String())
		return
	default:
	}

	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Errorf("signal lineage: %v", err)
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.done:
		if err := s.processError(); err != nil {
			t.Errorf("lineage shutdown: %v\n--- process logs ---\n%s", err, s.logs.String())
		}
	case <-time.After(shutdownTimeout):
		_ = s.cmd.Process.Kill()
		<-s.done
		t.Errorf("lineage did not stop within %s\n--- process logs ---\n%s", shutdownTimeout, s.logs.String())
	}
}

func (s *serverProcess) processError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitErr
}
