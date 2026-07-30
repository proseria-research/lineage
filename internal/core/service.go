// Package core is the domain core: business rules over the ports (§01). It is
// storage/engine-agnostic and never imports an adapter.
package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

type Service struct {
	store    domain.MetadataStore
	backends map[string]domain.StorageBackend
	defBack  string
	cache    domain.ResolutionCache
	events   domain.EventBus
	signTTL  time.Duration
}

func New(store domain.MetadataStore, backends map[string]domain.StorageBackend, defBack string, cache domain.ResolutionCache, events domain.EventBus) *Service {
	return &Service{store: store, backends: backends, defBack: defBack, cache: cache, events: events, signTTL: 15 * time.Minute}
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

// ---- Versions ----

func (s *Service) PublishVersion(ctx context.Context, actor, model string, in PublishVersionInput) (*domain.ModelVersion, []*domain.Artifact, error) {
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
	if err := s.store.CreateVersion(ctx, v); err != nil {
		return nil, nil, err
	}
	arts := make([]*domain.Artifact, 0, len(in.Artifacts))
	for _, ai := range in.Artifacts {
		a, err := s.registerArtifact(ctx, v.ID, ai)
		if err != nil {
			return nil, nil, err
		}
		arts = append(arts, a)
	}
	s.audit(ctx, actor, "version.create", "model_version", v.ID, "published "+m.Name+"@"+v.Name, nil)
	s.cache.InvalidateModel(m.Name)
	s.events.Publish(domain.Event{Type: "version.created", Model: m.Name, Version: v.Name})
	return v, arts, nil
}

func (s *Service) registerArtifact(ctx context.Context, versionID string, ai ArtifactInput) (*domain.Artifact, error) {
	if ai.Kind == "" {
		ai.Kind = domain.KindModel
	}
	if !domain.ValidName(ai.Name) {
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
		StorageBackend: ai.StorageBackend, SizeBytes: ai.SizeBytes, Digest: ai.Digest,
		MediaType: ai.MediaType, ModelFormat: ai.ModelFormat, ServiceAccount: ai.ServiceAccount,
		CreatedAt: now, UpdatedAt: now,
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
func (s *Service) Transition(ctx context.Context, actor, model, version string, to domain.Stage, reason string) (*domain.ModelVersion, error) {
	if !domain.ValidStage(to) {
		return nil, domain.Invalid("unknown stage '" + string(to) + "'")
	}
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if v.Stage == to {
		return v, nil
	}
	if !domain.CanTransition(v.Stage, to) {
		return nil, domain.Precondition(
			"illegal stage transition "+string(v.Stage)+"→"+string(to),
			map[string]any{"allowedTargets": domain.AllowedTargets(v.Stage)})
	}
	var demoteID string
	if domain.IsSingleton(to) {
		if inc, err := s.store.Resolve(ctx, model, domain.Selector{Stage: to}); err == nil && inc.ID != v.ID {
			demoteID = inc.ID
		}
	}
	if err := s.store.SetStage(ctx, v.ID, to, demoteID); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(map[string]string{"from": string(v.Stage), "to": string(to), "reason": reason})
	s.audit(ctx, actor, "version.stage_changed", "model_version", v.ID, model+"@"+version+" → "+string(to), data)
	s.cache.InvalidateModel(model)
	s.events.Publish(domain.Event{Type: "version.stage_changed", Model: model, Version: version, Data: map[string]any{"to": to}})
	v.Stage = to
	return v, nil
}

func (s *Service) audit(ctx context.Context, actor, action, subjType, subjID, summary string, data json.RawMessage) {
	_ = s.store.AppendAudit(ctx, &domain.AuditEvent{
		ID: domain.NewID(), At: domain.NowMillis(), Actor: actor, Action: action,
		SubjectType: subjType, SubjectID: subjID, Summary: summary, Data: data,
	})
}
