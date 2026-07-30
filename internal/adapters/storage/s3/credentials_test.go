package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestStaticResolver(t *testing.T) {
	r := buildResolver(Config{AccessKey: "AKIA", SecretKey: "secret", SessionToken: "tok"}, http.DefaultClient)
	cr, err := r.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cr.accessKey != "AKIA" || cr.secretKey != "secret" || cr.sessionToken != "tok" {
		t.Fatalf("static creds not returned: %+v", cr)
	}
}

// Web-identity (IRSA) exchanges a token file for temporary credentials via STS, parses the
// XML, records the expiry, and caches until near expiry.
func TestWebIdentityResolver(t *testing.T) {
	var hits int32
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if err := r.ParseForm(); err != nil || r.Form.Get("Action") != "AssumeRoleWithWebIdentity" {
			t.Errorf("unexpected STS request: %v %s", err, r.Form.Encode())
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<AssumeRoleWithWebIdentityResponse><AssumeRoleWithWebIdentityResult><Credentials>` +
			`<AccessKeyId>ASIATEMP</AccessKeyId><SecretAccessKey>tempsecret</SecretAccessKey>` +
			`<SessionToken>tempsession</SessionToken><Expiration>2999-01-01T00:00:00Z</Expiration>` +
			`</Credentials></AssumeRoleWithWebIdentityResult></AssumeRoleWithWebIdentityResponse>`))
	}))
	defer sts.Close()

	tokFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokFile, []byte("web-identity-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &webIdentityProvider{
		roleARN: "arn:aws:iam::123:role/lineage", tokenFile: tokFile,
		sessionName: "lineage", stsEndpoint: sts.URL, hc: sts.Client(),
	}
	r := newResolver(p)

	cr, err := r.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cr.accessKey != "ASIATEMP" || cr.sessionToken != "tempsession" {
		t.Fatalf("temporary creds not parsed: %+v", cr)
	}
	// Second resolve should be served from cache (expiry far in the future) — no new STS hit.
	if _, err := r.resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected 1 STS call (cached thereafter), got %d", got)
	}
}

// A resolver whose cached credentials are already expired must refresh on the next resolve.
func TestResolverRefreshesOnExpiry(t *testing.T) {
	var hits int32
	inner := providerFunc(func(context.Context) (creds, time.Time, error) {
		atomic.AddInt32(&hits, 1)
		return creds{accessKey: "AKIA", secretKey: "s"}, time.Now().Add(1 * time.Minute), nil // inside the 5m refresh window
	})
	r := newResolver(inner)
	if _, err := r.resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("near-expiry creds should refresh each call, got %d hits", got)
	}
}

type providerFunc func(context.Context) (creds, time.Time, error)

func (f providerFunc) retrieve(ctx context.Context) (creds, time.Time, error) { return f(ctx) }
