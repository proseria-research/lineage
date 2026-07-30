package adminui

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/proseria-research/lineage/internal/api"
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
	models, _, err := r.svc.ListModels(ctx, domain.ListOptions{PageSize: bigPage})
	if err != nil {
		api.WriteError(w, err)
		return
	}
	stages := map[string]int{"draft": 0, "staging": 0, "production": 0, "archived": 0}
	c := counts{Models: len(models)}
	for _, m := range models {
		vs, _, err := r.svc.ListVersions(ctx, m.Name, domain.ListOptions{PageSize: bigPage})
		if err != nil {
			api.WriteError(w, err)
			return
		}
		c.Versions += len(vs)
		for _, v := range vs {
			stages[string(v.Stage)]++
			if arts, err := r.svc.ListArtifacts(ctx, m.Name, v.Name); err == nil {
				c.Artifacts += len(arts)
			}
			if deps, err := r.svc.ListDeployments(ctx, m.Name, v.Name); err == nil {
				c.Deployments += len(deps)
			}
		}
	}
	recent, _, _ := r.svc.ListAudit(ctx, "", "", domain.ListOptions{PageSize: 12})
	api.WriteJSON(w, http.StatusOK, overviewDTO{Counts: c, Stages: stages, Recent: nz(recent)})
}

func (r *Router) models(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	o := domain.ListOptions{Q: req.URL.Query().Get("q"), PageSize: bigPage, PageToken: req.URL.Query().Get("pageToken")}
	items, next, err := r.svc.ListModels(ctx, o)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	out := make([]modelRollup, 0, len(items))
	for _, m := range items {
		out = append(out, r.rollup(ctx, m))
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
	api.WriteJSON(w, http.StatusOK, versionDetailDTO{
		Model: model, Version: toSummary(v), AllowedTargets: allowedTargets(v.Stage),
		Artifacts: nz(arts), Lineage: nz(edges), Deployments: nz(deps), Audit: nz(audit),
	})
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
	}
}

func toSummary(v *domain.ModelVersion) versionSummary {
	return versionSummary{
		ID: v.ID, Name: v.Name, Stage: v.Stage, Author: v.Author, Description: v.Description,
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
