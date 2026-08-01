package modelapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/proseria-research/lineage/internal/core"
)

// The published schema is the only contract a producer has. These tests keep it honest:
// it must not drift from the fields the code accepts, and the shipped fixtures must
// actually be accepted by a running registry.

// If the schema and the implementation disagree, every producer built against the schema
// is wrong in a way nothing else would catch.
func TestPublishedSchemaMatchesAcceptedFields(t *testing.T) {
	raw, err := os.ReadFile("insight_schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		XSchemaVersion string `json:"x-schemaVersion"`
		Properties     struct {
			Facts struct {
				Properties map[string]any `json:"properties"`
			} `json:"facts"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if doc.XSchemaVersion != core.InsightSchemaVersion {
		t.Fatalf("schema declares version %q but the registry speaks %q",
			doc.XSchemaVersion, core.InsightSchemaVersion)
	}

	published := make([]string, 0, len(doc.Properties.Facts.Properties))
	for k := range doc.Properties.Facts.Properties {
		published = append(published, k)
	}
	sort.Strings(published)

	accepted := core.InsightFactFields()
	if len(published) != len(accepted) {
		t.Fatalf("schema publishes %v but the registry accepts %v", published, accepted)
	}
	for i := range accepted {
		if published[i] != accepted[i] {
			t.Fatalf("schema/implementation drift at %d: schema=%q code=%q\nschema=%v\ncode=%v",
				i, published[i], accepted[i], published, accepted)
		}
	}
}

func TestInsightSchemaServed(t *testing.T) {
	srv := apiServer(t)
	code, body := do(t, srv, "GET", "/v1/insight-schema.json", "", nil)
	if code != http.StatusOK {
		t.Fatalf("schema endpoint = %d", code)
	}
	if body["$id"] == nil || body["x-schemaVersion"] != core.InsightSchemaVersion {
		t.Fatalf("served schema looks wrong: %v", body)
	}
}

// Every shipped fixture must be accepted as-is. A producer author copies these, so a
// fixture the server rejects is worse than no fixture.
func TestGoldenProducerFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/producers/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no producer fixtures found: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			srv := seedVersions(t)
			code, body := do(t, srv, "PATCH", patchPath, string(raw), nil)
			if code != http.StatusOK {
				t.Fatalf("fixture rejected with %d: %v", code, body)
			}
		})
	}
}

// A header scanner then the SDK at publish is the multi-producer story the design exists
// for: each adds what it knows, neither erases the other. (declared-only describes a
// different kind of model entirely, so it is exercised standalone above.)
func TestFixturesComposeIntoOneRecord(t *testing.T) {
	srv := seedVersions(t)
	for _, name := range []string{"scanner-safetensors", "sdk-publish"} {
		raw, err := os.ReadFile(filepath.Join("testdata/producers", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if code, body := do(t, srv, "PATCH", patchPath, string(raw), nil); code != http.StatusOK {
			t.Fatalf("%s: %d %v", name, code, body)
		}
	}

	_, got := do(t, srv, "GET", patchPath+"?include=layers,sources", "", nil)

	hashes, _ := got["hashes"].(map[string]any)
	if len(hashes) != 4 {
		t.Fatalf("all four hash levels should be present after the scanner and the SDK: %v", hashes)
	}
	if got["tensorCount"] != float64(291) || got["paramCountTotal"] != float64(8030261248) {
		t.Fatalf("facts from different producers should coexist: %v", got)
	}
	fw, _ := got["framework"].(map[string]any)
	if fw["name"] != "pytorch" {
		t.Fatalf("the scanner's framework should survive a write that does not mention it: %v", fw)
	}
	layers, _ := got["layers"].([]any)
	if len(layers) != 4 {
		t.Fatalf("layer breakdown from the scanner should survive later writes: %d", len(layers))
	}
	// Coverage merges per key rather than being replaced wholesale, and the same key
	// written twice is last-writer-wins: the scanner could not compute paramCountTotal,
	// the SDK could.
	cov, _ := got["coverage"].(map[string]any)
	if cov["quantMethod"] != "unavailable_for_format" {
		t.Fatalf("the scanner's coverage entry should survive: %v", cov)
	}
	if cov["paramCountTotal"] != "filled" {
		t.Fatalf("a re-reported coverage key should be last-writer-wins: %v", cov)
	}

	// Attribution still names who supplied what, which is the point of keeping several
	// producers out of each other's way.
	sources, _ := got["fieldSources"].(map[string]any)
	tc, _ := sources["tensorCount"].(map[string]any)
	pc, _ := sources["paramCountTotal"].(map[string]any)
	if tc["reporter"] != "lineage-scanner" || pc["reporter"] != "lineage-sdk" {
		t.Fatalf("per-field attribution should name each producer: %v", sources)
	}
}
