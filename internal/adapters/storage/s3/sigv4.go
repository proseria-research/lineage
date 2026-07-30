package s3

// Minimal AWS Signature Version 4 for S3, stdlib-only. Enough for presigned GET/PUT
// (query auth) and header-signed HEAD/GET/PUT/DELETE against any S3-compatible endpoint
// (AWS, MinIO, R2, Ceph). Validated against AWS's published presign test vector
// (sigv4_test.go). Not a general SDK — just what the storage backend needs (§05.3).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	algorithm    = "AWS4-HMAC-SHA256"
	service      = "s3"
	unsignedload = "UNSIGNED-PAYLOAD"
	terminator   = "aws4_request"
	timeFmt      = "20060102T150405Z"
	dateFmt      = "20060102"
)

// creds is a resolved credential set. sessionToken is non-empty for temporary credentials
// (IRSA/STS, ECS task roles, EC2 IMDS), and must be threaded into every signed request.
type creds struct {
	accessKey    string
	secretKey    string
	sessionToken string // optional (temporary credentials)
}

// signer is stateless apart from the region; credentials are passed per request so a
// backend can rotate temporary credentials without rebuilding the signer.
type signer struct {
	region string
}

// presign returns a presigned URL. host is the request host, canonicalURI the already
// path-encoded object path (leading '/'); query holds any pre-existing params.
func (s signer) presign(cr creds, method, scheme, host, canonicalURI string, query url.Values, expires time.Duration, now time.Time) string {
	amzDate := now.UTC().Format(timeFmt)
	scope := s.scope(now)

	q := cloneValues(query)
	q.Set("X-Amz-Algorithm", algorithm)
	q.Set("X-Amz-Credential", cr.accessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(int(expires.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	if cr.sessionToken != "" {
		q.Set("X-Amz-Security-Token", cr.sessionToken)
	}

	canonicalQuery := encodeQuery(q)
	canonicalHeaders := "host:" + host + "\n"
	canonicalRequest := strings.Join([]string{
		method, canonicalURI, canonicalQuery, canonicalHeaders, "host", unsignedload,
	}, "\n")

	sig := s.sign(cr, canonicalRequest, amzDate, scope, now)
	return scheme + "://" + host + canonicalURI + "?" + canonicalQuery + "&X-Amz-Signature=" + sig
}

// signHeaders computes the Authorization header for a header-signed request. `extra` holds
// any additional headers to sign and send (e.g. content-type, x-amz-meta-*); AWS requires
// every x-amz-* header on the wire to be part of the signature. It returns the auth value,
// the x-amz-date, and the full set of signed headers the caller must place on the request.
func (s signer) signHeaders(cr creds, method, host, canonicalURI string, query url.Values, payloadHash string, extra map[string]string, now time.Time) (auth, amzDate string, signed map[string]string) {
	amzDate = now.UTC().Format(timeFmt)
	scope := s.scope(now)

	headers := map[string]string{
		"host":                 host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	if cr.sessionToken != "" {
		headers["x-amz-security-token"] = cr.sessionToken
	}
	for k, v := range extra {
		if v != "" {
			headers[strings.ToLower(k)] = v
		}
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, k := range names {
		canonicalHeaders.WriteString(k + ":" + strings.TrimSpace(headers[k]) + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	canonicalRequest := strings.Join([]string{
		method, canonicalURI, encodeQuery(query), canonicalHeaders.String(), signedHeaders, payloadHash,
	}, "\n")
	sig := s.sign(cr, canonicalRequest, amzDate, scope, now)
	auth = algorithm + " Credential=" + cr.accessKey + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + sig
	return auth, amzDate, headers
}

func (s signer) scope(now time.Time) string {
	return now.UTC().Format(dateFmt) + "/" + s.region + "/" + service + "/" + terminator
}

func (s signer) sign(cr creds, canonicalRequest, amzDate, scope string, now time.Time) string {
	stringToSign := strings.Join([]string{
		algorithm, amzDate, scope, hexSHA256([]byte(canonicalRequest)),
	}, "\n")
	key := signingKey(cr.secretKey, now.UTC().Format(dateFmt), s.region)
	return hex.EncodeToString(hmacSHA256(key, stringToSign))
}

func signingKey(secret, datestamp, region string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), datestamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, terminator)
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// encodeQuery renders a canonical (sorted, RFC3986-encoded) query string.
func encodeQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(uriEncode(k, true))
			b.WriteByte('=')
			b.WriteString(uriEncode(v, true))
		}
	}
	return b.String()
}

// uriEncode is RFC3986 percent-encoding as required by SigV4 (unreserved chars are
// A-Z a-z 0-9 - _ . ~). encodeSlash=false preserves '/' in object paths (S3 encodes once).
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte('/')
		default:
			b.WriteByte('%')
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

func cloneValues(q url.Values) url.Values {
	out := make(url.Values, len(q)+6)
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}
