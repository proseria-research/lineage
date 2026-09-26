// Package contracttest is the M19 "external process": a client that reaches a Lineage
// registry through /v1 over plain HTTP and nothing else, and the reference report it
// assembles from what it reads.
//
// The package may import only the standard library. imports_test.go enforces that, so a
// helper that quietly reaches into the core — the thing this package exists to rule out —
// fails the build's tests instead of passing them. If collecting the report ever needs
// anything a /v1 read cannot supply, that is a gap in /v1 to close, not a reason to loosen
// the rule.
package contracttest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DefaultActorHeader is the header name a registry uses when LINEAGE_ACTOR_HEADER is unset.
const DefaultActorHeader = "X-Lineage-Actor"

// Client talks to one registry's Model API.
type Client struct {
	Base        string       // e.g. http://127.0.0.1:8081 (no trailing /v1)
	HTTP        *http.Client // nil = http.DefaultClient
	ActorHeader string       // header to send Actor under; "" = DefaultActorHeader
	Actor       string       // "" = send no actor header
}

// As returns a copy of c that sends actor as the identity.
func (c *Client) As(actor string) *Client {
	cp := *c
	cp.Actor = actor
	return &cp
}

func (c *Client) hc() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Do sends one request and decodes a JSON response body, if any. Numbers decode as
// json.Number so a value survives a read → re-encode round trip byte for byte.
func (c *Client) Do(method, path string, body any) (int, any, error) {
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			buf, err := json.Marshal(b)
			if err != nil {
				return 0, nil, err
			}
			rd = bytes.NewReader(buf)
		}
	}
	req, err := http.NewRequest(method, c.Base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Actor != "" {
		h := c.ActorHeader
		if h == "" {
			h = DefaultActorHeader
		}
		req.Header.Set(h, c.Actor)
	}
	resp, err := c.hc().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return resp.StatusCode, nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("%s %s: decode: %w (body %q)", method, path, err, raw)
	}
	return resp.StatusCode, out, nil
}

// Must is Do for writes that have to succeed: any status other than want is an error.
func (c *Client) Must(want int, method, path string, body any) (map[string]any, error) {
	code, out, err := c.Do(method, path, body)
	if err != nil {
		return nil, err
	}
	if code != want {
		return nil, fmt.Errorf("%s %s: status %d, want %d: %v", method, path, code, want, out)
	}
	m, _ := out.(map[string]any)
	return m, nil
}

// GetAll reads every page of a /v1 collection, following nextPageToken (§03.3). A
// collection that is not paginated simply has no token.
func (c *Client) GetAll(path string) (int, []any, error) {
	var items []any
	token := ""
	for {
		p := path
		if token != "" {
			sep := "?"
			if strings.Contains(p, "?") {
				sep = "&"
			}
			p += sep + "pageToken=" + url.QueryEscape(token)
		}
		code, out, err := c.Do(http.MethodGet, p, nil)
		if err != nil || code != http.StatusOK {
			return code, nil, err
		}
		page, _ := out.(map[string]any)
		got, ok := page["items"].([]any)
		if !ok && page["items"] != nil {
			return code, nil, fmt.Errorf("GET %s: items is %T, not a list", p, page["items"])
		}
		items = append(items, got...)
		token, _ = page["nextPageToken"].(string)
		if token == "" {
			return code, items, nil
		}
	}
}
