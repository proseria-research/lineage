package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// UI-shaped read DTOs (§06.3). The BFF aggregates the same core services the Model API uses;
// it adds no business logic, only shaping. Counts are computed by listing — fine at the
// single-tenant scale of a self-hosted registry; a Postgres aggregate can replace it later.

const bigPage = 500

type counts struct {
	Models      int `json:"models"`
	Versions    int `json:"versions"`
	Artifacts   int `json:"artifacts"`
	Deployments int `json:"deployments"`
}

type overviewDTO struct {
	Counts counts               `json:"counts"`
	Stages map[string]int       `json:"stages"`
	Recent []*domain.AuditEvent `json:"recent"`
}

type modelRollup struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Owner        string            `json:"owner,omitempty"`
	State        domain.ModelState `json:"state"`
	Labels       map[string]string `json:"labels,omitempty"`
	VersionCount int               `json:"versionCount"`
	Production   string            `json:"production"`
	UpdatedAt    int64             `json:"updatedAt"`
	// Classification is null when nobody has classified this model under the regime. The
	// console renders that as `unclassified`, never as `minimal` (§16.9) — which is only
	// possible because the absence arrives as an absence rather than a default.
	Classification *domain.ClassificationView `json:"classification"`
	// LegalHold is the model's own hold, null when not held (§19.8). The console shows it
	// and shows what it blocks — a disabled action with no stated reason is worse than one
	// that is simply absent.
	LegalHold *domain.Hold `json:"legalHold"`
}

type versionSummary struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Stage       domain.Stage      `json:"stage"`
	Author      string            `json:"author,omitempty"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	CreatedAt   int64             `json:"createdAt"`
	UpdatedAt   int64             `json:"updatedAt"`
	// LegalHold is this version's own hold. A version under a held *model* is not marked
	// here — inheritance is resolved at the delete guard, never copied onto rows — so the
	// version page reads the model's hold separately (see versionDetailDTO.ModelHold).
	LegalHold *domain.Hold `json:"legalHold"`
}

type modelDetailDTO struct {
	Model      modelRollup      `json:"model"`
	Versions   []versionSummary `json:"versions"`
	Production string           `json:"production"`
}

type versionDetailDTO struct {
	Model          string                `json:"model"`
	Version        versionSummary        `json:"version"`
	AllowedTargets []domain.Stage        `json:"allowedTargets"`
	Artifacts      []*domain.Artifact    `json:"artifacts"`
	Lineage        []*domain.LineageEdge `json:"lineage"`
	Deployments    []*domain.Deployment  `json:"deployments"`
	Audit          []*domain.AuditEvent  `json:"audit"`
	// Insight is null when nobody has reported on this version. The console renders that
	// as "not reported", never as zeroes (§11.8).
	Insight     *domain.VersionInsight `json:"insight"`
	Footprints  []*domain.Footprint    `json:"footprints"`
	Evaluations []*domain.Evaluation   `json:"evaluations"`
	// Classification belongs to the *model*, not this version, and is shown here because
	// the version page is where someone asks "is this thing I am about to promote governed,
	// and is that assessment still good?" (§16.9). Null when unclassified.
	Classification *domain.ClassificationView `json:"classification"`
	// ModelHold is the owning model's hold, which covers this version transitively (§19.3.1).
	// Carried separately from Version.LegalHold so the page can say *which* subject is held —
	// releasing the wrong one is the mistake this prevents.
	ModelHold *domain.Hold `json:"modelHold"`
}

// stageOrder gives transition buttons a stable, sensible order (promote paths first).
var stageOrder = []domain.Stage{domain.StageStaging, domain.StageProduction, domain.StageArchived, domain.StageDraft}

// allowedTargets lists the legal next stages from `s` in a stable UI order (§02.4).
func allowedTargets(s domain.Stage) []domain.Stage {
	out := []domain.Stage{}
	for _, t := range stageOrder {
		if domain.CanTransition(s, t) {
			out = append(out, t)
		}
	}
	return out
}

// actor is the infra-provided identity for audit attribution (§00 axiom 4); dev has no infra,
// so it defaults to "console".
func (r *Router) actor(req *http.Request) string {
	if a := req.Header.Get("X-Lineage-Actor"); a != "" {
		return a
	}
	return "console"
}

func (r *Router) overview(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	st, err := r.svc.Stats(ctx) // shared with the /metrics domain gauges (§09.2)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	stages := map[string]int{}
	for k, v := range st.Stages {
		stages[string(k)] = v
	}
	recent, _, _ := r.svc.ListAudit(ctx, "", "", domain.ListOptions{PageSize: 12})
	api.WriteJSON(w, http.StatusOK, overviewDTO{
		Counts: counts{Models: st.Models, Versions: st.Versions, Artifacts: st.Artifacts, Deployments: st.Deployments},
		Stages: stages,
		Recent: nz(recent),
	})
}

// models backs both the model table and the §16.9 inventory view — they are the same page.
// Unlike the Model API, this always carries the classification: the console is the inventory,
// so the join is the point rather than an opt-in cost.
func (r *Router) models(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	q := req.URL.Query()
	o := domain.ListOptions{Q: q.Get("q"), PageSize: bigPage, PageToken: q.Get("pageToken")}

	f := domain.ClassificationFilter{
		Regime:            domain.RegimeEUAIAct,
		EUSystemRiskClass: domain.EUSystemRiskClass(q.Get("euSystemRiskClass")),
		EUGpaiTier:        domain.EUGpaiTier(q.Get("euGpaiTier")),
	}
	items, next, err := r.svc.ListInventory(ctx, o, f, domain.ClassificationState(q.Get("classificationState")))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	out := make([]modelRollup, 0, len(items))
	for _, it := range items {
		roll := r.rollup(ctx, it.Model)
		roll.Classification = it.Classification
		out = append(out, roll)
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "nextPageToken": next})
}

func (r *Router) modelDetail(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	name := req.PathValue("model")
	m, err := r.svc.GetModel(ctx, name)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	vs, _, err := r.svc.ListVersions(ctx, name, domain.ListOptions{PageSize: bigPage})
	if err != nil {
		api.WriteError(w, err)
		return
	}
	summaries := make([]versionSummary, 0, len(vs))
	production := ""
	for _, v := range vs {
		summaries = append(summaries, toSummary(v))
		if v.Stage == domain.StageProduction {
			production = v.Name
		}
	}
	roll := toRollup(m, len(vs), production)
	// An unclassified model is not an error here: absence is the state (§16.4).
	if c, cerr := r.svc.GetClassification(ctx, name, domain.RegimeEUAIAct); cerr == nil {
		roll.Classification = c
	}
	api.WriteJSON(w, http.StatusOK, modelDetailDTO{Model: roll, Versions: summaries, Production: production})
}

func (r *Router) versionDetail(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	model, version := req.PathValue("model"), req.PathValue("version")
	v, err := r.svc.GetVersion(ctx, model, version)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	arts, _ := r.svc.ListArtifacts(ctx, model, version)
	edges, _ := r.svc.ListLineage(ctx, model, version)
	deps, _ := r.svc.ListDeployments(ctx, model, version)
	audit, _, _ := r.svc.ListAudit(ctx, "model_version", v.ID, domain.ListOptions{PageSize: 50})
	// Insight is optional: an error here means nothing has been reported, which is a
	// legitimate state the panel renders as such (§11.2).
	insight, _ := r.svc.GetInsight(ctx, model, version, true)
	footprints, _ := r.svc.ListFootprints(ctx, model, version)
	evals, _ := r.svc.ListEvaluations(ctx, model, version)
	// Unclassified is a state, not a failure, so a not_found here becomes a null panel.
	classification, _ := r.svc.GetClassification(ctx, model, domain.RegimeEUAIAct)
	// The owning model's hold covers this version transitively (§19.3.1), and the page has
	// to be able to say which subject is actually held.
	var modelHold *domain.Hold
	if m, merr := r.svc.GetModel(ctx, model); merr == nil {
		modelHold = m.LegalHold
	}
	api.WriteJSON(w, http.StatusOK, versionDetailDTO{
		Model: model, Version: toSummary(v), AllowedTargets: allowedTargets(v.Stage),
		Artifacts: nz(arts), Lineage: nz(edges), Deployments: nz(deps), Audit: nz(audit),
		Insight: insight, Footprints: nz(footprints), Evaluations: nz(evals),
		Classification: classification, ModelHold: modelHold,
	})
}

// compare backs the console's side-by-side view (§11.8). It is the same core diff the
// Model API serves, shaped for one model's two versions.
func (r *Router) compare(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	if from == "" || to == "" {
		api.WriteError(w, domain.Invalid("compare requires ?from= and ?to= version names"))
		return
	}
	model := req.PathValue("model")
	d, err := r.svc.DiffVersions(req.Context(), model, from, model, to)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, d)
}

// transition promotes/moves a version's stage (§03.7) — the console's human-approver action.
// It calls the same core operation the Model API does; audit + singleton demotion are identical.
func (r *Router) transition(w http.ResponseWriter, req *http.Request) {
	var body struct {
		To     domain.Stage `json:"to"`
		Reason string       `json:"reason"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		api.WriteError(w, domain.Invalid("invalid JSON: "+err.Error()))
		return
	}
	v, err := r.svc.Transition(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), body.To, body.Reason)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, versionSummary{
		ID: v.ID, Name: v.Name, Stage: v.Stage, Author: v.Author,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
	})
}

// setClassification is the console's second write, after `transition` (§16.9). It calls the
// same core operation the Model API does, so validation, the server-set anchor and the
// `classification.set` audit event are identical — the console is a client of the same rules,
// not a bypass around them.
//
// A form is the point: a compliance reader who can see that a model is unclassified but can
// only fix it with a curl is reading a report, not doing a job.
func (r *Router) setClassification(w http.ResponseWriter, req *http.Request) {
	regime := domain.Regime(req.PathValue("regime"))
	if !domain.ValidRegime(regime) {
		api.WriteError(w, domain.Invalid("unknown regime '"+string(regime)+"'"))
		return
	}
	var in core.ClassificationInput
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		api.WriteError(w, domain.Invalid("invalid JSON: "+err.Error()))
		return
	}
	v, err := r.svc.SetClassification(req.Context(), r.actor(req), req.PathValue("model"), regime, in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, v)
}

// lineageGraph traverses provenance (upstream) or impact (downstream) for the console's
// version-detail graph view (§06.2, §07.3).
func (r *Router) lineageGraph(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	dir := domain.LineageDirection(q.Get("direction"))
	if dir == "" {
		dir = domain.BothDirections
	}
	query := core.LineageQuery{Direction: dir}
	if d, err := strconv.Atoi(q.Get("depth")); err == nil {
		query.Depth = d
	}
	g, err := r.svc.TraverseLineage(req.Context(), req.PathValue("model"), req.PathValue("version"), query)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, g)
}

func (r *Router) activity(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	items, next, err := r.svc.ListAudit(req.Context(), q.Get("subjectType"), q.Get("subjectId"),
		domain.ListOptions{PageSize: 30, PageToken: q.Get("pageToken")})
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": nz(items), "nextPageToken": next})
}

// rollup computes a model's per-row aggregates (version count + production pointer).
func (r *Router) rollup(ctx context.Context, m *domain.Model) modelRollup {
	vs, _, _ := r.svc.ListVersions(ctx, m.Name, domain.ListOptions{PageSize: bigPage})
	production := ""
	for _, v := range vs {
		if v.Stage == domain.StageProduction {
			production = v.Name
		}
	}
	return toRollup(m, len(vs), production)
}

func toRollup(m *domain.Model, versionCount int, production string) modelRollup {
	return modelRollup{
		ID: m.ID, Name: m.Name, Owner: m.Owner, State: m.State, Labels: m.Labels,
		VersionCount: versionCount, Production: production, UpdatedAt: m.UpdatedAt,
		LegalHold: m.LegalHold,
	}
}

func toSummary(v *domain.ModelVersion) versionSummary {
	return versionSummary{
		LegalHold: v.LegalHold,
		ID:        v.ID, Name: v.Name, Stage: v.Stage, Author: v.Author, Description: v.Description,
		Labels: v.Labels, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
	}
}

// nz coalesces a nil slice to an empty one so the JSON is `[]`, not `null` (the SPA maps it).
func nz[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ---- Evidence integrity (§19.8) ----

// evidenceDTO is the install-level answer to "is the record trustworthy?": the floor the
// process is actually running under, and the state of audit attestation.
//
// §19.8 puts this on an ops view rather than a model page because it is a property of the
// install, not of any model. The console's install-level compliance page is where a reader
// is already asking the question, so it lands there.
type evidenceDTO struct {
	Retention   domain.RetentionConfig   `json:"retention"`
	Attestation domain.AttestationConfig `json:"attestation"`
	// Verify is filled only by the verify endpoint. Recomputing every sealed epoch means
	// reading every audit row ever written, which is not something a page load should do —
	// so the status strip is cheap and the scan is an explicit action.
	Verify *domain.VerifyResult `json:"verify,omitempty"`
}

func (r *Router) evidence(w http.ResponseWriter, req *http.Request) {
	api.WriteJSON(w, http.StatusOK, evidenceDTO{
		Retention:   r.svc.Retention(),
		Attestation: r.svc.Attestation(),
	})
}

// verifyEvidence runs the full recompute. Deliberately a separate, explicitly-triggered
// request; see evidenceDTO.Verify.
func (r *Router) verifyEvidence(w http.ResponseWriter, req *http.Request) {
	res, err := r.svc.VerifyAudit(req.Context(), 0, 0)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, evidenceDTO{
		Retention: r.svc.Retention(), Attestation: r.svc.Attestation(), Verify: &res,
	})
}
