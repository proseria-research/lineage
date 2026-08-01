// Command lineage-cli is the Go command-line client for the Lineage Model API.
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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type client struct{ base, actor string }

func main() {
	c := client{base: strings.TrimRight(env("LINEAGE_SERVER", "http://localhost:8081"), "/"), actor: os.Getenv("LINEAGE_ACTOR")}
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
	}
	// Two-word commands are matched on their first two args; the rest are single-word verbs
	// whose operand follows, so they must be matched on args[0] alone.
	switch args[0] {
	case "model", "version":
		if len(args) < 2 {
			usage()
		}
		switch args[0] + " " + args[1] {
		case "model list":
			out(c.do("GET", "/v1/models", nil, nil))
		case "version promote":
			promote(c, args[2:])
		case "version publish":
			publish(c, args[2:])
		default:
			usage()
		}
	case "resolve":
		resolve(c, args[1:])
	case "pull":
		pull(c, args[1:])
	case "lineage":
		lineage(c, args[1:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: lineage-cli model list | version publish -m MODEL -n VERSION -a FILE | version promote -m MODEL -n VERSION --to STAGE | resolve MODEL [--stage STAGE] | pull MODEL --dest DIR [--kind KIND] [--artifact NAME] | lineage MODEL@VERSION")
	os.Exit(2)
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func out(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }

// operand parses a command that takes exactly one positional argument. Go's flag package
// stops at the first non-flag arg, so the operand is pulled out and any flags that followed
// it are parsed in a second pass — `resolve MODEL --stage x` and `resolve --stage x MODEL`
// both work.
func operand(f *flag.FlagSet, args []string) string {
	_ = f.Parse(args)
	if f.NArg() == 0 {
		usage()
	}
	name, rest := f.Arg(0), f.Args()[1:]
	if len(rest) > 0 {
		_ = f.Parse(rest)
		if f.NArg() != 0 {
			usage()
		}
	}
	return name
}

func promote(c client, args []string) {
	f := flag.NewFlagSet("promote", flag.ExitOnError)
	m, n, to := f.String("m", "", "model"), f.String("n", "", "version"), f.String("to", "", "stage")
	_ = f.Parse(args)
	require(*m, *n, *to)
	out(c.do("POST", vp(*m, *n)+":transition", map[string]any{"to": *to}, nil))
}
func resolve(c client, args []string) {
	f := flag.NewFlagSet("resolve", flag.ExitOnError)
	stage, version := f.String("stage", "", "stage"), f.String("version", "", "version")
	model := operand(f, args)
	q := url.Values{}
	if *stage != "" {
		q.Set("stage", *stage)
	}
	if *version != "" {
		q.Set("version", *version)
	}
	out(c.do("GET", "/v1/models/"+url.PathEscape(model)+"/resolve?"+q.Encode(), nil, nil))
}
func lineage(c client, args []string) {
	f := flag.NewFlagSet("lineage", flag.ExitOnError)
	direction := f.String("direction", "upstream", "direction")
	ref := operand(f, args)
	if !strings.Contains(ref, "@") {
		usage()
	}
	model, version, _ := strings.Cut(ref, "@")
	out(c.do("GET", vp(model, version)+"/lineage?direction="+url.QueryEscape(*direction), nil, nil))
}

func publish(c client, args []string) {
	f := flag.NewFlagSet("publish", flag.ExitOnError)
	model, version, artifact, format := f.String("m", "", "model"), f.String("n", "", "version"), f.String("a", "", "artifact path"), f.String("format", "", "name:version")
	_ = f.Parse(args)
	require(*model, *version, *artifact)
	// Idempotently ensure the model exists, then create the version before uploading bytes.
	// The server's idempotency store is a single global keyspace, so the keys are namespaced
	// by resource — a bare version name would collide across models and replay the wrong
	// response, leaving the version uncreated.
	_ = c.do("POST", "/v1/models", map[string]any{"name": *model}, map[string]string{"Idempotency-Key": "model:" + *model})
	c.do("POST", "/v1/models/"+url.PathEscape(*model)+"/versions", map[string]any{"name": *version},
		map[string]string{"Idempotency-Key": "version:" + *model + "@" + *version})
	info, err := os.Stat(*artifact)
	if err != nil {
		fatal(err)
	}
	body := map[string]any{"name": filepath.Base(*artifact), "kind": "MODEL", "sizeBytes": info.Size()}
	if *format != "" {
		n, v, _ := strings.Cut(*format, ":")
		body["modelFormat"] = map[string]any{"name": n, "version": v}
	}
	ticket := c.do("POST", vp(*model, *version)+"/artifacts:initiateUpload", body, nil)
	final := map[string]any{"uploadId": ticket["uploadId"], "digest": digest(*artifact)}
	if b, _ := ticket["multipart"].(bool); b {
		final["parts"] = uploadParts(*artifact, ticket)
	} else {
		target := asString(ticket["contentUrl"], asString(ticket["url"], ""))
		if target == "" {
			fatal(errors.New("upload ticket had no upload URL"))
		}
		// Stream-through tickets carry a server-relative contentUrl; signed tickets are absolute.
		if strings.HasPrefix(target, "/") {
			target = c.base + target
		}
		putFile(target, *artifact, asString(ticket["method"], "PUT"), ticket["headers"])
	}
	out(c.do("POST", vp(*model, *version)+"/artifacts:finalizeUpload", final, nil))
}

// uploadParts PUTs each presigned part in order and returns the per-part ETags the finalize
// call needs to assemble the object (§05.6). Parts are read one at a time, so peak memory is
// one part rather than the whole artifact.
func uploadParts(path string, ticket map[string]any) []map[string]any {
	partSize, _ := ticket["partSize"].(float64)
	planned, _ := ticket["parts"].([]any)
	if partSize <= 0 || len(planned) == 0 {
		fatal(errors.New("multipart ticket had no part plan"))
	}
	f, err := os.Open(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	buf := make([]byte, int(partSize))
	done := make([]map[string]any, 0, len(planned))
	for _, p := range planned {
		part, _ := p.(map[string]any)
		n, err := io.ReadFull(f, buf)
		if err == io.ErrUnexpectedEOF || err == io.EOF {
			err = nil // the final part is short
		}
		if err != nil {
			fatal(err)
		}
		if n == 0 {
			break
		}
		etag := putBytes(asString(part["url"], ""), buf[:n])
		if etag == "" {
			fatal(fmt.Errorf("multipart part %v returned no ETag", part["partNumber"]))
		}
		done = append(done, map[string]any{"partNumber": part["partNumber"], "etag": etag})
	}
	if len(done) != len(planned) {
		fatal(errors.New("multipart upload did not consume every planned part"))
	}
	return done
}

func pull(c client, args []string) {
	f := flag.NewFlagSet("pull", flag.ExitOnError)
	stage, dest := f.String("stage", "production", "stage"), f.String("dest", "", "destination")
	kind, only := f.String("kind", "MODEL", "artifact kind to pull (empty for all)"), f.String("artifact", "", "pull one artifact by name")
	model := operand(f, args)
	if *dest == "" {
		usage()
	}
	r := c.do("GET", "/v1/models/"+url.PathEscape(model)+"/resolve?stage="+url.QueryEscape(*stage), nil, nil)
	arts, _ := r["artifacts"].([]any)
	// Mirrors the lineage:// initializer (§04.5): every MODEL artifact, since sharded weights,
	// config and tokenizer are separate artifacts of one version and pulling only the first
	// leaves an unusable model dir. DOC artifacts stay out unless asked for by name or kind.
	var selected []map[string]any
	for _, item := range arts {
		a, _ := item.(map[string]any)
		switch {
		case *only != "":
			if asString(a["name"], "") == *only {
				selected = append(selected, a)
			}
		case *kind == "" || asString(a["kind"], "") == *kind:
			selected = append(selected, a)
		}
	}
	if len(selected) == 0 {
		fatal(fmt.Errorf("resolution has no matching artifact for %s@%s", model, *stage))
	}
	if err := os.MkdirAll(*dest, 0755); err != nil {
		fatal(err)
	}
	for _, a := range selected {
		name := asString(a["name"], "")
		if !safeName(name) {
			fatal(fmt.Errorf("refusing unsafe artifact name %q", name))
		}
		out := filepath.Join(*dest, name)
		getFile(asString(a["signedUrl"], c.base+vp(model, r["version"].(string))+"/artifacts/"+url.PathEscape(name)+"/content"), out)
		fmt.Println(out)
	}
}
func require(v ...string) {
	for _, s := range v {
		if s == "" {
			usage()
		}
	}
}
func vp(m, v string) string {
	return "/v1/models/" + url.PathEscape(m) + "/versions/" + url.PathEscape(v)
}

// safeName guards a server-supplied artifact name before it is joined into a local path, so
// a bad or compromised registry cannot write outside the destination directory.
func safeName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`) && !filepath.IsAbs(name)
}

func asString(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "lineage-cli:", err); os.Exit(1) }

func (c client) do(method, path string, body any, extra map[string]string) map[string]any {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.actor != "" {
		req.Header.Set("X-Lineage-Actor", c.actor)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode >= 300 && !(resp.StatusCode == 409 && path == "/v1/models") {
		fatal(fmt.Errorf("%s", out["detail"]))
	}
	return out
}
func putFile(raw, path, method string, hdr any) {
	f, err := os.Open(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	req, err := http.NewRequest(method, raw, f)
	if err != nil {
		fatal(err)
	}
	if hs, ok := hdr.(map[string]any); ok {
		for k, v := range hs {
			req.Header.Set(k, fmt.Sprint(v))
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		fatal(fmt.Errorf("upload failed: %s", resp.Status))
	}
}

// putBytes uploads one multipart part and returns the storage-assigned ETag.
func putBytes(raw string, data []byte) string {
	if raw == "" {
		fatal(errors.New("multipart part had no presigned URL"))
	}
	req, err := http.NewRequest("PUT", raw, bytes.NewReader(data))
	if err != nil {
		fatal(err)
	}
	req.ContentLength = int64(len(data))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		fatal(fmt.Errorf("part upload failed: %s", resp.Status))
	}
	return strings.Trim(resp.Header.Get("ETag"), `"`)
}

func getFile(raw, path string) {
	resp, err := http.Get(raw)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		fatal(fmt.Errorf("download failed: %s", resp.Status))
	}
	f, err := os.Create(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	if err != nil {
		fatal(err)
	}
}
func digest(path string) string {
	f, err := os.Open(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		fatal(err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
