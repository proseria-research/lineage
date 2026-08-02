package oci

// An in-process OCI Distribution v1.1 registry, enough of one to drive the driver end to end:
// the bearer-token challenge, blob upload sessions, blob reads (inline or via redirect), and
// manifest get/put/delete. It lets the round-trip tests run with no container and no network.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type fakeRegistry struct {
	t   *testing.T
	srv *httptest.Server

	// requireAuth turns on the 401 → token → retry flow; blobRedirect makes blob reads answer
	// 307 to a "presigned" URL the way an object-store-backed registry does.
	requireAuth  bool
	blobRedirect bool
	username     string
	password     string

	mu        sync.Mutex
	blobs     map[string][]byte            // repo|digest → bytes
	manifests map[string][]byte            // repo|reference → manifest json
	uploads   map[string][]byte            // upload id → buffered bytes
	tokens    int                          // token exchanges served
	requests  []string                     // method + path, for assertions
	seen      map[string]map[string]string // repo|digest → nothing; reserved
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{
		t:         t,
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		uploads:   map[string][]byte{},
		seen:      map[string]map[string]string{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

// host returns the registry's host:port, which is what the backend config takes.
func (f *fakeRegistry) host() string { return strings.TrimPrefix(f.srv.URL, "http://") }

// backend builds a driver pointed at this registry.
func (f *fakeRegistry) backend(t *testing.T, prefix string) *Backend {
	t.Helper()
	b, err := New("default", Config{
		Registry: f.host(), Repository: prefix,
		Username: f.username, Password: f.password, PlainHTTP: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func (f *fakeRegistry) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
}

func (f *fakeRegistry) handle(w http.ResponseWriter, r *http.Request) {
	f.record(r)

	if r.URL.Path == "/token" {
		f.serveToken(w, r)
		return
	}
	// A "presigned" blob URL: no credentials, opaque path — exactly what a redirect target is.
	if strings.HasPrefix(r.URL.Path, "/presigned/") {
		f.mu.Lock()
		body, ok := f.blobs[r.URL.Query().Get("k")]
		f.mu.Unlock()
		if !ok {
			http.Error(w, "gone", http.StatusNotFound)
			return
		}
		w.Write(body)
		return
	}
	if f.requireAuth && !f.authorized(r) {
		w.Header().Set("WWW-Authenticate",
			`Bearer realm="`+f.srv.URL+`/token",service="fake",scope="repository:x:pull,push"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	path, ok := strings.CutPrefix(r.URL.Path, "/v2/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	switch {
	case strings.Contains(path, "/blobs/uploads/"):
		repo, id, _ := strings.Cut(path, "/blobs/uploads/")
		f.serveUpload(w, r, repo, id)
	case strings.Contains(path, "/blobs/"):
		repo, dig, _ := strings.Cut(path, "/blobs/")
		f.serveBlob(w, r, repo, dig)
	case strings.Contains(path, "/manifests/"):
		repo, ref, _ := strings.Cut(path, "/manifests/")
		f.serveManifest(w, r, repo, ref)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (f *fakeRegistry) authorized(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (f *fakeRegistry) serveToken(w http.ResponseWriter, r *http.Request) {
	if f.username != "" {
		u, p, ok := r.BasicAuth()
		if !ok || u != f.username || p != f.password {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
	}
	f.mu.Lock()
	f.tokens++
	f.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]any{"token": "tok", "expires_in": 300})
}

func (f *fakeRegistry) serveUpload(w http.ResponseWriter, r *http.Request, repo, id string) {
	switch r.Method {
	case "POST":
		f.mu.Lock()
		id := "u" + strconv.Itoa(len(f.uploads)+1)
		f.uploads[id] = nil
		f.mu.Unlock()
		w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+id)
		w.WriteHeader(http.StatusAccepted)
	case "PATCH":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.uploads[id] = append(f.uploads[id], body...)
		f.mu.Unlock()
		w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+id)
		w.WriteHeader(http.StatusAccepted)
	case "PUT":
		want := r.URL.Query().Get("digest")
		f.mu.Lock()
		body := f.uploads[id]
		delete(f.uploads, id)
		f.mu.Unlock()
		sum := sha256.Sum256(body)
		got := "sha256:" + hex.EncodeToString(sum[:])
		if want != got {
			// The registry verifies the digest the client closed the session with; a mismatch
			// here would mean our streamed hash disagreed with the bytes that arrived.
			http.Error(w, "digest mismatch: "+want+" != "+got, http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.blobs[repo+"|"+got] = body
		f.mu.Unlock()
		w.Header().Set("Docker-Content-Digest", got)
		w.WriteHeader(http.StatusCreated)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (f *fakeRegistry) serveBlob(w http.ResponseWriter, r *http.Request, repo, dig string) {
	f.mu.Lock()
	body, ok := f.blobs[repo+"|"+dig]
	f.mu.Unlock()
	if !ok {
		http.Error(w, "blob unknown", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Docker-Content-Digest", dig)
	if r.Method == "HEAD" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if f.blobRedirect {
		http.Redirect(w, r, f.srv.URL+"/presigned/blob?k="+repo+"|"+dig, http.StatusTemporaryRedirect)
		return
	}
	w.Write(body)
}

func (f *fakeRegistry) serveManifest(w http.ResponseWriter, r *http.Request, repo, ref string) {
	key := repo + "|" + ref
	switch r.Method {
	case "PUT":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sum := sha256.Sum256(body)
		dig := "sha256:" + hex.EncodeToString(sum[:])
		f.mu.Lock()
		f.manifests[key] = body
		f.manifests[repo+"|"+dig] = body // also addressable by digest
		f.mu.Unlock()
		w.Header().Set("Docker-Content-Digest", dig)
		w.WriteHeader(http.StatusCreated)
	case "GET", "HEAD":
		f.mu.Lock()
		body, ok := f.manifests[key]
		f.mu.Unlock()
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"errors":[{"code":"MANIFEST_UNKNOWN","message":"not found"}]}`))
			return
		}
		sum := sha256.Sum256(body)
		w.Header().Set("Content-Type", mediaTypeManifest)
		w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(sum[:]))
		if r.Method == "HEAD" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Write(body)
	case "DELETE":
		f.mu.Lock()
		body, ok := f.manifests[key]
		if ok {
			delete(f.manifests, key)
			// Drop every reference to the same manifest, tag and digest alike.
			for k, v := range f.manifests {
				if strings.HasPrefix(k, repo+"|") && string(v) == string(body) {
					delete(f.manifests, k)
				}
			}
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// manifestFor returns the stored manifest for a repo reference, parsed.
func (f *fakeRegistry) manifestFor(t *testing.T, repo, ref string) manifest {
	t.Helper()
	f.mu.Lock()
	body, ok := f.manifests[repo+"|"+ref]
	f.mu.Unlock()
	if !ok {
		t.Fatalf("no manifest stored for %s:%s", repo, ref)
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("parse stored manifest: %v", err)
	}
	return m
}

func (f *fakeRegistry) manifestExists(repo, ref string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.manifests[repo+"|"+ref]
	return ok
}

func (f *fakeRegistry) tokenExchanges() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokens
}
