package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Multipart is the upload mode the CLI can only reach on a signing backend with a large
// artifact, so it is covered here against a stub that behaves like presigned part PUTs:
// the final part is short, and each part's ETag must be echoed back to finalize (§05.6).
func TestUploadPartsSendsEveryPartAndCollectsETags(t *testing.T) {
	const partSize, total = 5, 12 // 3 parts: 5, 5, 2

	var mu sync.Mutex
	got := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.URL.Path] = body
		mu.Unlock()
		w.Header().Set("ETag", `"etag`+r.URL.Path+`"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "big.bin")
	data := make([]byte, total)
	for i := range data {
		data[i] = byte('a' + i)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	ticket := map[string]any{"partSize": float64(partSize), "parts": []any{}}
	var planned []any
	for i := 1; i <= 3; i++ {
		planned = append(planned, map[string]any{"partNumber": float64(i), "url": fmt.Sprintf("%s/p%d", srv.URL, i)})
	}
	ticket["parts"] = planned

	parts := uploadParts(path, ticket)

	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(parts))
	}
	for i, p := range parts {
		if want := float64(i + 1); p["partNumber"] != want {
			t.Errorf("part %d partNumber = %v, want %v", i, p["partNumber"], want)
		}
		if want := fmt.Sprintf("etag/p%d", i+1); p["etag"] != want {
			t.Errorf("part %d etag = %v, want %q (quotes must be stripped)", i, p["etag"], want)
		}
	}
	// The bytes must be split in order, with a short final part — not padded to partSize.
	for path, want := range map[string]string{"/p1": "abcde", "/p2": "fghij", "/p3": "kl"} {
		if string(got[path]) != want {
			t.Errorf("body for %s = %q, want %q", path, got[path], want)
		}
	}
}

func TestPathBuildersEscape(t *testing.T) {
	if got, want := vp("my model", "1.0.0+a"), "/v1/models/my%20model/versions/1.0.0+a"; got != want {
		t.Errorf("vp() = %q, want %q", got, want)
	}
}
