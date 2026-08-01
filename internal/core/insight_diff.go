package core

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/proseria-research/lineage/internal/domain"
)

// Version-to-version diff (§11.6.2). Computed entirely over stored facts — no artifact
// access, the same posture as lineage traversal (§07).

// DiffSide identifies one end of a comparison.
type DiffSide struct {
	Model   string `json:"model"`
	Version string `json:"version"`
}

// TensorDiff summarises which tensors changed, from the per-tensor digests producers
// retain in archDoc (§11.4.2). This is what separates a merged LoRA adapter from a full
// fine-tune; a single artifact digest cannot express it.
type TensorDiff struct {
	Unchanged int `json:"unchanged"`
	Changed   int `json:"changed"`
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	// ChangedPatterns collapses repeated block indices, so 32 changed `q_proj` tensors
	// read as one `model.layers.*.self_attn.q_proj` entry.
	ChangedPatterns []string `json:"changedPatterns,omitempty"`
}

// ScalarDelta is a from/to pair for a numeric fact, tagged with where each side came from.
type ScalarDelta struct {
	Field      string `json:"field"`
	From       *int64 `json:"from,omitempty"`
	To         *int64 `json:"to,omitempty"`
	Delta      *int64 `json:"delta,omitempty"`
	FromSource string `json:"fromSource,omitempty"`
	ToSource   string `json:"toSource,omitempty"`
}

// FootprintDelta compares one memory scenario across versions. Scenarios only compare
// against the same scenario name — a bs1-2k estimate says nothing about bs32-8k.
type FootprintDelta struct {
	Scenario   string `json:"scenario"`
	From       *int64 `json:"fromTotalBytes,omitempty"`
	To         *int64 `json:"toTotalBytes,omitempty"`
	Delta      *int64 `json:"delta,omitempty"`
	FromSource string `json:"fromSource,omitempty"`
	ToSource   string `json:"toSource,omitempty"`
	Comparable bool   `json:"comparable"`
}

// MetricDelta is the accuracy tradeoff, derived on read (§11.3.4). Direction applies
// higherIsBetter so a lower perplexity reads as "better", not as a negative delta.
type MetricDelta struct {
	Suite          string   `json:"suite"`
	Metric         string   `json:"metric"`
	Split          string   `json:"split,omitempty"`
	HarnessVersion string   `json:"harnessVersion,omitempty"`
	From           *float64 `json:"from,omitempty"`
	To             *float64 `json:"to,omitempty"`
	Delta          *float64 `json:"delta,omitempty"`
	Direction      string   `json:"direction,omitempty"` // better | worse | same
	Comparable     bool     `json:"comparable"`
	Reason         string   `json:"reason,omitempty"` // why not comparable
}

// DiffBasis records which facts each side actually supplied, so a partial verdict is
// identifiable as partial rather than mistaken for a complete one (§11.4.3).
type DiffBasis struct {
	FromHasInsight bool     `json:"fromHasInsight"`
	ToHasInsight   bool     `json:"toHasInsight"`
	FromHashes     []string `json:"fromHashes,omitempty"`
	ToHashes       []string `json:"toHashes,omitempty"`
	TensorDigests  bool     `json:"tensorDigests"`
}

// InsightDiff is the response of the diff endpoints.
type InsightDiff struct {
	From       DiffSide                  `json:"from"`
	To         DiffSide                  `json:"to"`
	Verdict    domain.Verdict            `json:"verdict"`
	Candidates []domain.Verdict          `json:"candidates,omitempty"`
	Missing    []string                  `json:"missing,omitempty"`
	Hashes     map[string]domain.HashCmp `json:"hashes"`
	Tensors    *TensorDiff               `json:"tensors,omitempty"`
	Params     []ScalarDelta             `json:"params,omitempty"`
	Footprints []FootprintDelta          `json:"footprints,omitempty"`
	Metrics    []MetricDelta             `json:"metrics,omitempty"`
	Basis      DiffBasis                 `json:"basis"`
}

// DiffVersions compares two versions, which may belong to different models (a distilled
// student against its teacher).
func (s *Service) DiffVersions(ctx context.Context, fromModel, fromVer, toModel, toVer string) (*InsightDiff, error) {
	fv, err := s.store.GetVersion(ctx, fromModel, fromVer)
	if err != nil {
		return nil, err
	}
	tv, err := s.store.GetVersion(ctx, toModel, toVer)
	if err != nil {
		return nil, err
	}

	d := &InsightDiff{
		From: DiffSide{Model: fromModel, Version: fromVer},
		To:   DiffSide{Model: toModel, Version: toVer},
	}

	fi := s.insightOrNil(ctx, fv.ID)
	ti := s.insightOrNil(ctx, tv.ID)
	d.Basis.FromHasInsight, d.Basis.ToHasInsight = fi != nil, ti != nil

	var fh, th domain.Hashes
	if fi != nil {
		fh = fi.Hashes
		d.Basis.FromHashes = presentHashes(fh)
	}
	if ti != nil {
		th = ti.Hashes
		d.Basis.ToHashes = presentHashes(th)
	}
	c := domain.Classify(fh, th)
	d.Verdict, d.Candidates, d.Missing, d.Hashes = c.Verdict, c.Candidates, c.Missing, c.Hashes

	if fi != nil && ti != nil {
		if td := tensorDiff(fi.ArchDoc, ti.ArchDoc); td != nil {
			d.Tensors = td
			d.Basis.TensorDigests = true
		}
		d.Params = paramDeltas(fi, ti)
	}

	d.Footprints = s.footprintDeltas(ctx, fv.ID, tv.ID)
	d.Metrics = s.metricDeltas(ctx, fv.ID, tv.ID)
	return d, nil
}

// insightOrNil treats "no insight recorded" as an absence rather than an error: a diff
// against a version nobody has reported on is a legitimate `unknown`, not a 404.
func (s *Service) insightOrNil(ctx context.Context, versionID string) *domain.VersionInsight {
	in, err := s.store.GetInsight(ctx, versionID)
	if err != nil {
		return nil
	}
	return in
}

func presentHashes(h domain.Hashes) []string {
	var out []string
	for _, p := range []struct {
		name string
		val  string
	}{{"topology", h.Topology}, {"shape", h.Shape}, {"dtype", h.Dtype}, {"weights", h.Weights}} {
		if p.val != "" {
			out = append(out, p.name)
		}
	}
	return out
}

// blockIndex matches the numeric index of a repeated block so changed tensor names can be
// collapsed into one pattern.
var blockIndex = regexp.MustCompile(`\.\d+\.`)

// tensorDiff compares the per-tensor digest lists. It returns nil when either side has no
// parseable tensor list — the registry reports the absence rather than guessing.
func tensorDiff(fromDoc, toDoc json.RawMessage) *TensorDiff {
	from, ok1 := parseTensors(fromDoc)
	to, ok2 := parseTensors(toDoc)
	if !ok1 || !ok2 {
		return nil
	}
	td := &TensorDiff{}
	patterns := map[string]bool{}
	for name, digest := range from {
		other, ok := to[name]
		switch {
		case !ok:
			td.Removed++
		case other == digest:
			td.Unchanged++
		default:
			td.Changed++
			patterns[blockIndex.ReplaceAllString(name, ".*.")] = true
		}
	}
	for name := range to {
		if _, ok := from[name]; !ok {
			td.Added++
		}
	}
	for p := range patterns {
		td.ChangedPatterns = append(td.ChangedPatterns, p)
	}
	sort.Strings(td.ChangedPatterns)
	return td
}

func parseTensors(doc json.RawMessage) (map[string]string, bool) {
	if len(doc) == 0 {
		return nil, false
	}
	var ad domain.ArchDoc
	if err := json.Unmarshal(doc, &ad); err != nil || len(ad.Tensors) == 0 {
		return nil, false
	}
	out := make(map[string]string, len(ad.Tensors))
	for _, t := range ad.Tensors {
		if t.Name == "" || t.Digest == "" {
			continue
		}
		out[t.Name] = t.Digest
	}
	return out, len(out) > 0
}

func paramDeltas(from, to *domain.VersionInsight) []ScalarDelta {
	fields := []struct {
		name     string
		from, to *int64
	}{
		{"paramCountTotal", from.ParamCountTotal, to.ParamCountTotal},
		{"tensorCount", from.TensorCount, to.TensorCount},
		{"diskBytes", from.DiskBytes, to.DiskBytes},
		{"weightsBytes", from.WeightsBytes, to.WeightsBytes},
	}
	var out []ScalarDelta
	for _, f := range fields {
		if f.from == nil && f.to == nil {
			continue
		}
		d := ScalarDelta{Field: f.name, From: f.from, To: f.to,
			FromSource: string(fieldSource(from, f.name)), ToSource: string(fieldSource(to, f.name))}
		if f.from != nil && f.to != nil {
			delta := *f.to - *f.from
			d.Delta = &delta
		}
		out = append(out, d)
	}
	return out
}

// fieldSource prefers the per-field attribution and falls back to the row default (§11.2).
func fieldSource(in *domain.VersionInsight, field string) domain.FactSource {
	if fs, ok := in.FieldSources[field]; ok && fs.Source != "" {
		return fs.Source
	}
	return in.Source
}

func (s *Service) footprintDeltas(ctx context.Context, fromID, toID string) []FootprintDelta {
	fromFP, err1 := s.store.ListFootprints(ctx, fromID)
	toFP, err2 := s.store.ListFootprints(ctx, toID)
	if err1 != nil || err2 != nil || (len(fromFP) == 0 && len(toFP) == 0) {
		return nil
	}
	byScenario := map[string]*FootprintDelta{}
	order := []string{}
	get := func(scenario string) *FootprintDelta {
		if d, ok := byScenario[scenario]; ok {
			return d
		}
		d := &FootprintDelta{Scenario: scenario}
		byScenario[scenario] = d
		order = append(order, scenario)
		return d
	}
	for _, f := range fromFP {
		d := get(f.Scenario)
		d.From, d.FromSource = f.TotalBytes, string(f.Source)
	}
	for _, f := range toFP {
		d := get(f.Scenario)
		d.To, d.ToSource = f.TotalBytes, string(f.Source)
	}
	sort.Strings(order)
	out := make([]FootprintDelta, 0, len(order))
	for _, sc := range order {
		d := byScenario[sc]
		if d.From != nil && d.To != nil {
			delta := *d.To - *d.From
			d.Delta, d.Comparable = &delta, true
		}
		out = append(out, *d)
	}
	return out
}

// metricKey is the tuple that makes two evaluations comparable. Harness version is part of
// it: a score from a different harness build is a different measurement (§11.3.4).
type metricKey struct{ suite, metric, split, harnessVersion string }

func (s *Service) metricDeltas(ctx context.Context, fromID, toID string) []MetricDelta {
	fromEv, err1 := s.store.ListEvaluations(ctx, fromID)
	toEv, err2 := s.store.ListEvaluations(ctx, toID)
	if err1 != nil || err2 != nil || (len(fromEv) == 0 && len(toEv) == 0) {
		return nil
	}
	fromLatest, toLatest := latestPerKey(fromEv), latestPerKey(toEv)

	keys := map[metricKey]bool{}
	for k := range fromLatest {
		keys[k] = true
	}
	for k := range toLatest {
		keys[k] = true
	}
	ordered := make([]metricKey, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		return a.suite+a.metric+a.split+a.harnessVersion < b.suite+b.metric+b.split+b.harnessVersion
	})

	out := make([]MetricDelta, 0, len(ordered))
	for _, k := range ordered {
		f, hasFrom := fromLatest[k]
		t, hasTo := toLatest[k]
		md := MetricDelta{Suite: k.suite, Metric: k.metric, Split: k.split, HarnessVersion: k.harnessVersion}
		if hasFrom {
			v := f.Value
			md.From = &v
		}
		if hasTo {
			v := t.Value
			md.To = &v
		}
		switch {
		case !hasFrom:
			md.Reason = "not evaluated on the from side"
		case !hasTo:
			md.Reason = "not evaluated on the to side"
		default:
			// Both sides ran it. Refuse to compare across a higherIsBetter disagreement
			// rather than pick one and silently invert the meaning of the delta.
			if f.HigherIsBetter != t.HigherIsBetter {
				md.Reason = "higherIsBetter disagrees between the two records"
				break
			}
			delta := t.Value - f.Value
			md.Delta, md.Comparable = &delta, true
			md.Direction = direction(delta, f.HigherIsBetter)
		}
		out = append(out, md)
	}
	return out
}

// latestPerKey keeps the most recent run for each comparable tuple; the store already
// returns newest-first, so the first sighting wins.
func latestPerKey(evs []*domain.Evaluation) map[metricKey]*domain.Evaluation {
	out := map[metricKey]*domain.Evaluation{}
	for _, e := range evs {
		k := metricKey{strings.ToLower(e.Suite), strings.ToLower(e.Metric), e.Split, e.HarnessVersion}
		if _, seen := out[k]; !seen {
			out[k] = e
		}
	}
	return out
}

func direction(delta float64, higherIsBetter bool) string {
	switch {
	case delta == 0:
		return "same"
	case (delta > 0) == higherIsBetter:
		return "better"
	default:
		return "worse"
	}
}
