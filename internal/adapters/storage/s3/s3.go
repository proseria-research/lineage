// Package s3 is the S3-compatible StorageBackend (AWS · MinIO · R2 · Ceph). It supports
// signed GET/PUT (presigned URLs) so bytes flow directly between the object store and the
// consumer/uploader; the core never proxies them (§05.1, §05.3). SigV4 is hand-rolled
// (sigv4.go) to keep the binary dependency-light and work against any S3 endpoint.
package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// Config configures an S3-compatible backend (§05.4). Credentials come from the
// environment/secret at wiring time and never appear in API responses.
type Config struct {
	Bucket       string
	Region       string
	Endpoint     string // e.g. https://s3.amazonaws.com, https://<acct>.r2.cloudflarestorage.com, http://minio:9000
	AccessKey    string
	SecretKey    string
	SessionToken string
	PathStyle    bool // required for MinIO/Ceph and many R2 setups; AWS uses virtual-host
}

type Backend struct {
	name   string
	cfg    Config
	sign   signer
	creds  *resolver
	scheme string
	host   string // endpoint host[:port]
	hc     *http.Client
}

func New(name string, cfg Config) (*Backend, error) {
	if cfg.Bucket == "" {
		return nil, domain.Invalid("s3 backend: bucket is required")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://s3." + cfg.Region + ".amazonaws.com"
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, domain.Invalid("s3 backend: bad endpoint: " + err.Error())
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	return &Backend{
		name:   name,
		cfg:    cfg,
		sign:   signer{region: cfg.Region},
		creds:  buildResolver(cfg, hc),
		scheme: u.Scheme,
		host:   u.Host,
		hc:     hc,
	}, nil
}

var _ domain.StorageBackend = (*Backend)(nil)

func (b *Backend) Name() string { return b.name }

func (b *Backend) Capabilities() domain.StorageCapabilities {
	return domain.StorageCapabilities{Signing: true, Ranges: true, Multipart: true}
}

// endpoint resolves the request host and canonical (path-encoded) URI for bucket/key,
// honoring path-style vs virtual-hosted addressing.
func (b *Backend) endpoint(bucket, key string) (host, canonicalURI string) {
	enc := uriEncode("/"+key, false)
	if b.cfg.PathStyle {
		return b.host, "/" + bucket + enc
	}
	return bucket + "." + b.host, enc
}

// parse splits an s3://bucket/key uri. A bare key (no scheme) is treated as a key in the
// configured bucket, matching the storagePath convention (§05.5).
func (b *Backend) parse(uri string) (bucket, key string, err error) {
	if !strings.HasPrefix(uri, "s3://") {
		return b.cfg.Bucket, strings.TrimPrefix(uri, "/"), nil
	}
	rest := strings.TrimPrefix(uri, "s3://")
	bucket, key, ok := strings.Cut(rest, "/")
	if !ok || bucket == "" || key == "" {
		return "", "", domain.Invalid("s3: malformed uri '" + uri + "'")
	}
	return bucket, key, nil
}

func (b *Backend) SignGet(ctx context.Context, uri string, ttl time.Duration) (string, error) {
	bucket, key, err := b.parse(uri)
	if err != nil {
		return "", err
	}
	cr, err := b.creds.resolve(ctx)
	if err != nil {
		return "", err
	}
	host, canonicalURI := b.endpoint(bucket, key)
	return b.sign.presign(cr, "GET", b.scheme, host, canonicalURI, url.Values{}, ttl, time.Now()), nil
}

func (b *Backend) SignPut(ctx context.Context, path string, ttl time.Duration) (domain.SignedRequest, error) {
	cr, err := b.creds.resolve(ctx)
	if err != nil {
		return domain.SignedRequest{}, err
	}
	host, canonicalURI := b.endpoint(b.cfg.Bucket, strings.TrimPrefix(path, "/"))
	u := b.sign.presign(cr, "PUT", b.scheme, host, canonicalURI, url.Values{}, ttl, time.Now())
	return domain.SignedRequest{
		URL:       u,
		Method:    "PUT",
		ExpiresAt: domain.NowMillis() + ttl.Milliseconds(),
	}, nil
}

func (b *Backend) Stat(ctx context.Context, uri string) (domain.ObjectInfo, error) {
	bucket, key, err := b.parse(uri)
	if err != nil {
		return domain.ObjectInfo{}, err
	}
	resp, err := b.do(ctx, "HEAD", bucket, key, nil, unsignedload, nil, nil)
	if err != nil {
		return domain.ObjectInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return domain.ObjectInfo{Exists: false}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return domain.ObjectInfo{}, b.httpErr("stat", resp)
	}
	info := domain.ObjectInfo{Exists: true}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		info.SizeBytes, _ = strconv.ParseInt(cl, 10, 64)
	}
	// S3 ETag is only a sha256 when we wrote it that way; the real content digest is the
	// x-amz-meta-sha256 we set on Put. ETag (often MD5) is deliberately not surfaced as a
	// content digest — callers verify sha256 explicitly on finalize (§05.6).
	if d := resp.Header.Get("x-amz-meta-sha256"); d != "" {
		info.Digest = "sha256:" + d
	}
	return info, nil
}

func (b *Backend) Get(ctx context.Context, uri string) (io.ReadCloser, error) {
	bucket, key, err := b.parse(uri)
	if err != nil {
		return nil, err
	}
	resp, err := b.do(ctx, "GET", bucket, key, nil, unsignedload, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, b.httpErr("get", resp)
	}
	return resp.Body, nil
}

// Put uploads size bytes to path in the configured bucket, tagging the object with the
// content sha256 (x-amz-meta-sha256) so a later Stat can report the digest cheaply.
func (b *Backend) Put(ctx context.Context, path string, r io.Reader, size int64, contentType string) (string, error) {
	key := strings.TrimPrefix(path, "/")
	// SigV4 needs the payload hash; buffer to hash then send. Stream-through is for small/
	// DOC artifacts — large files use the signed direct PUT path instead (§05.6).
	body, err := io.ReadAll(io.LimitReader(r, size+1))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	resp, err := b.do(ctx, "PUT", b.cfg.Bucket, key, nil, hash, body, putExtra(hash, contentType))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", b.httpErr("put", resp)
	}
	return "s3://" + b.cfg.Bucket + "/" + key, nil
}

// URIFor returns the s3://bucket/key uri for a stored path in the configured bucket (§05.5).
func (b *Backend) URIFor(path string) string {
	return "s3://" + b.cfg.Bucket + "/" + strings.TrimPrefix(path, "/")
}

func (b *Backend) Delete(ctx context.Context, uri string) error {
	bucket, key, err := b.parse(uri)
	if err != nil {
		return err
	}
	resp, err := b.do(ctx, "DELETE", bucket, key, nil, unsignedload, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// S3 returns 204 for a delete (and for a missing key — delete is idempotent).
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return b.httpErr("delete", resp)
	}
	return nil
}

// do builds, signs (header auth), and performs an S3 request. payloadHash is the hex sha256
// of body (or unsignedload/emptyHash for bodiless requests). query carries subresources
// (?uploads, ?uploadId=, ?list-type=2). extra headers (content-type, x-amz-meta-*) are both
// signed and sent. body may be nil for HEAD/GET/DELETE.
func (b *Backend) do(ctx context.Context, method, bucket, key string, query url.Values, payloadHash string, body []byte, extra map[string]string) (*http.Response, error) {
	host, canonicalURI := b.endpoint(bucket, key)
	rawURL := b.scheme + "://" + host + canonicalURI
	if q := encodeQuery(query); q != "" {
		rawURL += "?" + q
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return nil, err
	}
	cr, err := b.creds.resolve(ctx)
	if err != nil {
		return nil, err
	}
	auth, _, signed := b.sign.signHeaders(cr, method, host, canonicalURI, query, payloadHash, extra, time.Now())
	req.Header.Set("Authorization", auth)
	for k, v := range signed {
		if k == "host" {
			continue // sent via the Host line, not a header
		}
		req.Header.Set(k, v)
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	return b.hc.Do(req)
}

// emptyHash is the sha256 of an empty payload, used for signed bodiless control requests
// (multipart initiate, list).
var emptyHash = hexSHA256(nil)

func putExtra(payloadHash, contentType string) map[string]string {
	extra := map[string]string{"x-amz-meta-sha256": payloadHash}
	if contentType != "" {
		extra["content-type"] = contentType
	}
	return extra
}

// httpErr renders an S3 error body into a coded domain error.
func (b *Backend) httpErr(op string, resp *http.Response) error {
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	_ = xml.Unmarshal(buf, &e)
	msg := fmt.Sprintf("s3 %s: %d %s %s", op, resp.StatusCode, e.Code, e.Message)
	if resp.StatusCode == http.StatusNotFound {
		return domain.NotFound(msg)
	}
	return domain.Internal(msg)
}
