package core

import (
	"context"
	"encoding/json"

	"github.com/proseria-research/lineage/internal/domain"
)

// This file completes the resource CRUD half of the Model API (§03): PATCH/archive/DELETE
// for models and versions, and GET/PATCH/DELETE for artifacts, with the §03.4/§03.6 guards.

// ---- Models ----

// PatchModelInput is a partial update of a model's mutable fields (§03.4). A nil pointer /
// nil map means "leave unchanged"; State lets a PATCH reverse an :archive.
type PatchModelInput struct {
	Description      *string            `json:"description"`
	Owner            *string            `json:"owner"`
	Labels           map[string]string  `json:"labels"`
	CustomProperties json.RawMessage    `json:"customProperties"`
	State            *domain.ModelState `json:"state"`
}

func (s *Service) PatchModel(ctx context.Context, actor, name string, in PatchModelInput) (*domain.Model, error) {
	m, err := s.store.GetModel(ctx, name)
	if err != nil {
		return nil, err
	}
	if in.Description != nil {
		m.Description = *in.Description
	}
	if in.Owner != nil {
		m.Owner = *in.Owner
	}
	if in.Labels != nil {
		m.Labels = in.Labels
	}
	if in.CustomProperties != nil {
		m.CustomProperties = in.CustomProperties
	}
	if in.State != nil {
		if *in.State != domain.StateActive && *in.State != domain.StateArchived {
			return nil, domain.Invalid("invalid state '" + string(*in.State) + "'")
		}
		m.State = *in.State
	}
	m.UpdatedAt = domain.NowMillis()
	if err := s.store.UpdateModel(ctx, m); err != nil {
		return nil, err
	}
	s.audit(ctx, actor, "model.update", "model", m.ID, "updated model "+m.Name, nil)
	s.events.Publish(domain.Event{Type: "model.updated", Model: m.Name})
	return m, nil
}

// ArchiveModel soft-archives a model (reversible via PatchModel, §03.4).
func (s *Service) ArchiveModel(ctx context.Context, actor, name string) (*domain.Model, error) {
	archived := domain.StateArchived
	return s.PatchModel(ctx, actor, name, PatchModelInput{State: &archived})
}

// DeleteModel hard-deletes a model and cascades (§03.4). Guarded: refuses when a version is
// in production unless force is set.
func (s *Service) DeleteModel(ctx context.Context, actor, name string, force bool) error {
	m, err := s.store.GetModel(ctx, name)
	if err != nil {
		return err
	}
	// Evidence first (§19.3.1). Deliberately ahead of the force-able guard below, and
	// deliberately not force-able itself — see guardDelete.
	if err := s.guardDelete(ctx, domain.SubjectModel, m.ID); err != nil {
		return err
	}
	if !force {
		n, err := s.store.CountVersionsInStage(ctx, m.ID, domain.StageProduction)
		if err != nil {
			return err
		}
		if n > 0 {
			return domain.Precondition("model '"+name+"' has a production version; delete blocked",
				map[string]any{"productionVersions": n, "hint": "retry with ?force=true"})
		}
	}
	if err := s.store.DeleteModel(ctx, m.ID); err != nil {
		return err
	}
	s.audit(ctx, actor, "model.delete", "model", m.ID, "deleted model "+m.Name, nil)
	s.events.Publish(domain.Event{Type: "model.deleted", Model: m.Name})
	return nil
}

// ---- Versions ----

// PatchVersionInput is a partial update of a version's mutable metadata (§03.5). Stage is
// NOT here — it changes only via :transition (§03.7).
type PatchVersionInput struct {
	Description      *string           `json:"description"`
	Author           *string           `json:"author"`
	Labels           map[string]string `json:"labels"`
	CustomProperties json.RawMessage   `json:"customProperties"`
}

func (s *Service) PatchVersion(ctx context.Context, actor, model, version string, in PatchVersionInput) (*domain.ModelVersion, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if in.Description != nil {
		v.Description = *in.Description
	}
	if in.Author != nil {
		v.Author = *in.Author
	}
	if in.Labels != nil {
		v.Labels = in.Labels
	}
	if in.CustomProperties != nil {
		v.CustomProperties = in.CustomProperties
	}
	v.UpdatedAt = domain.NowMillis()
	if err := s.store.UpdateVersion(ctx, v); err != nil {
		return nil, err
	}
	s.audit(ctx, actor, "version.update", "model_version", v.ID, "updated "+model+"@"+version, nil)
	s.events.Publish(domain.Event{Type: "version.updated", Model: model, Version: version})
	return v, nil
}

// DeleteVersion hard-deletes a version and cascades (§03.5). Guarded against deleting a live
// production version unless force is set.
func (s *Service) DeleteVersion(ctx context.Context, actor, model, version string, force bool) error {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return err
	}
	if err := s.guardDelete(ctx, domain.SubjectVersion, v.ID); err != nil {
		return err
	}
	if v.Stage == domain.StageProduction && !force {
		return domain.Precondition("version '"+model+"@"+version+"' is in production; delete blocked",
			map[string]any{"hint": "retry with ?force=true"})
	}
	if err := s.store.DeleteVersion(ctx, v.ID); err != nil {
		return err
	}
	s.audit(ctx, actor, "version.delete", "model_version", v.ID, "deleted "+model+"@"+version, nil)
	s.events.Publish(domain.Event{Type: "version.deleted", Model: model, Version: version})
	return nil
}

// ---- Artifacts ----

func (s *Service) ListArtifacts(ctx context.Context, model, version string) ([]*domain.Artifact, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	return s.store.ListArtifacts(ctx, v.ID)
}

func (s *Service) GetArtifact(ctx context.Context, model, version, name string) (*domain.Artifact, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	return s.store.GetArtifact(ctx, v.ID, name)
}

// PatchArtifactInput updates metadata only. Content fields (uri/digest/sizeBytes) are
// immutable once set (§03.6): supplying a *changed* value is rejected.
type PatchArtifactInput struct {
	MediaType        *string         `json:"mediaType"`
	ServiceAccount   *string         `json:"serviceAccount"`
	CustomProperties json.RawMessage `json:"customProperties"`
	URI              *string         `json:"uri"`
	Digest           *string         `json:"digest"`
	SizeBytes        *int64          `json:"sizeBytes"`
}

func (s *Service) PatchArtifact(ctx context.Context, actor, model, version, name string, in PatchArtifactInput) (*domain.Artifact, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	a, err := s.store.GetArtifact(ctx, v.ID, name)
	if err != nil {
		return nil, err
	}
	if (in.URI != nil && *in.URI != a.URI) ||
		(in.Digest != nil && *in.Digest != a.Digest) ||
		(in.SizeBytes != nil && *in.SizeBytes != a.SizeBytes) {
		return nil, domain.Precondition("artifact content (uri/digest/sizeBytes) is immutable; new content ⇒ new version", nil)
	}
	if in.MediaType != nil {
		a.MediaType = *in.MediaType
	}
	if in.ServiceAccount != nil {
		a.ServiceAccount = *in.ServiceAccount
	}
	if in.CustomProperties != nil {
		a.CustomProperties = in.CustomProperties
	}
	a.UpdatedAt = domain.NowMillis()
	if err := s.store.UpdateArtifact(ctx, a); err != nil {
		return nil, err
	}
	s.audit(ctx, actor, "artifact.update", "artifact", a.ID, "updated "+model+"@"+version+"/"+name, nil)
	s.events.Publish(domain.Event{Type: "artifact.updated", Model: model, Version: version})
	return a, nil
}

// DeleteArtifact removes the metadata row; backend bytes are a storage-policy concern (§05.8).
func (s *Service) DeleteArtifact(ctx context.Context, actor, model, version, name string) error {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return err
	}
	a, err := s.store.GetArtifact(ctx, v.ID, name)
	if err != nil {
		return err
	}
	if err := s.store.DeleteArtifact(ctx, a.ID); err != nil {
		return err
	}
	s.audit(ctx, actor, "artifact.delete", "artifact", a.ID, "deleted "+model+"@"+version+"/"+name, nil)
	s.events.Publish(domain.Event{Type: "artifact.deleted", Model: model, Version: version})
	return nil
}
