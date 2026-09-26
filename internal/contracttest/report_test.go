package contracttest_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/proseria-research/lineage/internal/contracttest"
)

// M19 read-from-outside: every fact the reference report needs is written and read over /v1,
// by a client that shares nothing with the server but a URL.
func TestReadFromOutside(t *testing.T) {
	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			reg := newRegistry(t, dialect, registryOpts{})
			c := &contracttest.Client{Base: reg.ModelAPI.URL}

			asOf := seedReportFacts(t, c)
			subj := contracttest.Subject{Model: "fraud-detector", Version: "2.0.0", AsOf: asOf}

			rep, err := c.Collect(subj)
			if err != nil {
				t.Fatalf("collect: %v", err)
			}
			first, err := rep.Canonical()
			if err != nil {
				t.Fatal(err)
			}
			assertReport(t, rep)

			// Collection is a pure read of recorded facts: nothing in it may depend on when it
			// ran. There is no clock to inject into the core, so let the wall clock move.
			time.Sleep(25 * time.Millisecond)
			rep2, err := c.Collect(subj)
			if err != nil {
				t.Fatalf("second collect: %v", err)
			}
			second, _ := rep2.Canonical()
			if !bytes.Equal(first, second) {
				t.Fatalf("two collections differ:\n--- first\n%s\n--- second\n%s", first, second)
			}
		})
	}
}

// seedReportFacts writes the report's subject through /v1 and returns the asOf instant: after
// every fact but the legal hold, which is written later so the bounded read can be seen to
// exclude it.
func seedReportFacts(t *testing.T, c *contracttest.Client) int64 {
	t.Helper()
	ci, risk, mrm, ra := c.As("ci@acme.example"), c.As("risk@acme.example"), c.As("mrm@acme.example"), c.As("ra@acme.example")
	must := func(cl *contracttest.Client, want int, method, path, body string) map[string]any {
		t.Helper()
		out, err := cl.Must(want, method, path, body)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	const m = "/v1/models/fraud-detector"
	ladder := func(v, shape, weights string) {
		must(ci, http.StatusOK, "PATCH", m+"/versions/"+v+"/insight", `{"schemaVersion":"1","source":"derived",
			"facts":{"hashes":{"topology":"t","shape":"`+shape+`","dtype":"d","weights":"`+weights+`"}}}`)
	}

	must(ci, http.StatusCreated, "POST", "/v1/models", `{"name":"fraud-detector","owner":"risk-team",
		"description":"Card-not-present fraud scorer","labels":{"domain":"risk"}}`)
	must(ci, http.StatusCreated, "POST", m+"/versions", `{"name":"1.0.0","author":"dev@acme.example"}`)
	ladder("1.0.0", "sh", "w1")

	must(risk, http.StatusOK, "PUT", m+"/classifications/eu_ai_act", `{"euSystemRiskClass":"high_annex_iii",
		"euGpaiTier":"none","intendedPurpose":"Scores card-not-present transactions for manual review.",
		"basis":"Annex III §5(b) — creditworthiness adjacent."}`)
	must(mrm, http.StatusOK, "PUT", m+"/classifications/mrm",
		`{"mrmTier":"tier_1","basis":"Drives automated card-not-present declines above $500."}`)
	must(ra, http.StatusCreated, "POST", m+"/change-plans", `{"ref":"K243117",
		"summary":"Periodic retraining on additional sites. Architecture frozen.",
		"allowedVerdicts":["identical","reweighted"],"allowedMethods":["retrain"],"effectiveFrom":0}`)

	must(ci, http.StatusCreated, "POST", m+"/versions", `{"name":"2.0.0","author":"dev@acme.example",
		"artifacts":[{"kind":"MODEL","name":"model.onnx","uri":"s3://models/fraud/2.0.0/model.onnx",
		"digest":"sha256:1a2b3c","sizeBytes":12582912,"modelFormat":{"name":"onnx","version":"1.16"}}]}`)
	must(ci, http.StatusCreated, "POST", m+"/versions/2.0.0/lineage",
		`{"relation":"derived_from","to":{"version":"1.0.0"},"properties":{"method":"retrain"}}`)
	must(ci, http.StatusCreated, "POST", m+"/versions/2.0.0/lineage",
		`{"relation":"trained_on","to":{"uri":"s3://datasets/fraud/2024q2"}}`)
	ladder("2.0.0", "sh", "w2")
	must(ci, http.StatusCreated, "POST", m+"/versions/2.0.0/evaluations",
		`{"suite":"holdout-2024q2","metric":"auroc","split":"test","value":0.931,"higherIsBetter":true,"nSamples":50000}`)
	for _, to := range []string{"staging", "production"} {
		must(ci, http.StatusOK, "POST", m+"/versions/2.0.0:transition", `{"to":"`+to+`","reason":"eval gate passed"}`)
	}
	must(mrm, http.StatusCreated, "POST", m+"/versions/2.0.0/validations",
		`{"outcome":"approved","scope":"full","findings":"none material"}`)

	// asOf sits strictly between the two phases, on a millisecond boundary of its own.
	time.Sleep(5 * time.Millisecond)
	asOf := time.Now().UnixMilli()
	time.Sleep(5 * time.Millisecond)

	must(c.As("counsel@acme.example"), http.StatusOK, "POST", m+"/versions/2.0.0:hold", `{"reason":"matter 2026-17"}`)
	return asOf
}

// assertReport checks the report's contract: every heading is filled or explicitly not held,
// and the facts that were written are the facts that were read.
func assertReport(t *testing.T, rep *contracttest.Report) {
	t.Helper()
	if len(rep.Sections) != len(contracttest.Headings) {
		t.Fatalf("sections = %d, headings = %d", len(rep.Sections), len(contracttest.Headings))
	}
	// Nothing was recorded for modification review, and the report must say so rather than
	// omit the heading.
	wantNotHeld := map[string]bool{"modification_review": true}
	for _, s := range rep.Sections {
		switch {
		case s.Status != contracttest.Filled && s.Status != contracttest.NotHeld:
			t.Errorf("%s: status %q", s.Key, s.Status)
		case wantNotHeld[s.Key] && s.Status != contracttest.NotHeld:
			t.Errorf("%s: %s, want not_held", s.Key, s.Status)
		case !wantNotHeld[s.Key] && s.Status != contracttest.Filled:
			t.Errorf("%s: %s, want filled", s.Key, s.Status)
		}
		for _, r := range s.Reads {
			if !wantNotHeld[s.Key] && r.Status != contracttest.Filled {
				t.Errorf("%s/%s (%s): %s", s.Key, r.Name, r.Path, r.Status)
			}
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	obj := func(key, read string) map[string]any {
		m, _ := rep.Section(key).Read(read).Body.(map[string]any)
		return m
	}
	list := func(key, read string) []any {
		l, _ := rep.Section(key).Read(read).Body.([]any)
		return l
	}
	actions := func(events []any) map[string]bool {
		out := map[string]bool{}
		for _, e := range events {
			out[e.(map[string]any)["action"].(string)] = true
		}
		return out
	}

	// Version-scoped.
	v := obj("version", "version")
	if v["name"] != "2.0.0" || v["stage"] != "production" || v["author"] != "dev@acme.example" {
		t.Errorf("version: %v", v)
	}
	if a := list("version", "artifacts"); len(a) != 1 || a[0].(map[string]any)["digest"] != "sha256:1a2b3c" {
		t.Errorf("artifacts: %v", a)
	}

	// Install-scoped, asOf-bounded: everything before asOf, nothing after it.
	bounded := list("audit_as_of", "events")
	for _, e := range bounded {
		at, _ := e.(map[string]any)["at"].(json.Number).Int64()
		if at > rep.Subject.AsOf {
			t.Errorf("event after asOf: %v", e)
		}
	}
	ba := actions(bounded)
	for _, a := range []string{"model.create", "version.create", "classification.set", "change_plan.declare",
		"lineage.add", "evaluation.create", "version.stage_changed", "validation.record"} {
		if !ba[a] {
			t.Errorf("asOf read is missing %s: %v", a, ba)
		}
	}
	if ba["hold.set"] {
		t.Error("asOf read includes the hold written after it")
	}

	// Evaluations, lineage.
	if e := list("evaluations", "evaluations"); len(e) != 1 || e[0].(map[string]any)["metric"] != "auroc" {
		t.Errorf("evaluations: %v", e)
	}
	if e := list("lineage", "edges"); len(e) != 2 {
		t.Errorf("lineage edges: %v", e)
	}

	// Audit history, unbounded: the hold is there, attributed verbatim.
	va := list("audit_history", "version")
	found := false
	for _, e := range va {
		ev := e.(map[string]any)
		if ev["action"] == "hold.set" {
			found = ev["actor"] == "counsel@acme.example"
		}
	}
	if !found {
		t.Errorf("version audit lacks an attributed hold.set: %v", va)
	}

	// EU classification fields.
	eu := obj("eu_classification", "classification")
	if eu["euSystemRiskClass"] != "high_annex_iii" || eu["euGpaiTier"] != "none" ||
		eu["classifiedBy"] != "risk@acme.example" || eu["basis"] == nil || eu["intendedPurpose"] == nil {
		t.Errorf("eu classification: %v", eu)
	}

	// MRM (M17).
	if mr := obj("model_risk", "classification"); mr["mrmTier"] != "tier_1" || mr["regime"] != "mrm" {
		t.Errorf("mrm classification: %v", mr)
	}
	if vals, _ := obj("model_risk", "validations")["items"].([]any); len(vals) != 1 ||
		vals[0].(map[string]any)["validatedBy"] != "mrm@acme.example" {
		t.Errorf("validations: %v", obj("model_risk", "validations"))
	}

	// Change-plan conformance (M18): retrain + reweighted is inside the K243117 envelope.
	if cf := list("change_control", "conformance"); len(cf) != 1 || cf[0].(map[string]any)["conformance"] != "within_plan" {
		t.Errorf("conformance: %v", cf)
	}

	// Retention: the floors in force, and the hold on the subject.
	if f := obj("retention", "floors"); f["minArchivedVersionDays"] != json.Number("3650") {
		t.Errorf("retention floors: %v", f)
	}
	if h, _ := obj("retention", "version")["legalHold"].(map[string]any); h == nil || h["heldBy"] != "counsel@acme.example" {
		t.Errorf("legal hold: %v", obj("retention", "version")["legalHold"])
	}
}

// asOf is validated, not read as "unbounded" when garbled: that would silently widen a report
// meant to be pinned.
func TestAuditAsOfRejectsGarbage(t *testing.T) {
	reg := newRegistry(t, "sqlite", registryOpts{})
	c := &contracttest.Client{Base: reg.ModelAPI.URL}
	for _, q := range []string{"asOf=yesterday", "asOf=-5", "asOf=0", "asOf=2026-01-01T00:00:00Z"} {
		code, body, err := c.Do("GET", "/v1/audit?"+q, nil)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := body.(map[string]any); code != http.StatusBadRequest || b["code"] != "invalid_argument" ||
			!strings.Contains(b["detail"].(string), "asOf") {
			t.Errorf("%s: %d %v", q, code, body)
		}
	}
}
