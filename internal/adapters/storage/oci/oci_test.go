package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func put(t *testing.T, b *Backend, path, content, mediaType string) string {
	t.Helper()
	uri, err := b.Put(context.Background(), path, strings.NewReader(content), int64(len(content)), mediaType)
	if err != nil {
		t.Fatalf("Put(%s): %v", path, err)
	}
	return uri
}

// The whole round trip: push bytes, get the canonical ref back, Stat it, read it back.
func TestPutStatGet(t *testing.T) {
	reg := newFakeRegistry(t)
	b := reg.backend(t, "lineage/models")
	ctx := context.Background()
	const body = "onnx bytes"

	uri := put(t, b, "fraud-detector/v1/model.onnx", body, "application/octet-stream")
	if want := "oci://" + reg.host() + "/lineage/models/fraud-detector:v1#model.onnx"; uri != want {
		t.Fatalf("Put returned %q, want %q", uri, want)
	}
	if got := b.URIFor("fraud-detector/v1/model.onnx"); got != uri {
		t.Fatalf("URIFor = %q, but Put returned %q — the two must agree", got, uri)
	}

	info, err := b.Stat(ctx, uri)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// The layer digest is the artifact's content digest: nothing is repacked on the way in, so
	// integrity verification on finalize costs one manifest read (§05.6).
	if !info.Exists || info.SizeBytes != int64(len(body)) || info.Digest != digestOf(body) {
		t.Fatalf("Stat = %+v, want exists with size %d and digest %s", info, len(body), digestOf(body))
	}

	rc, err := b.Get(ctx, uri)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != body {
		t.Fatalf("Get returned %q, want %q", got, body)
	}
}

// Every artifact of a version lands in one manifest, so `oci://…:<version>` is a single
// pullable reference to the whole model directory (§05.3.1).
func TestArtifactsShareOneManifest(t *testing.T) {
	reg := newFakeRegistry(t)
	b := reg.backend(t, "lineage/models")

	put(t, b, "fraud-detector/v1/model.onnx", "weights", "application/octet-stream")
	put(t, b, "fraud-detector/v1/config.json", "{}", "application/json")
	put(t, b, "fraud-detector/v1/tokenizer.json", "[]", "application/json")

	m := reg.manifestFor(t, "lineage/models/fraud-detector", "v1")
	if len(m.Layers) != 3 {
		t.Fatalf("manifest has %d layers, want 3", len(m.Layers))
	}
	titles := map[string]bool{}
	for _, l := range m.Layers {
		titles[l.title()] = true
	}
	for _, want := range []string{"model.onnx", "config.json", "tokenizer.json"} {
		if !titles[want] {
			t.Fatalf("manifest is missing layer %q; has %v", want, titles)
		}
	}
	if m.Config.Digest != emptyDigest {
		t.Fatalf("config digest = %s, want the empty descriptor %s", m.Config.Digest, emptyDigest)
	}
	if m.ArtifactType != artifactTypeModel {
		t.Fatalf("artifactType = %q, want %q", m.ArtifactType, artifactTypeModel)
	}

	// Statting the image (no #fragment) describes the whole thing.
	info, err := b.Stat(context.Background(), "oci://"+reg.host()+"/lineage/models/fraud-detector:v1")
	if err != nil {
		t.Fatalf("Stat image: %v", err)
	}
	if !info.Exists || info.SizeBytes != int64(len("weights")+len("{}")+len("[]")) {
		t.Fatalf("image Stat = %+v, want the summed layer sizes", info)
	}
}

// A second push of the same artifact name replaces its layer rather than duplicating it.
func TestPutReplacesLayerInPlace(t *testing.T) {
	reg := newFakeRegistry(t)
	b := reg.backend(t, "m")

	put(t, b, "fraud/v1/model.onnx", "v1 bytes", "")
	put(t, b, "fraud/v1/notes.txt", "notes", "")
	put(t, b, "fraud/v1/model.onnx", "corrected bytes", "")

	m := reg.manifestFor(t, "m/fraud", "v1")
	if len(m.Layers) != 2 {
		t.Fatalf("manifest has %d layers, want 2", len(m.Layers))
	}
	if m.Layers[0].title() != "model.onnx" {
		t.Fatalf("layer order changed on rewrite: %s first, want model.onnx", m.Layers[0].title())
	}
	if m.Layers[0].Digest != digestOf("corrected bytes") {
		t.Fatalf("layer digest = %s, want the rewritten bytes", m.Layers[0].Digest)
	}
}

// Concurrent uploads into one version must not lose a layer to a manifest read-modify-write
// race — the failure mode this driver's per-tag lock exists to prevent.
func TestConcurrentPutsKeepEveryLayer(t *testing.T) {
	reg := newFakeRegistry(t)
	b := reg.backend(t, "m")

	names := []string{"a.bin", "b.bin", "c.bin", "d.bin", "e.bin", "f.bin", "g.bin", "h.bin"}
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := b.Put(context.Background(), "fraud/v1/"+n, strings.NewReader("bytes of "+n), 0, ""); err != nil {
				t.Errorf("Put(%s): %v", n, err)
			}
		}()
	}
	wg.Wait()

	m := reg.manifestFor(t, "m/fraud", "v1")
	if len(m.Layers) != len(names) {
		t.Fatalf("manifest has %d layers, want %d — a concurrent write was lost", len(m.Layers), len(names))
	}
}

// Where the registry redirects blob reads, SignGet hands the consumer that URL and the bytes
// never touch Lineage (§05.7).
func TestSignGetReturnsRedirectTarget(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.blobRedirect = true
	b := reg.backend(t, "m")
	ctx := context.Background()

	uri := put(t, b, "fraud/v1/model.onnx", "weights", "")
	url, err := b.SignGet(ctx, uri, 0)
	if err != nil {
		t.Fatalf("SignGet: %v", err)
	}
	if !strings.Contains(url, "/presigned/") {
		t.Fatalf("SignGet returned %q, want the registry's redirect target", url)
	}
}

// A registry that serves blobs inline has nothing to sign — the caller must fall back to
// stream-through rather than get a URL that needs our credentials.
func TestSignGetUnsupportedWhenServedInline(t *testing.T) {
	reg := newFakeRegistry(t)
	b := reg.backend(t, "m")

	uri := put(t, b, "fraud/v1/model.onnx", "weights", "")
	if _, err := b.SignGet(context.Background(), uri, 0); !errors.Is(err, domain.ErrStorageUnsupported) {
		t.Fatalf("SignGet error = %v, want ErrStorageUnsupported", err)
	}
}

func TestStatMissing(t *testing.T) {
	reg := newFakeRegistry(t)
	b := reg.backend(t, "m")
	ctx := context.Background()

	// No such tag.
	info, err := b.Stat(ctx, "oci://"+reg.host()+"/m/fraud:nope#model.onnx")
	if err != nil {
		t.Fatalf("Stat of a missing tag should not error: %v", err)
	}
	if info.Exists {
		t.Fatal("Stat reported a non-existent manifest as existing")
	}

	// Tag exists, layer does not.
	put(t, b, "fraud/v1/model.onnx", "weights", "")
	info, err = b.Stat(ctx, "oci://"+reg.host()+"/m/fraud:v1#absent.bin")
	if err != nil {
		t.Fatalf("Stat of a missing layer should not error: %v", err)
	}
	if info.Exists {
		t.Fatal("Stat reported a non-existent layer as existing")
	}
}

// Deleting one artifact drops its layer; deleting the last one drops the manifest.
func TestDeleteLayerThenManifest(t *testing.T) {
	reg := newFakeRegistry(t)
	b := reg.backend(t, "m")
	ctx := context.Background()

	one := put(t, b, "fraud/v1/model.onnx", "weights", "")
	two := put(t, b, "fraud/v1/notes.txt", "notes", "")

	if err := b.Delete(ctx, one); err != nil {
		t.Fatalf("Delete layer: %v", err)
	}
	m := reg.manifestFor(t, "m/fraud", "v1")
	if len(m.Layers) != 1 || m.Layers[0].title() != "notes.txt" {
		t.Fatalf("after deleting one layer the manifest holds %+v, want just notes.txt", m.Layers)
	}

	if err := b.Delete(ctx, two); err != nil {
		t.Fatalf("Delete last layer: %v", err)
	}
	if reg.manifestExists("m/fraud", "v1") {
		t.Fatal("manifest survived the deletion of its last layer")
	}

	// Deleting again is a no-op, not an error — GC and DELETE both retry.
	if err := b.Delete(ctx, two); err != nil {
		t.Fatalf("second Delete should be idempotent: %v", err)
	}
}

// The bearer-token flow: challenge, exchange, retry — and the token is reused afterwards.
func TestBearerAuthAndTokenReuse(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.requireAuth = true
	reg.username, reg.password = "robot", "hunter2"
	b := reg.backend(t, "m")
	ctx := context.Background()

	uri := put(t, b, "fraud/v1/model.onnx", "weights", "")
	if _, err := b.Stat(ctx, uri); err != nil {
		t.Fatalf("Stat under auth: %v", err)
	}
	rc, err := b.Get(ctx, uri)
	if err != nil {
		t.Fatalf("Get under auth: %v", err)
	}
	rc.Close()

	// One exchange covers every request in the push and the reads that follow; a token per
	// request would make a large multi-artifact push pathological.
	if n := reg.tokenExchanges(); n != 1 {
		t.Fatalf("registry served %d token exchanges, want 1", n)
	}
}

// GC and multipart do not exist here, and must say so with the sentinel the core checks for.
func TestUnsupportedOperations(t *testing.T) {
	reg := newFakeRegistry(t)
	b := reg.backend(t, "m")
	ctx := context.Background()

	if _, err := b.ListObjects(ctx, ""); !errors.Is(err, domain.ErrStorageUnsupported) {
		t.Fatalf("ListObjects error = %v, want ErrStorageUnsupported", err)
	}
	if _, err := b.SignPut(ctx, "fraud/v1/model.onnx", 0); !errors.Is(err, domain.ErrStorageUnsupported) {
		t.Fatalf("SignPut error = %v, want ErrStorageUnsupported", err)
	}
	if _, err := b.InitiateMultipart(ctx, "p", 2, 1, 0); !errors.Is(err, domain.ErrStorageUnsupported) {
		t.Fatalf("InitiateMultipart error = %v, want ErrStorageUnsupported", err)
	}

	caps := b.Capabilities()
	if !caps.Signing || caps.SignPut || caps.Multipart || !caps.OCI {
		t.Fatalf("Capabilities = %+v, want read-signing and OCI only", caps)
	}
}

// An image reference names a directory, not a file; asking for its bytes is a client error
// with an actionable message rather than a confusing 404.
func TestGetImageRefRejected(t *testing.T) {
	reg := newFakeRegistry(t)
	b := reg.backend(t, "m")
	put(t, b, "fraud/v1/model.onnx", "weights", "")

	_, err := b.Get(context.Background(), "oci://"+reg.host()+"/m/fraud:v1")
	if err == nil {
		t.Fatal("Get of an image reference succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "#") {
		t.Fatalf("error %q should tell the caller to name a layer with #<artifact>", err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New("default", Config{}); err == nil {
		t.Fatal("New accepted an empty registry")
	}
	if _, err := New("default", Config{Registry: "ghcr.io/acme"}); err == nil {
		t.Fatal("New accepted a registry with a path")
	}
	if _, err := New("default", Config{Registry: "ghcr.io", Repository: "Acme/Models"}); err == nil {
		t.Fatal("New accepted an uppercase repository prefix")
	}
	if _, err := New("default", Config{Registry: "ghcr.io", Repository: "acme/models"}); err != nil {
		t.Fatalf("New rejected a valid config: %v", err)
	}
}

// Model names are looser than the distribution-spec name grammar, so the mapping has to
// produce something a registry will accept for every legal model name.
func TestSanitizeRepoComponent(t *testing.T) {
	cases := map[string]string{
		"fraud-detector": "fraud-detector",
		"a.b.c":          "a.b.c",
		"a..b":           "a.b",  // separator runs are illegal
		"-lead":          "lead", // and so is a leading one
		"trail-":         "trail",
	}
	for in, want := range cases {
		got := sanitizeRepoComponent(in)
		if got != want {
			t.Fatalf("sanitizeRepoComponent(%q) = %q, want %q", in, got, want)
		}
		if !domain.ValidOCIRepository(got) {
			t.Fatalf("sanitizeRepoComponent(%q) = %q, which no registry will accept", in, got)
		}
	}
}
