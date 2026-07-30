package s3

// Credential resolution for the S3 backend (§05.4). Least-privilege credentials are
// resolved from the environment — never carried in Lineage API responses — and temporary
// credentials (IRSA/STS, EKS Pod Identity, ECS task roles, EC2 IMDS) are refreshed before
// they expire. Providers are stdlib-only so the binary stays dependency-light.
//
// Resolution order for the default "auto" chain, first non-empty wins:
//  1. static      — explicit access/secret keys from config
//  2. env         — AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY [/ AWS_SESSION_TOKEN]
//  3. web identity — IRSA / EKS Pod Identity (AWS_ROLE_ARN + AWS_WEB_IDENTITY_TOKEN_FILE)
//  4. ECS          — AWS_CONTAINER_CREDENTIALS_{RELATIVE,FULL}_URI
//  5. IMDS         — EC2 instance role (IMDSv2)

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// credProvider resolves a credential set plus its expiry (zero = non-expiring, e.g. static
// keys). Temporary-credential providers return a real expiry so the cache can refresh.
type credProvider interface {
	retrieve(ctx context.Context) (creds, time.Time, error)
}

// resolver is what the backend holds: a cached view over the provider chain.
type resolver struct {
	inner     credProvider
	mu        sync.Mutex
	cur       creds
	expiresAt time.Time
}

func newResolver(inner credProvider) *resolver { return &resolver{inner: inner} }

// resolve returns cached credentials, refreshing 5 minutes before expiry so signing never
// races an expiring token.
func (r *resolver) resolve(ctx context.Context) (creds, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.cur.accessKey != "" && (r.expiresAt.IsZero() || now.Add(5*time.Minute).Before(r.expiresAt)) {
		return r.cur, nil
	}
	cr, exp, err := r.inner.retrieve(ctx)
	if err != nil {
		return creds{}, err
	}
	if cr.accessKey == "" {
		return creds{}, domain.Internal("s3: no credentials found (static/env/irsa/ecs/imds all empty)")
	}
	r.cur, r.expiresAt = cr, exp
	return cr, nil
}

// chain tries each provider in order, returning the first that yields an access key.
type chain struct{ providers []credProvider }

func (c chain) retrieve(ctx context.Context) (creds, time.Time, error) {
	var last error
	for _, p := range c.providers {
		cr, exp, err := p.retrieve(ctx)
		if err != nil {
			last = err
			continue
		}
		if cr.accessKey != "" {
			return cr, exp, nil
		}
	}
	return creds{}, time.Time{}, last
}

// --- static / env ---

type staticProvider struct{ c creds }

func (p staticProvider) retrieve(context.Context) (creds, time.Time, error) {
	return p.c, time.Time{}, nil
}

type envProvider struct{}

func (envProvider) retrieve(context.Context) (creds, time.Time, error) {
	return creds{
		accessKey:    os.Getenv("AWS_ACCESS_KEY_ID"),
		secretKey:    os.Getenv("AWS_SECRET_ACCESS_KEY"),
		sessionToken: os.Getenv("AWS_SESSION_TOKEN"),
	}, time.Time{}, nil
}

// --- web identity (IRSA / EKS Pod Identity) ---

// webIdentityProvider exchanges a projected service-account token for temporary credentials
// via STS AssumeRoleWithWebIdentity. This is how EKS IRSA and Pod Identity work: the pod
// gets AWS_ROLE_ARN and a token-file path injected into its environment.
type webIdentityProvider struct {
	roleARN     string
	tokenFile   string
	sessionName string
	stsEndpoint string
	hc          *http.Client
}

func newWebIdentityProvider(region string, hc *http.Client) *webIdentityProvider {
	role, tok := os.Getenv("AWS_ROLE_ARN"), os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")
	if role == "" || tok == "" {
		return nil // not running under IRSA/Pod Identity
	}
	sess := os.Getenv("AWS_ROLE_SESSION_NAME")
	if sess == "" {
		sess = "lineage"
	}
	ep := "https://sts.amazonaws.com"
	if region != "" {
		ep = "https://sts." + region + ".amazonaws.com"
	}
	return &webIdentityProvider{roleARN: role, tokenFile: tok, sessionName: sess, stsEndpoint: ep, hc: hc}
}

func (p *webIdentityProvider) retrieve(ctx context.Context) (creds, time.Time, error) {
	token, err := os.ReadFile(p.tokenFile)
	if err != nil {
		return creds{}, time.Time{}, domain.Internal("s3 irsa: read token file: " + err.Error())
	}
	form := url.Values{
		"Action":           {"AssumeRoleWithWebIdentity"},
		"Version":          {"2011-06-15"},
		"RoleArn":          {p.roleARN},
		"RoleSessionName":  {p.sessionName},
		"WebIdentityToken": {strings.TrimSpace(string(token))},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", p.stsEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return creds{}, time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/xml")
	resp, err := p.hc.Do(req)
	if err != nil {
		return creds{}, time.Time{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return creds{}, time.Time{}, domain.Internal("s3 irsa: sts " + resp.Status + ": " + string(body))
	}
	var out struct {
		Result struct {
			Credentials stsCreds `xml:"Credentials"`
		} `xml:"AssumeRoleWithWebIdentityResult"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return creds{}, time.Time{}, domain.Internal("s3 irsa: parse sts response: " + err.Error())
	}
	c := out.Result.Credentials
	return creds{accessKey: c.AccessKeyID, secretKey: c.SecretAccessKey, sessionToken: c.SessionToken}, c.Expiration, nil
}

type stsCreds struct {
	AccessKeyID     string    `xml:"AccessKeyId"`
	SecretAccessKey string    `xml:"SecretAccessKey"`
	SessionToken    string    `xml:"SessionToken"`
	Expiration      time.Time `xml:"Expiration"`
}

// --- ECS task role ---

type ecsProvider struct {
	url   string
	token string
	hc    *http.Client
}

func newECSProvider(hc *http.Client) *ecsProvider {
	if rel := os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"); rel != "" {
		return &ecsProvider{url: "http://169.254.170.2" + rel, hc: hc}
	}
	if full := os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI"); full != "" {
		return &ecsProvider{url: full, token: os.Getenv("AWS_CONTAINER_AUTHORIZATION_TOKEN"), hc: hc}
	}
	return nil
}

func (p *ecsProvider) retrieve(ctx context.Context) (creds, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.url, nil)
	if err != nil {
		return creds{}, time.Time{}, err
	}
	if p.token != "" {
		req.Header.Set("Authorization", p.token)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return creds{}, time.Time{}, err
	}
	defer resp.Body.Close()
	return decodeJSONCreds(resp)
}

// --- EC2 IMDSv2 ---

type imdsProvider struct{ hc *http.Client }

func (p imdsProvider) retrieve(ctx context.Context) (creds, time.Time, error) {
	const base = "http://169.254.169.254"
	treq, _ := http.NewRequestWithContext(ctx, "PUT", base+"/latest/api/token", nil)
	treq.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
	tresp, err := p.hc.Do(treq)
	if err != nil {
		return creds{}, time.Time{}, err
	}
	tok, _ := io.ReadAll(io.LimitReader(tresp.Body, 4096))
	tresp.Body.Close()

	get := func(path string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", base+path, nil)
		if len(tok) > 0 {
			req.Header.Set("X-aws-ec2-metadata-token", string(tok))
		}
		return p.hc.Do(req)
	}
	rresp, err := get("/latest/meta-data/iam/security-credentials/")
	if err != nil {
		return creds{}, time.Time{}, err
	}
	role, _ := io.ReadAll(io.LimitReader(rresp.Body, 4096))
	rresp.Body.Close()
	if len(role) == 0 {
		return creds{}, time.Time{}, nil // no instance role attached
	}
	cresp, err := get("/latest/meta-data/iam/security-credentials/" + strings.TrimSpace(string(role)))
	if err != nil {
		return creds{}, time.Time{}, err
	}
	defer cresp.Body.Close()
	return decodeJSONCreds(cresp)
}

// decodeJSONCreds parses the AWS JSON credential document returned by ECS/IMDS.
func decodeJSONCreds(resp *http.Response) (creds, time.Time, error) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return creds{}, time.Time{}, domain.Internal("s3 creds: " + resp.Status + ": " + string(body))
	}
	var d struct {
		AccessKeyID     string    `json:"AccessKeyId"`
		SecretAccessKey string    `json:"SecretAccessKey"`
		Token           string    `json:"Token"`
		Expiration      time.Time `json:"Expiration"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return creds{}, time.Time{}, domain.Internal("s3 creds: parse: " + err.Error())
	}
	return creds{accessKey: d.AccessKeyID, secretKey: d.SecretAccessKey, sessionToken: d.Token}, d.Expiration, nil
}

// buildResolver assembles the credential resolver for cfg (§05.4). Explicit static keys
// short-circuit the chain; otherwise env → IRSA → ECS → IMDS are tried in order.
func buildResolver(cfg Config, hc *http.Client) *resolver {
	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		return newResolver(staticProvider{creds{cfg.AccessKey, cfg.SecretKey, cfg.SessionToken}})
	}
	ps := []credProvider{envProvider{}}
	if wi := newWebIdentityProvider(cfg.Region, hc); wi != nil {
		ps = append(ps, wi)
	}
	if ecs := newECSProvider(hc); ecs != nil {
		ps = append(ps, ecs)
	}
	ps = append(ps, imdsProvider{hc: hc})
	return newResolver(chain{providers: ps})
}
