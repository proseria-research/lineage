package contracttest_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/proseria-research/lineage/internal/config"
	"github.com/proseria-research/lineage/internal/contracttest"
)

// M19 actor-header contract (§03.1): a non-default LINEAGE_ACTOR_HEADER is honoured on both
// surfaces, its value lands in audit_event verbatim, the default header is then ignored, and
// nothing is authorized from it.
func TestCustomActorHeader(t *testing.T) {
	const custom = "X-Forwarded-User"
	// A value a gateway might really send: spaces, commas, an email in angle brackets. It must
	// come back byte for byte — not normalised, lower-cased, or parsed.
	const who = "CN=Alice Smith,O=Acme Corp <alice@acme.example>"

	t.Setenv("LINEAGE_ACTOR_HEADER", custom)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActorHeader != custom {
		t.Fatalf("config ActorHeader = %q", cfg.ActorHeader)
	}
	reg := newRegistry(t, "sqlite", registryOpts{actorHeader: cfg.ActorHeader})
	anon := &contracttest.Client{Base: reg.ModelAPI.URL}
	alice := &contracttest.Client{Base: reg.ModelAPI.URL, ActorHeader: custom, Actor: who}
	mallory := &contracttest.Client{Base: reg.ModelAPI.URL, Actor: "mallory"} // default header name

	mustOK := func(cl *contracttest.Client, want int, method, path, body string) {
		t.Helper()
		if _, err := cl.Must(want, method, path, body); err != nil {
			t.Fatal(err)
		}
	}
	// Each write below is to a model of its own, so its audit row is findable by subject.
	mustOK(alice, http.StatusCreated, "POST", "/v1/models", `{"name":"by-custom"}`)
	// The default header is not an alias: with a custom name configured it is just another
	// header, and a request carrying only it is unattributed, not attributed to "mallory".
	mustOK(mallory, http.StatusCreated, "POST", "/v1/models", `{"name":"by-default-header"}`)
	// No header at all: the write still succeeds. Attribution is recorded, never required.
	mustOK(anon, http.StatusCreated, "POST", "/v1/models", `{"name":"by-nobody"}`)
	// Both headers: the configured one wins.
	both := *alice
	both.HTTP = &http.Client{Transport: addHeader{"X-Lineage-Actor", "mallory"}}
	mustOK(&both, http.StatusCreated, "POST", "/v1/models", `{"name":"by-both"}`)
	// Reads need nothing either.
	mustOK(anon, http.StatusOK, "GET", "/v1/models/by-custom", "")

	for model, want := range map[string]string{
		"by-custom": who, "by-default-header": "", "by-nobody": "", "by-both": who,
	} {
		if got := createActor(t, anon, model); got != want {
			t.Errorf("%s: model.create actor = %q, want %q", model, got, want)
		}
	}

	// The Admin UI BFF reads the same configured header. Its own fallback for an unattributed
	// console action is "console" (§06.1), and the default header does not bypass that.
	mustOK(alice, http.StatusCreated, "POST", "/v1/models/by-custom/versions", `{"name":"1.0.0"}`)
	for _, tc := range []struct{ header, value, to, want string }{
		{custom, who, "staging", who},
		{"X-Lineage-Actor", "mallory", "production", "console"},
	} {
		req, _ := http.NewRequest("POST", reg.Admin.URL+"/api/models/by-custom/versions/1.0.0/transition",
			strings.NewReader(`{"to":"`+tc.to+`"}`))
		req.Header.Set(tc.header, tc.value)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("console transition to %s: %d", tc.to, resp.StatusCode)
		}
		if got := stageActor(t, anon, tc.to); got != tc.want {
			t.Errorf("console → %s: actor = %q, want %q", tc.to, got, tc.want)
		}
	}
}

// addHeader is a RoundTripper that adds one header to every request.
type addHeader struct{ k, v string }

func (a addHeader) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(a.k, a.v)
	return http.DefaultTransport.RoundTrip(r)
}

// createActor reads model.create's actor for model through /v1.
func createActor(t *testing.T, c *contracttest.Client, model string) string {
	t.Helper()
	_, items, err := c.GetAll("/v1/models/" + model + "/audit")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if e := it.(map[string]any); e["action"] == "model.create" {
			a, _ := e["actor"].(string)
			return a
		}
	}
	t.Fatalf("%s: no model.create event in %v", model, items)
	return ""
}

// stageActor reads the actor of the stage change into `to` through /v1.
func stageActor(t *testing.T, c *contracttest.Client, to string) string {
	t.Helper()
	_, items, err := c.GetAll("/v1/audit?pageSize=500")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		e := it.(map[string]any)
		if d, _ := e["data"].(map[string]any); e["action"] == "version.stage_changed" && d["to"] == to {
			a, _ := e["actor"].(string)
			return a
		}
	}
	t.Fatalf("no stage change to %s", to)
	return ""
}
