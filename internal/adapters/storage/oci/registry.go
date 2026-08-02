package oci

// The OCI Distribution Spec v1.1 client (§05.3.1). Hand-rolled against the HTTP API for the
// same reason SigV4 is (see the s3 driver): the surface Lineage needs is small — pull a
// manifest, read a blob, push a blob, push a manifest — and a registry client dependency
// would drag in an image-handling stack we never use.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

const (
	mediaTypeManifest = "application/vnd.oci.image.manifest.v1+json"
	mediaTypeIndex    = "application/vnd.oci.image.index.v1+json"
	mediaTypeEmpty    = "application/vnd.oci.empty.v1+json"
	// mediaTypeDockerManifest is accepted on read: plenty of registries still hold Docker
	// schema-2 manifests, and a model image built by an ordinary `docker build` is one.
	mediaTypeDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"

	// artifactTypeModel marks a manifest Lineage pushed, so a registry browser can tell a
	// model artifact from a runnable image.
	artifactTypeModel = "application/vnd.lineage.model.v1+json"

	// titleAnnotation names a layer's file inside the manifest — the ORAS convention, and
	// what an `oci://…#name` fragment matches against.
	titleAnnotation = "org.opencontainers.image.title"

	// emptyDescriptor is the spec-mandated placeholder config for a non-image artifact.
	emptyDigest  = "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
	emptyContent = "{}"
)

// descriptor is an OCI content descriptor.
type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// title returns the descriptor's file name, or "" when it carries none.
func (d descriptor) title() string { return d.Annotations[titleAnnotation] }

// manifest is the subset of an OCI image manifest Lineage reads and writes.
type manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType,omitempty"`
	Config        descriptor        `json:"config"`
	Layers        []descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// layer finds a layer by its title annotation.
func (m *manifest) layer(title string) (descriptor, bool) {
	for _, l := range m.Layers {
		if l.title() == title {
			return l, true
		}
	}
	return descriptor{}, false
}

// client speaks the distribution API to one registry host.
type client struct {
	scheme string // https, or http when plainHTTP is set
	host   string
	auth   *authorizer
	hc     *http.Client // follows redirects (blob reads)
	noRdr  *http.Client // stops at the first redirect (SignGet)
}

func newClient(host string, plainHTTP bool, username, password string) *client {
	scheme := "https"
	if plainHTTP {
		scheme = "http"
	}
	hc := &http.Client{Timeout: 10 * time.Minute}
	noRdr := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &client{scheme: scheme, host: host, auth: newAuthorizer(username, password, hc), hc: hc, noRdr: noRdr}
}

// do performs an authenticated request against an absolute url, retrying once against the
// registry's WWW-Authenticate challenge. body must be re-readable for that retry, so it is
// taken as a factory rather than a reader.
func (c *client) do(ctx context.Context, hc *http.Client, method, rawURL, scope string, body func() io.Reader, headers map[string]string) (*http.Response, error) {
	send := func(authz string) (*http.Response, error) {
		var r io.Reader
		if body != nil {
			r = body()
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, r)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		return hc.Do(req)
	}

	// Try a cached token for this scope first; most requests never see a 401.
	resp, err := send(c.cached(scope))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	ch, ok := parseChallenge(resp.Header.Get("WWW-Authenticate"))
	resp.Body.Close()
	if !ok {
		return nil, domain.Internal("oci: registry returned 401 with no usable challenge")
	}
	if ch.params["scope"] == "" && scope != "" {
		ch.params["scope"] = scope // some registries omit it; ask for what we need
	}
	authz, err := c.auth.authorize(ctx, ch, scope)
	if err != nil {
		return nil, err
	}
	return send(authz)
}

// doStream sends a request whose body is a one-shot reader. The 401-retry `do` performs is
// impossible here — the stream would already be drained — so the token is obtained up front
// and a 401 is reported rather than retried into a silently empty upload.
func (c *client) doStream(ctx context.Context, method, rawURL, scope string, body io.Reader, headers map[string]string) (*http.Response, error) {
	authz, err := c.warm(ctx, scope)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		defer resp.Body.Close()
		return nil, domain.Internal("oci: registry rejected the upload session's credentials mid-stream")
	}
	return resp, nil
}

// warm returns a usable Authorization header for scope, probing /v2/ for a challenge when
// nothing is cached yet.
func (c *client) warm(ctx context.Context, scope string) (string, error) {
	if v := c.cached(scope); v != "" {
		return v, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.scheme+"://"+c.host+"/v2/", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return "", nil // the registry is open; no header needed
	}
	ch, ok := parseChallenge(resp.Header.Get("WWW-Authenticate"))
	if !ok {
		return "", domain.Internal("oci: registry returned 401 with no usable challenge")
	}
	ch.params["scope"] = scope
	return c.auth.authorize(ctx, ch, scope)
}

// cached returns a bearer token already held for scope, or the static Basic credential. A
// pull is satisfied by a token already issued for pull,push on the same repository — the
// broader grant contains it, and reusing it halves the token traffic of a publish that reads
// its own manifest back.
func (c *client) cached(scope string) string {
	candidates := []string{scope}
	if repo, ok := strings.CutSuffix(scope, ":pull"); ok {
		candidates = append(candidates, repo+":pull,push")
	}
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()
	now := time.Now()
	for _, want := range candidates {
		for k, t := range c.auth.tokens {
			if strings.HasSuffix(k, "|"+want) && now.Before(t.expiresAt) {
				return t.value
			}
		}
	}
	return c.auth.basic()
}

func (c *client) url(repo, rest string) string {
	return c.scheme + "://" + c.host + "/v2/" + repo + rest
}

func pullScope(repo string) string { return "repository:" + repo + ":pull" }
func pushScope(repo string) string { return "repository:" + repo + ":pull,push" }

// manifestAccept lists the manifest types we can parse, in preference order.
const manifestAccept = mediaTypeManifest + ", " + mediaTypeDockerManifest + ", " + mediaTypeIndex

// getManifest fetches and parses a manifest, also returning its own digest (from
// Docker-Content-Digest where the registry supplies it, else computed over the bytes).
func (c *client) getManifest(ctx context.Context, repo, ref string) (*manifest, string, error) {
	resp, err := c.do(ctx, c.hc, "GET", c.url(repo, "/manifests/"+ref), pullScope(repo), nil,
		map[string]string{"Accept": manifestAccept})
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", errManifestNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", httpErr("get-manifest", resp)
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "", err
	}
	var m manifest
	if err := json.Unmarshal(buf, &m); err != nil {
		return nil, "", domain.Internal("oci: parse manifest: " + err.Error())
	}
	dig := resp.Header.Get("Docker-Content-Digest")
	if !domain.ValidDigest(dig) {
		sum := sha256.Sum256(buf)
		dig = "sha256:" + hex.EncodeToString(sum[:])
	}
	return &m, dig, nil
}

// errManifestNotFound distinguishes "no such tag" from a transport failure, so Stat can
// report a non-existent object rather than an error (§05.2).
var errManifestNotFound = domain.NotFound("oci: manifest not found")

// putManifest uploads a manifest under ref and returns its digest.
func (c *client) putManifest(ctx context.Context, repo, ref string, m *manifest) (string, error) {
	buf, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	dig := "sha256:" + hex.EncodeToString(sum[:])
	resp, err := c.do(ctx, c.hc, "PUT", c.url(repo, "/manifests/"+ref), pushScope(repo),
		func() io.Reader { return bytes.NewReader(buf) },
		map[string]string{"Content-Type": m.MediaType})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", httpErr("put-manifest", resp)
	}
	return dig, nil
}

// deleteManifest removes a manifest by digest. Many registries disable this by policy; the
// error is surfaced rather than swallowed so an operator sees why nothing was freed.
func (c *client) deleteManifest(ctx context.Context, repo, dig string) error {
	resp, err := c.do(ctx, c.hc, "DELETE", c.url(repo, "/manifests/"+dig), pushScope(repo), nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusAccepted, http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil // delete is idempotent
	default:
		return httpErr("delete-manifest", resp)
	}
}

// headBlob reports a blob's size, or exists=false when the registry does not hold it.
func (c *client) headBlob(ctx context.Context, repo, dig string) (exists bool, size int64, err error) {
	resp, err := c.do(ctx, c.hc, "HEAD", c.url(repo, "/blobs/"+dig), pullScope(repo), nil, nil)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, 0, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, 0, httpErr("head-blob", resp)
	}
	size, _ = strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return true, size, nil
}

// getBlob streams a blob's bytes.
func (c *client) getBlob(ctx context.Context, repo, dig string) (io.ReadCloser, error) {
	resp, err := c.do(ctx, c.hc, "GET", c.url(repo, "/blobs/"+dig), pullScope(repo), nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, httpErr("get-blob", resp)
	}
	return resp.Body, nil
}

// blobRedirect asks for a blob without following the redirect. Registries backed by object
// storage answer a blob GET with a 307 to a presigned URL — that Location *is* a signed
// download URL, so the read path offloads exactly as it does on s3. Registries that serve
// bytes inline return 200 instead, and the caller falls back to stream-through (§05.7).
func (c *client) blobRedirect(ctx context.Context, repo, dig string) (string, error) {
	resp, err := c.do(ctx, c.noRdr, "GET", c.url(repo, "/blobs/"+dig), pullScope(repo), nil, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", domain.ErrStorageUnsupported
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", domain.ErrStorageUnsupported
	}
	// A relative Location points back at the registry and would need our credentials, so it
	// is no use to an unauthenticated consumer.
	u, err := url.Parse(loc)
	if err != nil || !u.IsAbs() {
		return "", domain.ErrStorageUnsupported
	}
	return loc, nil
}

// pushBlob uploads bytes to the repository and returns their descriptor. It streams: the
// upload session is opened, the bytes are PATCHed through a hasher, and the digest computed
// on the way past closes the session. Nothing is buffered whole in memory.
func (c *client) pushBlob(ctx context.Context, repo string, r io.Reader, mediaType string) (descriptor, error) {
	loc, err := c.startUpload(ctx, repo)
	if err != nil {
		return descriptor{}, err
	}

	h := sha256.New()
	counted := &countingReader{r: io.TeeReader(r, h)}
	// A single PATCH carries the whole stream: chunking exists to make a resume possible,
	// which we do not attempt — a failed push is simply retried from the start.
	resp, err := c.doStream(ctx, "PATCH", loc, pushScope(repo), counted,
		map[string]string{"Content-Type": "application/octet-stream"})
	if err != nil {
		return descriptor{}, err
	}
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
		defer resp.Body.Close()
		return descriptor{}, httpErr("patch-blob", resp)
	}
	if next := resp.Header.Get("Location"); next != "" {
		loc = c.absolute(next)
	}
	resp.Body.Close()

	dig := "sha256:" + hex.EncodeToString(h.Sum(nil))
	closeURL, err := withQuery(loc, "digest", dig)
	if err != nil {
		return descriptor{}, err
	}
	resp, err = c.do(ctx, c.hc, "PUT", closeURL, pushScope(repo), nil,
		map[string]string{"Content-Length": "0"})
	if err != nil {
		return descriptor{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return descriptor{}, httpErr("put-blob", resp)
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return descriptor{MediaType: mediaType, Digest: dig, Size: counted.n}, nil
}

// ensureEmptyConfig uploads the 2-byte empty config blob every non-image manifest points at.
func (c *client) ensureEmptyConfig(ctx context.Context, repo string) (descriptor, error) {
	d := descriptor{MediaType: mediaTypeEmpty, Digest: emptyDigest, Size: int64(len(emptyContent))}
	exists, _, err := c.headBlob(ctx, repo, emptyDigest)
	if err != nil {
		return descriptor{}, err
	}
	if exists {
		return d, nil
	}
	if _, err := c.pushBlob(ctx, repo, strings.NewReader(emptyContent), mediaTypeEmpty); err != nil {
		return descriptor{}, err
	}
	return d, nil
}

// startUpload opens a blob upload session and returns its absolute location.
func (c *client) startUpload(ctx context.Context, repo string) (string, error) {
	resp, err := c.do(ctx, c.hc, "POST", c.url(repo, "/blobs/uploads/"), pushScope(repo), nil,
		map[string]string{"Content-Length": "0"})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
		return "", httpErr("start-upload", resp)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", domain.Internal("oci: upload session returned no Location")
	}
	return c.absolute(loc), nil
}

// absolute resolves a registry-relative Location against the registry root.
func (c *client) absolute(loc string) string {
	if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
		return loc
	}
	if !strings.HasPrefix(loc, "/") {
		loc = "/" + loc
	}
	return c.scheme + "://" + c.host + loc
}

func withQuery(rawURL, k, v string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", domain.Internal("oci: bad upload location: " + err.Error())
	}
	q := u.Query()
	q.Set(k, v)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// countingReader records how many bytes passed through, so a streamed push can report the
// blob size it just wrote without knowing it up front.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// httpErr renders a registry error body into a coded domain error.
func httpErr(op string, resp *http.Response) error {
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	msg := "oci " + op + ": " + resp.Status
	if json.Unmarshal(buf, &e) == nil && len(e.Errors) > 0 {
		msg += ": " + e.Errors[0].Code + " " + e.Errors[0].Message
	} else if len(buf) > 0 {
		msg += ": " + string(buf)
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return domain.NotFound(msg)
	case http.StatusUnauthorized, http.StatusForbidden:
		return domain.Internal(msg + " (check LINEAGE_OCI_USERNAME/PASSWORD and repository scope)")
	default:
		return domain.Internal(msg)
	}
}
