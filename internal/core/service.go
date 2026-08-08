// Package core is the domain core: business rules over the ports (§01). It is
// storage/engine-agnostic and never imports an adapter.
package core

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

type Service struct {
	store     domain.MetadataStore
	backends  map[string]domain.StorageBackend
	defBack   string
	cache     domain.ResolutionCache
	events    domain.EventBus
	meter     domain.Meter
	tracer    domain.Tracer
	signTTL   time.Duration
	uploadTTL time.Duration
	retention domain.RetentionConfig // §19.4; zero value = floors disabled, see WithRetention

	mu      sync.Mutex                // guards pending
	pending map[string]*pendingUpload // in-flight uploads keyed by uploadId (§05.6)
}

// Option configures a Service at construction (non-breaking additive knobs).
type Option func(*Service)

// WithMeter attaches a telemetry Meter (§09.2); nil leaves the no-op in place.
func WithMeter(m domain.Meter) Option {
	return func(s *Service) {
		if m != nil {
			s.meter = m
		}
	}
}

// WithTracer attaches a telemetry Tracer (§09.4); nil leaves the no-op in place.
func WithTracer(t domain.Tracer) Option {
	return func(s *Service) {
		if t != nil {
			s.tracer = t
		}
	}
}

func New(store domain.MetadataStore, backends map[string]domain.StorageBackend, defBack string, cache domain.ResolutionCache, events domain.EventBus, opts ...Option) *Service {
	s := &Service{
		store: store, backends: backends, defBack: defBack, cache: cache, events: events,
		meter: domain.NopMeter{}, tracer: domain.NopTracer{},
		signTTL: 15 * time.Minute, uploadTTL: time.Hour,
		pending: map[string]*pendingUpload{},
	}
	for _, o := range opts {
		o(s)
	}
	// Event-driven resolve-cache invalidation (§04.4): every mutation that can change a
	// resolution publishes an event carrying its model; we drop that model's cached entries.
	// A short TTL backstops any missed event.
	events.Subscribe(func(e domain.Event) {
		if e.Model != "" {
			s.cache.InvalidateModel(e.Model)
		}
	})
	return s
}

func (s *Service) backend(name string) domain.StorageBackend {
	if name == "" {
		name = s.defBack
	}
	return s.backends[name]
}

// ---- Inputs ----

type CreateModelInput struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Owner            string            `json:"owner"`
	Labels           map[string]string `json:"labels"`
	CustomProperties json.RawMessage   `json:"customProperties"`
}

type ArtifactInput struct {
	Kind           domain.ArtifactKind `json:"kind"`
	Name           string              `json:"name"`
	URI            string              `json:"uri"`
	StorageBackend string              `json:"storageBackend"`
	StoragePath    string              `json:"storagePath"`
	Digest         string              `json:"digest"`
	SizeBytes      int64               `json:"sizeBytes"`
	MediaType      string              `json:"mediaType"`
	ModelFormat    *domain.ModelFormat `json:"modelFormat"`
	ServiceAccount string              `json:"serviceAccount"`
}

type PublishVersionInput struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Author      string            `json:"author"`
	Labels      map[string]string `json:"labels"`
	Artifacts   []ArtifactInput   `json:"artifacts"`
}

// ---- Models ----

func (s *Service) CreateModel(ctx context.Context, actor string, in CreateModelInput) (*domain.Model, error) {
	if !domain.ValidName(in.Name) {
		return nil, domain.Invalid("invalid model name '" + in.Name + "'")
	}
	now := domain.NowMillis()
	m := &domain.Model{
		ID: domain.NewID(), Name: in.Name, Description: in.Description, Owner: in.Owner,
		State: domain.StateActive, Labels: in.Labels, CustomProperties: in.CustomProperties,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateModel(ctx, m); err != nil {
		return nil, err
	}
	s.audit(ctx, actor, "model.create", "model", m.ID, "created model "+m.Name, nil)
	return m, nil
}

func (s *Service) GetModel(ctx context.Context, name string) (*domain.Model, error) {
	return s.store.GetModel(ctx, name)
}

func (s *Service) ListModels(ctx context.Context, o domain.ListOptions) ([]*domain.Model, string, error) {
	return s.store.ListModels(ctx, o)
}

// Stats is a registry-wide snapshot for the dashboard and domain gauges (§06.2, §09.2).
// Computed by listing — fine at the single-tenant scale of a self-hosted registry.
type Stats struct {
	Models      int
	Versions    int
	Artifacts   int
	Deployments int
	Stages      map[domain.Stage]int
}

func (s *Service) Stats(ctx context.Context) (Stats, error) {
	st := Stats{Stages: map[domain.Stage]int{
		domain.StageDraft: 0, domain.StageStaging: 0, domain.StageProduction: 0, domain.StageArchived: 0,
	}}
	models, _, err := s.store.ListModels(ctx, domain.ListOptions{PageSize: 500})
	if err != nil {
		return st, err
	}
	st.Models = len(models)
	for _, m := range models {
		vs, _, err := s.store.ListVersions(ctx, m.Name, domain.ListOptions{PageSize: 500})
		if err != nil {
			return st, err
		}
		st.Versions += len(vs)
		for _, v := range vs {
			st.Stages[v.Stage]++
			if arts, err := s.store.ListArtifacts(ctx, v.ID); err == nil {
				st.Artifacts += len(arts)
			}
			if deps, err := s.store.ListDeployments(ctx, v.ID); err == nil {
				st.Deployments += len(deps)
			}
		}
	}
	return st, nil
}

// ---- Versions ----

func (s *Service) PublishVersion(ctx context.Context, actor, model string, in PublishVersionInput) (_ *domain.ModelVersion, _ []*domain.Artifact, err error) {
	ctx, sp := s.tracer.Start(ctx, "core.PublishVersion")
	defer func() { sp.RecordError(err); sp.End() }()
	sp.SetString("lineage.model", model)
	sp.SetString("lineage.version", in.Name)
	sp.SetInt("lineage.artifacts", int64(len(in.Artifacts)))

	m, err := s.store.GetModel(ctx, model)
	if err != nil {
		return nil, nil, err
	}
	if !domain.ValidName(in.Name) {
		return nil, nil, domain.Invalid("invalid version name '" + in.Name + "'")
	}
	now := domain.NowMillis()
	v := &domain.ModelVersion{
		ID: domain.NewID(), ModelID: m.ID, Model: m.Name, Name: in.Name,
		Description: in.Description, Author: in.Author, Stage: domain.StageDraft,
		Labels: in.Labels, CreatedAt: now, UpdatedAt: now,
	}
	// Assign, never `:=`, inside these blocks: a shadowed err would leave the deferred
	// RecordError above looking at a nil and marking a failed publish as a clean span.
	if err = s.store.CreateVersion(ctx, v); err != nil {
		return nil, nil, err
	}
	arts := make([]*domain.Artifact, 0, len(in.Artifacts))
	for _, ai := range in.Artifacts {
		var a *domain.Artifact
		a, err = s.registerArtifact(ctx, v.ID, ai)
		if err != nil {
			return nil, nil, err
		}
		arts = append(arts, a)
	}
	s.audit(ctx, actor, "version.create", "model_version", v.ID, "published "+m.Name+"@"+v.Name, nil)
	s.events.Publish(domain.Event{Type: "version.created", Model: m.Name, Version: v.Name})
	s.meter.VersionPublished()
	return v, arts, nil
}

func (s *Service) registerArtifact(ctx context.Context, versionID string, ai ArtifactInput) (*domain.Artifact, error) {
	if ai.Kind == "" {
		ai.Kind = domain.KindModel
	}
	if !domain.ValidArtifactName(ai.Name) {
		return nil, domain.Invalid("invalid artifact name '" + ai.Name + "'")
	}
	if ai.URI == "" {
		return nil, domain.Invalid("artifact '" + ai.Name + "' requires a uri (register-by-reference)")
	}
	// Fill digest/size from a Stat where possible (§05.5).
	if b := s.backend(ai.StorageBackend); b != nil && (ai.Digest == "" || ai.SizeBytes == 0) {
		if info, err := b.Stat(ctx, ai.URI); err == nil && info.Exists {
			if ai.Digest == "" {
				ai.Digest = info.Digest
			}
			if ai.SizeBytes == 0 {
				ai.SizeBytes = info.SizeBytes
			}
		}
	}
	now := domain.NowMillis()
	a := &domain.Artifact{
		ID: domain.NewID(), VersionID: versionID, Kind: ai.Kind, Name: ai.Name, URI: ai.URI,
		StorageBackend: ai.StorageBackend, StoragePath: ai.StoragePath, SizeBytes: ai.SizeBytes,
		Digest: ai.Digest, MediaType: ai.MediaType, ModelFormat: ai.ModelFormat,
		ServiceAccount: ai.ServiceAccount, CreatedAt: now, UpdatedAt: now,
	}
	return a, s.store.CreateArtifact(ctx, a)
}

func (s *Service) GetVersion(ctx context.Context, model, version string) (*domain.ModelVersion, error) {
	return s.store.GetVersion(ctx, model, version)
}

func (s *Service) ListVersions(ctx context.Context, model string, o domain.ListOptions) ([]*domain.ModelVersion, string, error) {
	return s.store.ListVersions(ctx, model, o)
}

// Transition moves a version to `to`, enforcing the state machine and singleton
// demotion, then audits, emits, and invalidates the resolve cache (§03.7, §02.4).
func (s *Service) Transition(ctx context.Context, actor, model, version string, to domain.Stage, reason string) (_ *domain.ModelVersion, err error) {
	ctx, sp := s.tracer.Start(ctx, "core.Transition")
	defer func() { sp.RecordError(err); sp.End() }()
	sp.SetString("lineage.model", model)
	sp.SetString("lineage.version", version)
	sp.SetString("lineage.stage.to", string(to))

	if !domain.ValidStage(to) {
		return nil, domain.Invalid("unknown stage '" + string(to) + "'")
	}
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	sp.SetString("lineage.stage.from", string(v.Stage))
	if v.Stage == to {
		return v, nil
	}
	if !domain.CanTransition(v.Stage, to) {
		return nil, domain.Precondition(
			"illegal stage transition "+string(v.Stage)+"→"+string(to),
			map[string]any{"allowedTargets": domain.AllowedTargets(v.Stage)})
	}
	// A singleton target with another version already there means this promotion demotes it.
	demoted := false
	if domain.IsSingleton(to) {
		if n, err := s.store.CountVersionsInStage(ctx, v.ModelID, to); err == nil && n > 0 {
			demoted = true
		}
	}
	if err = s.store.SetStage(ctx, v.ID, to, domain.IsSingleton(to)); err != nil {
		return nil, err
	}
	sp.SetString("lineage.demoted", strconv.FormatBool(demoted))
	data, _ := json.Marshal(map[string]string{"from": string(v.Stage), "to": string(to), "reason": reason})
	s.audit(ctx, actor, "version.stage_changed", "model_version", v.ID, model+"@"+version+" → "+string(to), data)
	s.events.Publish(domain.Event{Type: "version.stage_changed", Model: model, Version: version, Data: map[string]any{"to": to}})
	s.meter.StageTransitioned(to, demoted)
	v.Stage = to
	return v, nil
}

func (s *Service) audit(ctx context.Context, actor, action, subjType, subjID, summary string, data json.RawMessage) {
	_ = s.store.AppendAudit(ctx, &domain.AuditEvent{
		ID: domain.NewID(), At: domain.NowMillis(), Actor: actor, Action: action,
		SubjectType: subjType, SubjectID: subjID, Summary: summary, Data: data,
	})
}
