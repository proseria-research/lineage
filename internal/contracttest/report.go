package contracttest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The reference report (M19). Each heading is a fact a compliance report has to cite, paired
// with the /v1 reads that supply it. The table is the contract: if a heading cannot be filled
// from its reads, /v1 has a gap.

// Shape says how a read's response is interpreted.
type Shape int

const (
	One  Shape = iota // a single resource: 200 = held, 404 = not held
	List              // a collection: non-empty = held, empty = not held; every page is read
)

// Read is one /v1 GET. Path placeholders: {model}, {version}, {versionId}, {asOf}.
type Read struct {
	Name  string
	Path  string
	Shape Shape
}

// Heading is one section of the report.
type Heading struct {
	Key   string
	Title string
	Reads []Read
}

const (
	versionPath = "/v1/models/{model}/versions/{version}"
)

// Headings is the reference report's definition, in report order.
var Headings = []Heading{
	{Key: "version", Title: "Version record (version-scoped)", Reads: []Read{
		{"version", versionPath, One},
		{"artifacts", versionPath + "/artifacts", List},
		{"insight", versionPath + "/insight?include=sources", One},
	}},
	{Key: "audit_as_of", Title: "Install audit trail as of the report date (install-scoped)", Reads: []Read{
		{"events", "/v1/audit?asOf={asOf}&pageSize=500", List},
	}},
	{Key: "evaluations", Title: "Evaluations", Reads: []Read{
		{"evaluations", versionPath + "/evaluations", List},
	}},
	{Key: "lineage", Title: "Lineage and provenance", Reads: []Read{
		{"edges", versionPath + "/lineage", List},
		{"graph", versionPath + "/lineage?direction=both", One},
	}},
	{Key: "audit_history", Title: "Audit history", Reads: []Read{
		{"model", "/v1/models/{model}/audit?pageSize=500", List},
		{"version", "/v1/audit?subjectType=model_version&subjectId={versionId}&pageSize=500", List},
	}},
	{Key: "eu_classification", Title: "EU AI Act classification", Reads: []Read{
		{"classification", "/v1/models/{model}/classifications/eu_ai_act", One},
	}},
	{Key: "modification_review", Title: "EU modification review (Art. 25)", Reads: []Read{
		{"reviews", versionPath + "/reviews", List},
	}},
	{Key: "model_risk", Title: "Model risk management (tier, validation)", Reads: []Read{
		{"classification", "/v1/models/{model}/classifications/mrm", One},
		{"validations", versionPath + "/validations", One},
	}},
	{Key: "change_control", Title: "Change control plan conformance", Reads: []Read{
		{"plans", "/v1/models/{model}/change-plans", List},
		{"conformance", versionPath + "/conformance", List},
	}},
	{Key: "retention", Title: "Retention floors and legal hold", Reads: []Read{
		{"floors", "/v1/retention", One},
		{"version", versionPath, One},
	}},
}

// Section statuses. There is deliberately no third value: a heading is filled, or /v1 says
// plainly that the registry holds no such fact. Anything else fails collection.
const (
	Filled  = "filled"
	NotHeld = "not_held"
)

// Subject pins what a report is about.
type Subject struct {
	Model   string `json:"model"`
	Version string `json:"version"`
	AsOf    int64  `json:"asOf"` // epoch-millis; bounds the install-scoped read
}

// ReadResult is one read's outcome.
type ReadResult struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Body   any    `json:"body,omitempty"`
}

// Section is one filled-in heading.
type Section struct {
	Key    string       `json:"key"`
	Title  string       `json:"title"`
	Status string       `json:"status"`
	Reads  []ReadResult `json:"reads"`
}

// Report is the assembled reference report.
type Report struct {
	Subject  Subject   `json:"subject"`
	Sections []Section `json:"sections"`
}

// Section returns the section with key, or nil.
func (r *Report) Section(key string) *Section {
	for i := range r.Sections {
		if r.Sections[i].Key == key {
			return &r.Sections[i]
		}
	}
	return nil
}

// Read returns the named read's result, or nil.
func (s *Section) Read(name string) *ReadResult {
	for i := range s.Reads {
		if s.Reads[i].Name == name {
			return &s.Reads[i]
		}
	}
	return nil
}

// Canonical encodes the report deterministically: struct fields in declaration order, map
// keys sorted (encoding/json's rule), and numbers exactly as the server sent them.
func (r *Report) Canonical() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

// Collect assembles the report for subj through /v1 alone.
func (c *Client) Collect(subj Subject) (*Report, error) {
	// {versionId} is itself a /v1 fact: the audit feed is keyed by id, and the only way an
	// outside client learns it is by reading the version.
	code, v, err := c.Do(http.MethodGet, fill(versionPath, subj, ""), nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("subject version %s@%s: status %d", subj.Model, subj.Version, code)
	}
	vid, _ := v.(map[string]any)["id"].(string)

	rep := &Report{Subject: subj}
	for _, h := range Headings {
		sec := Section{Key: h.Key, Title: h.Title, Status: NotHeld}
		for _, rd := range h.Reads {
			path := fill(rd.Path, subj, vid)
			res := ReadResult{Name: rd.Name, Path: rd.Path, Status: NotHeld}
			switch rd.Shape {
			case One:
				code, body, err := c.Do(http.MethodGet, path, nil)
				switch {
				case err != nil:
					return nil, err
				case code == http.StatusOK:
					res.Status, res.Body = Filled, body
				case code != http.StatusNotFound:
					return nil, fmt.Errorf("%s/%s: GET %s: status %d: %v", h.Key, rd.Name, path, code, body)
				}
			case List:
				code, items, err := c.GetAll(path)
				switch {
				case err != nil:
					return nil, err
				case code != http.StatusOK:
					return nil, fmt.Errorf("%s/%s: GET %s: status %d", h.Key, rd.Name, path, code)
				case len(items) > 0:
					res.Status, res.Body = Filled, items
				}
			}
			if res.Status == Filled {
				sec.Status = Filled
			}
			sec.Reads = append(sec.Reads, res)
		}
		rep.Sections = append(rep.Sections, sec)
	}
	return rep, nil
}

func fill(p string, s Subject, versionID string) string {
	return strings.NewReplacer(
		"{model}", url.PathEscape(s.Model),
		"{version}", url.PathEscape(s.Version),
		"{versionId}", url.QueryEscape(versionID),
		"{asOf}", strconv.FormatInt(s.AsOf, 10),
	).Replace(p)
}
