// Command lineage-seed loads a demo dataset into a running registry through the public
// Model API (§03) — no direct database access, so it works against localhost, a port-forward,
// or an in-cluster install exactly like any other client.
//
//	go run ./cmd/lineage-seed              # seed a fresh registry
//	go run ./cmd/lineage-seed -reset       # delete the seeded models first, then seed
//
// Config: -endpoint (env LINEAGE_ENDPOINT, default http://localhost:8081), -actor (env
// LINEAGE_ACTOR) — the audit identity every seeded mutation is attributed to (§00 axiom 4).
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/proseria-research/lineage/internal/core"
)

func main() {
	endpoint := flag.String("endpoint", env("LINEAGE_ENDPOINT", "http://localhost:8081"), "Model API base URL")
	actor := flag.String("actor", env("LINEAGE_ACTOR", "seed@lineage.dev"), "audit identity recorded for every seeded change")
	reset := flag.Bool("reset", false, "delete the seed models (force) before loading — makes re-runs idempotent")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("□ ")
	c := &client{base: strings.TrimSuffix(*endpoint, "/"), actor: *actor, http: &http.Client{Timeout: 30 * time.Second}}
	if err := run(c, *reset); err != nil {
		log.Fatalf("seed: %v", err)
	}
}

func run(c *client, reset bool) error {
	if err := c.do("GET", "/v1/models?pageSize=1", nil, nil); err != nil {
		return fmt.Errorf("cannot reach the Model API at %s (is `make run` up?): %w", c.base, err)
	}
	log.Printf("seeding %s as %q", c.base, c.actor)

	if reset {
		for _, m := range dataset {
			if err := c.delete("/v1/models/" + m.Name + "?force=true"); err != nil {
				return err
			}
		}
		log.Printf("reset: removed %d seed models (if present)", len(dataset))
	}

	var versions, artifacts int
	for _, m := range dataset {
		if err := c.do("POST", "/v1/models", m.CreateModelInput, nil); err != nil {
			var he *httpError
			if errors.As(err, &he) && he.status == http.StatusConflict {
				return fmt.Errorf("model %q already exists — re-run with -reset to replace the seed data", m.Name)
			}
			return fmt.Errorf("create model %s: %w", m.Name, err)
		}
		for _, v := range m.Versions {
			if err := seedVersion(c, m.Name, v); err != nil {
				return fmt.Errorf("%s@%s: %w", m.Name, v.Name, err)
			}
			versions++
			artifacts += len(v.Artifacts) + len(v.Uploads)
		}
		if m.Archived {
			if err := c.do("POST", "/v1/models/"+m.Name+":archive", struct{}{}, nil); err != nil {
				return fmt.Errorf("archive model %s: %w", m.Name, err)
			}
		}
		log.Printf("  %-22s %2d version(s)", m.Name, len(m.Versions))
	}

	log.Printf("done: %d models, %d versions, %d artifacts", len(dataset), versions, artifacts)
	log.Printf("try:  curl %s/v1/models/fraud-detector/resolve?stage=production", c.base)
	log.Printf("      curl %s/v1/models/sentiment-classifier/diff?from=2.2.0-rc1\\&to=2.2.0-int8", c.base)
	log.Printf("      open the console at http://localhost:8080")
	return nil
}

// seedVersion publishes a version, uploads any real bytes, then walks it up the stage
// machine and records its lineage edges and deployments (§03.7, §03.8).
func seedVersion(c *client, model string, v version) error {
	body := core.PublishVersionInput{
		Name: v.Name, Description: v.Description, Author: v.Author,
		Labels: v.Labels, Artifacts: v.Artifacts,
	}
	if err := c.do("POST", "/v1/models/"+model+"/versions", body, nil); err != nil {
		return err
	}
	for _, u := range v.Uploads {
		if err := upload(c, model, v.Name, u); err != nil {
			return fmt.Errorf("upload %s: %w", u.Name, err)
		}
	}
	for _, e := range v.Lineage {
		if err := c.do("POST", "/v1/models/"+model+"/versions/"+v.Name+"/lineage", e, nil); err != nil {
			return fmt.Errorf("lineage %s: %w", e.Relation, err)
		}
	}
	// Stages are reached by legal moves only, so the seeded audit trail is a real history:
	// draft → staging → production, with the production singleton demoting the incumbent (§02.4).
	for _, to := range v.Path {
		t := map[string]string{"to": string(to), "reason": "seed data"}
		if err := c.do("POST", "/v1/models/"+model+"/versions/"+v.Name+":transition", t, nil); err != nil {
			return fmt.Errorf("transition →%s: %w", to, err)
		}
	}
	for _, d := range v.Deployments {
		if err := c.do("POST", "/v1/models/"+model+"/versions/"+v.Name+"/deployments", d, nil); err != nil {
			return fmt.Errorf("deployment %s: %w", d.Environment, err)
		}
	}
	// Insight facts (§11). Each write is a PATCH, so several producers merge into one
	// record instead of overwriting each other — which is what the seeded data shows.
	base := "/v1/models/" + model + "/versions/" + v.Name
	for _, in := range v.Insight {
		if err := c.do("PATCH", base+"/insight", in, nil); err != nil {
			return fmt.Errorf("insight (%s): %w", in.Reporter, err)
		}
	}
	for _, f := range v.Footprints {
		if err := c.do("PUT", base+"/footprints/"+f.Scenario, f.FootprintInput, nil); err != nil {
			return fmt.Errorf("footprint %s: %w", f.Scenario, err)
		}
	}
	for _, e := range v.Evaluations {
		if err := c.do("POST", base+"/evaluations", e, nil); err != nil {
			return fmt.Errorf("evaluation %s/%s: %w", e.Suite, e.Metric, err)
		}
	}
	return nil
}

// upload runs the full artifact upload handshake (§05.6): initiate → PUT the bytes (direct
// signed PUT on S3, stream-through on the fs backend) → finalize with the declared digest.
func upload(c *client, model, version string, u blob) error {
	data := u.bytes()
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	base := "/v1/models/" + model + "/versions/" + version + "/artifacts"

	var ticket core.UploadTicket
	in := core.InitiateUploadInput{
		Name: u.Name, Kind: u.Kind, SizeBytes: int64(len(data)),
		MediaType: u.MediaType, ModelFormat: u.ModelFormat,
	}
	if err := c.do("POST", base+":initiateUpload", in, &ticket); err != nil {
		return err
	}
	switch {
	case ticket.StreamThrough:
		if err := c.put(c.base+ticket.ContentURL, data, nil); err != nil {
			return err
		}
	case ticket.URL != "":
		if err := c.put(ticket.URL, data, ticket.Headers); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported upload mode for %s (multipart is not used for seed-sized blobs)", u.Name)
	}
	fin := map[string]string{"uploadId": ticket.UploadID, "digest": digest}
	return c.do("POST", base+":finalizeUpload", fin, nil)
}

// ---- HTTP client ----

type client struct {
	base  string
	actor string
	http  *http.Client
}

// do sends a JSON request and decodes a JSON response into out (nil discards the body).
func (c *client) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("X-Lineage-Actor", c.actor)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return apiError(method, path, resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// delete tolerates 404 so -reset works on a registry that was never seeded.
func (c *client) delete(path string) error {
	err := c.do("DELETE", path, nil, nil)
	var he *httpError
	if errors.As(err, &he) && he.status == http.StatusNotFound {
		return nil
	}
	return err
}

// put uploads raw bytes to an absolute URL (a signed storage URL or the broker sink).
func (c *client) put(url string, data []byte, headers map[string]string) error {
	req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("X-Lineage-Actor", c.actor)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return apiError("PUT", url, resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// httpError carries the status and the problem+json body (§03.9) for a readable failure.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func apiError(method, path string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var p struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &p) == nil && p.Detail != "" {
		msg = p.Detail
	}
	return &httpError{status: resp.StatusCode, msg: fmt.Sprintf("%s %s: %s: %s", method, path, resp.Status, msg)}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
