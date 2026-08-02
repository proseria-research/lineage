package oci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// Live round-trip against a real OCI registry. Skipped unless configured, so CI stays
// hermetic. The fake registry covers the protocol; this covers a real implementation of it —
// in particular that a manifest we push is one `oras pull` / `docker pull` accepts.
//
//	docker run -d -p5000:5000 --name registry registry:2
//	LINEAGE_TEST_OCI_REGISTRY=localhost:5000 LINEAGE_TEST_OCI_PLAIN_HTTP=1 \
//	go test ./internal/adapters/storage/oci/ -run Integration -v
//
//	# then, to confirm an OCI-native consumer sees what Lineage wrote:
//	oras manifest fetch --plain-http localhost:5000/lineage-it/fraud-detector:<version>
func TestOCIIntegrationRoundTrip(t *testing.T) {
	registry := os.Getenv("LINEAGE_TEST_OCI_REGISTRY")
	if registry == "" {
		t.Skip("set LINEAGE_TEST_OCI_REGISTRY (e.g. localhost:5000) to run the live registry test")
	}
	b, err := New("it", Config{
		Registry:   registry,
		Repository: envOr("LINEAGE_TEST_OCI_REPOSITORY", "lineage-it"),
		Username:   os.Getenv("LINEAGE_TEST_OCI_USERNAME"),
		Password:   os.Getenv("LINEAGE_TEST_OCI_PASSWORD"),
		PlainHTTP:  os.Getenv("LINEAGE_TEST_OCI_PLAIN_HTTP") != "",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// A fresh tag per run, so repeated runs against a persistent registry stay independent.
	version := "v" + strconv.FormatInt(time.Now().Unix(), 10)
	weights := []byte("onnx weights \x00\x01\x02")
	config := []byte(`{"task":"binary-classification"}`)
	sum := sha256.Sum256(weights)
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])

	weightsURI, err := b.Put(ctx, "fraud-detector/"+version+"/model.onnx", bytes.NewReader(weights), int64(len(weights)), "application/octet-stream")
	if err != nil {
		t.Fatalf("Put weights: %v", err)
	}
	if _, err := b.Put(ctx, "fraud-detector/"+version+"/config.json", bytes.NewReader(config), int64(len(config)), "application/json"); err != nil {
		t.Fatalf("Put config: %v", err)
	}

	info, err := b.Stat(ctx, weightsURI)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.Exists || info.Digest != wantDigest || info.SizeBytes != int64(len(weights)) {
		t.Fatalf("Stat = %+v, want exists with digest %s and size %d", info, wantDigest, len(weights))
	}

	rc, err := b.Get(ctx, weightsURI)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if !bytes.Equal(got, weights) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(got), len(weights))
	}

	// Both artifacts must be reachable from the one image reference — that is the property a
	// modelcars/ORAS pull depends on.
	ref, err := domain.ParseOCIURI(weightsURI)
	if err != nil {
		t.Fatal(err)
	}
	image, err := b.Stat(ctx, ref.Image())
	if err != nil {
		t.Fatalf("Stat image: %v", err)
	}
	if want := int64(len(weights) + len(config)); image.SizeBytes != want {
		t.Fatalf("image size = %d, want %d (both layers)", image.SizeBytes, want)
	}
	t.Logf("pushed %s (manifest %s)", ref.Image(), image.Digest)

	// What an OCI-native consumer sees: a plain HTTP GET with the standard Accept header, no
	// Lineage code in the path. If this does not come back as a well-formed manifest with our
	// layers titled, `oras pull` and a modelcars mount would not work either.
	assertManifestIsOCINative(t, b, ref, wantDigest)

	// SignGet is best-effort: registry:2 on local disk serves blobs inline, an object-store
	// backed one redirects. Both outcomes are correct — only an unexpected error is not.
	if url, err := b.SignGet(ctx, weightsURI, time.Minute); err != nil {
		if err != domain.ErrStorageUnsupported {
			t.Fatalf("SignGet: %v", err)
		}
		t.Log("registry serves blobs inline; delivery falls back to stream-through")
	} else {
		t.Logf("registry redirects blob reads: %s", url)
	}

	t.Cleanup(func() {
		if err := b.Delete(context.Background(), ref.Image()); err != nil {
			t.Logf("cleanup: %v (registries often disable manifest deletion)", err)
		}
	})
}

// assertManifestIsOCINative fetches the manifest the way any registry client would and checks
// it is spec-shaped: the OCI manifest media type, an artifactType, and a layer per artifact
// carrying its file name and its content digest.
func assertManifestIsOCINative(t *testing.T, b *Backend, ref domain.OCIRef, weightsDigest string) {
	t.Helper()
	scheme := "https"
	if b.cfg.PlainHTTP {
		scheme = "http"
	}
	url := scheme + "://" + ref.Registry + "/v2/" + ref.Repository + "/manifests/" + ref.Reference()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", mediaTypeManifest)
	if u := os.Getenv("LINEAGE_TEST_OCI_USERNAME"); u != "" {
		req.SetBasicAuth(u, os.Getenv("LINEAGE_TEST_OCI_PASSWORD"))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("plain manifest fetch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Fatalf("plain manifest fetch: %s: %s", resp.Status, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != mediaTypeManifest {
		t.Fatalf("manifest Content-Type = %q, want %q", ct, mediaTypeManifest)
	}
	var m manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.SchemaVersion != 2 || m.ArtifactType != artifactTypeModel {
		t.Fatalf("manifest = schemaVersion %d artifactType %q, want 2 / %s", m.SchemaVersion, m.ArtifactType, artifactTypeModel)
	}
	l, ok := m.layer("model.onnx")
	if !ok {
		t.Fatalf("manifest has no layer titled model.onnx: %+v", m.Layers)
	}
	// The layer digest being the artifact digest is the invariant the whole design rests on.
	if l.Digest != weightsDigest {
		t.Fatalf("layer digest = %s, want the artifact's content digest %s", l.Digest, weightsDigest)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
