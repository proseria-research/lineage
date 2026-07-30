package fs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Put must persist bytes under root (creating parent dirs), and Get/Stat must round-trip
// them with the correct size and sha256 — the stream-through delivery path (§05.6, §05.7).
func TestPutGetStatRoundTrip(t *testing.T) {
	root := t.TempDir()
	b := New("default", root)
	ctx := context.Background()

	payload := []byte("hello-bytes\x00\x01")
	uri, err := b.Put(ctx, "m/1.0.0/w.bin", strings.NewReader(string(payload)), int64(len(payload)), "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	if uri != "file://m/1.0.0/w.bin" {
		t.Fatalf("uri = %q, want file://m/1.0.0/w.bin", uri)
	}

	// Bytes are physically on disk under root.
	onDisk, err := os.ReadFile(filepath.Join(root, "m", "1.0.0", "w.bin"))
	if err != nil {
		t.Fatalf("expected file under root: %v", err)
	}
	if string(onDisk) != string(payload) {
		t.Fatalf("disk content mismatch: %q", onDisk)
	}

	// Get streams the same bytes back.
	rc, err := b.Get(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != string(payload) {
		t.Fatalf("Get mismatch: %q", got)
	}

	// Stat reports size and the sha256 digest.
	info, err := b.Stat(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if !info.Exists || info.SizeBytes != int64(len(payload)) || info.Digest != want {
		t.Fatalf("Stat = %+v, want exists,size=%d,digest=%s", info, len(payload), want)
	}

	// URIFor is consistent with what Put returned.
	if b.URIFor("m/1.0.0/w.bin") != uri {
		t.Fatalf("URIFor mismatch: %s vs %s", b.URIFor("m/1.0.0/w.bin"), uri)
	}
}
