//go:build e2e

package e2e_test

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGracefulShutdownCompletesInflightUpload(t *testing.T) {
	stateDir := t.TempDir()
	s := startServer(t, stateDir)
	const model, version, artifact = "shutdown-upload", "1.0.0", "model.bin"
	createModelVersion(t, s, model, version)
	ticket := initiateUpload(t, s, model, version, artifact, 0)
	if !ticket.StreamThrough {
		t.Fatalf("filesystem upload should stream through: %+v", ticket)
	}

	reader, writer := io.Pipe()
	req, err := http.NewRequest(http.MethodPut, s.modelURL+ticket.ContentURL, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	result := make(chan response, 1)
	errs := make(chan error, 1)
	go func() {
		resp, err := s.client.Do(req)
		if err != nil {
			errs <- err
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			errs <- err
			return
		}
		result <- response{status: resp.StatusCode, header: resp.Header.Clone(), body: body}
	}()

	first := bytes.Repeat([]byte("a"), 256*1024)
	second := bytes.Repeat([]byte("b"), 256*1024)
	if _, err := writer.Write(first); err != nil {
		t.Fatal(err)
	}
	// Write on the pipe only returns after the handler consumes the first chunk, so the
	// request is active when SIGINT enters the production shutdown path.
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(second); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()

	select {
	case err := <-errs:
		t.Fatalf("in-flight upload failed during graceful shutdown: %v\n%s", err, s.logs.String())
	case uploaded := <-result:
		if uploaded.status != http.StatusNoContent {
			t.Fatalf("in-flight upload status=%d body=%s", uploaded.status, uploaded.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight upload did not finish during graceful shutdown")
	}
	select {
	case <-s.done:
		if err := s.processError(); err != nil {
			t.Fatalf("graceful shutdown exit: %v\n%s", err, s.logs.String())
		}
	case <-time.After(shutdownTimeout):
		t.Fatal("server did not exit after in-flight upload completed")
	}

	want := append(first, second...)
	stored, err := os.ReadFile(filepath.Join(stateDir, "artifacts", model, version, artifact))
	if err != nil || !bytes.Equal(stored, want) {
		t.Fatalf("in-flight bytes were truncated: size=%d want=%d err=%v", len(stored), len(want), err)
	}
}
