package oci

// Registry authentication: the Docker Registry v2 token flow (§05.3.1). A request goes out
// unauthenticated; a 401 carries a `WWW-Authenticate: Bearer realm=…,service=…,scope=…`
// challenge naming a token server; we exchange our credentials there for a scoped bearer
// token and retry. Tokens are cached per scope until shortly before they expire, so a push
// of many blobs costs one token exchange, not one per request.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// challenge is a parsed WWW-Authenticate header.
type challenge struct {
	scheme string // "bearer" | "basic"
	realm  string
	params map[string]string
}

// authorizer mints and caches Authorization headers per scope.
type authorizer struct {
	username string
	password string
	hc       *http.Client

	mu     sync.Mutex
	tokens map[string]cachedToken // scope → token
}

type cachedToken struct {
	value     string
	expiresAt time.Time
}

func newAuthorizer(username, password string, hc *http.Client) *authorizer {
	return &authorizer{username: username, password: password, hc: hc, tokens: map[string]cachedToken{}}
}

// basic returns the static Basic credential, or "" when running anonymously.
func (a *authorizer) basic() string {
	if a.username == "" && a.password == "" {
		return ""
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(a.username+":"+a.password))
}

// authorize returns the Authorization header value satisfying c, exchanging credentials at
// the token endpoint for a bearer challenge. An anonymous exchange is still attempted for a
// bearer challenge — public registries hand out read tokens without credentials.
//
// wantScope is the scope the *caller* needs, which is what the token is cached under. Some
// registries echo a broader or differently-spelled scope in the challenge, and caching under
// theirs would mean the next request for the same repository missed the cache and took
// another 401 — fatal on a request whose body is a stream that cannot be replayed.
func (a *authorizer) authorize(ctx context.Context, c challenge, wantScope string) (string, error) {
	switch c.scheme {
	case "basic":
		if v := a.basic(); v != "" {
			return v, nil
		}
		return "", domain.Internal("oci: registry requires credentials (set LINEAGE_OCI_USERNAME/PASSWORD)")
	case "bearer":
		return a.bearer(ctx, c, wantScope)
	default:
		return "", domain.Internal("oci: unsupported auth scheme '" + c.scheme + "'")
	}
}

func (a *authorizer) bearer(ctx context.Context, c challenge, wantScope string) (string, error) {
	scope := c.params["scope"]
	key := c.realm + "|" + c.params["service"] + "|" + wantScope

	a.mu.Lock()
	if t, ok := a.tokens[key]; ok && time.Now().Before(t.expiresAt) {
		a.mu.Unlock()
		return t.value, nil
	}
	a.mu.Unlock()

	u, err := url.Parse(c.realm)
	if err != nil {
		return "", domain.Internal("oci: bad token realm '" + c.realm + "'")
	}
	q := u.Query()
	if svc := c.params["service"]; svc != "" {
		q.Set("service", svc)
	}
	// Multiple scopes are repeated parameters, not a comma-joined value.
	for s := range strings.SplitSeq(scope, " ") {
		if s != "" {
			q.Add("scope", s)
		}
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	if v := a.basic(); v != "" {
		req.Header.Set("Authorization", v)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", domain.Internal("oci: token exchange failed: " + resp.Status + " " + string(body))
	}
	var tr struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		IssuedAt    string `json:"issued_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", domain.Internal("oci: parse token response: " + err.Error())
	}
	tok := tr.Token
	if tok == "" {
		tok = tr.AccessToken
	}
	if tok == "" {
		return "", domain.Internal("oci: token response carried no token")
	}
	// The spec makes expires_in optional and defaults it to 60s. Refresh a little early so a
	// long push never races the expiry mid-upload.
	ttl := time.Duration(tr.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	if ttl > 30*time.Second {
		ttl -= 30 * time.Second
	}
	value := "Bearer " + tok

	a.mu.Lock()
	a.tokens[key] = cachedToken{value: value, expiresAt: time.Now().Add(ttl)}
	a.mu.Unlock()
	return value, nil
}

// parseChallenge parses a WWW-Authenticate header value. Only the first challenge is read —
// registries send one.
func parseChallenge(h string) (challenge, bool) {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(h), " ")
	c := challenge{scheme: strings.ToLower(scheme), params: map[string]string{}}
	if !ok {
		return c, c.scheme == "basic" || c.scheme == "bearer"
	}
	for _, kv := range splitParams(rest) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.Trim(strings.TrimSpace(v), `"`)
		if k == "realm" {
			c.realm = v
		}
		c.params[k] = v
	}
	return c, c.scheme == "basic" || c.scheme == "bearer"
}

// splitParams splits on commas that are not inside a quoted value — scope values legitimately
// contain commas (`repository:foo:pull,push`).
func splitParams(s string) []string {
	var out []string
	var start int
	var inQuote bool
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}
