// Command lineage-init is the KServe storage-initializer for lineage:// URIs (§04.5). It
// runs as the init container of an InferenceService: given a lineage:// reference and a
// destination directory, it resolves the model against the Model API and downloads the
// MODEL artifact (via its signed URL, else the broker /content endpoint) into the dir.
//
//	lineage-init lineage://fraud-detector/production /mnt/models
//
// Config (env): LINEAGE_ENDPOINT (Model API base, default http://lineage-model-api:8081),
// LINEAGE_ACTOR (optional audit identity header value).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: lineage-init <lineage://model[/stage][@version][#artifact]> <dest-dir>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "lineage-init: "+err.Error())
		os.Exit(1)
	}
}

// resolution mirrors the subset of the /resolve response the initializer needs (§04.2).
type resolution struct {
	Version   string `json:"version"`
	Artifacts []struct {
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		StorageURI string `json:"storageUri"`
		SignedURL  string `json:"signedUrl"`
		SizeBytes  int64  `json:"sizeBytes"`
	} `json:"artifacts"`
}

func run(rawURI, dest string) error {
	ref, err := domain.ParseLineageURI(rawURI)
	if err != nil {
		return err
	}
	endpoint := env("LINEAGE_ENDPOINT", "http://lineage-model-api:8081")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	res, err := resolve(ctx, endpoint, ref)
	if err != nil {
		return err
	}

	// Pick the requested artifact, or the MODEL artifact by default.
	idx := -1
	for i, a := range res.Artifacts {
		if ref.Artifact != "" && a.Name == ref.Artifact {
			idx = i
			break
		}
		if ref.Artifact == "" && a.Kind == string(domain.KindModel) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("no matching artifact for %q in resolved version %s", rawURI, res.Version)
	}
	a := res.Artifacts[idx]

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	out := filepath.Join(dest, a.Name)

	// Prefer the signed URL (bytes flow straight from storage); otherwise fall back to the
	// broker /content endpoint, which streams through for backends that can't sign (§04.3).
	src := a.SignedURL
	if src == "" {
		src = endpoint + "/v1/models/" + url.PathEscape(ref.Model) +
			"/versions/" + url.PathEscape(res.Version) +
			"/artifacts/" + url.PathEscape(a.Name) + "/content"
	}
	n, err := download(ctx, src, out)
	if err != nil {
		return err
	}
	fmt.Printf("lineage-init: wrote %s (%d bytes) from %s@%s\n", out, n, ref.Model, res.Version)
	return nil
}

func resolve(ctx context.Context, endpoint string, ref domain.LineageRef) (*resolution, error) {
	q := url.Values{}
	switch sel := ref.Selector(); {
	case sel.Version != "":
		q.Set("version", sel.Version)
	case sel.Stage != "":
		q.Set("stage", string(sel.Stage))
	}
	u := endpoint + "/v1/models/" + url.PathEscape(ref.Model) + "/resolve"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	if actor := os.Getenv("LINEAGE_ACTOR"); actor != "" {
		req.Header.Set("X-Lineage-Actor", actor)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("resolve %s: %s: %s", u, resp.Status, body)
	}
	var res resolution
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode resolve response: %w", err)
	}
	return &res, nil
}

func download(ctx context.Context, src, dst string) (int64, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", src, nil)
	if actor := os.Getenv("LINEAGE_ACTOR"); actor != "" {
		req.Header.Set("X-Lineage-Actor", actor)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("download %s: %s: %s", src, resp.Status, body)
	}
	f, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, resp.Body)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
