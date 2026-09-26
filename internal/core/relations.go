package core

import (
	"context"
	"encoding/json"

	"github.com/proseria-research/lineage/internal/domain"
)

// Lineage edges, deployment records, and the audit feed (§03.8, §07). Graph traversal
// queries (ancestry / impact) are M7; this is the CRUD surface.

// ---- Lineage ----

// LineageInput records a typed edge from a version to another version or an external URI.
type LineageInput struct {
	Relation domain.LineageRelation `json:"relation"`
	To       struct {
		Version string `json:"version"` // another version of the same model
		URI     string `json:"uri"`     // external artifact/dataset reference
	} `json:"to"`
	// Properties records how the relation came about, e.g.
	// {"method":"quantize","from_dtype":"fp16"} on a derived_from edge. This is what lets a
	// diff verdict be corroborated by declared intent without growing the relation enum
	// (§11.3.6) — the hashes can prove the shape is unchanged, but not why.
	Properties json.RawMessage `json:"properties"`
}

var validRelations = map[domain.LineageRelation]bool{
	domain.RelDerivedFrom: true, domain.RelTrainedOn: true,
	domain.RelProducedBy: true, domain.RelDeployedAs: true,
}

func (s *Service) AddLineage(ctx context.Context, actor, model, version string, in LineageInput) (*domain.LineageEdge, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if !validRelations[in.Relation] {
		return nil, domain.Invalid("unknown lineage relation '" + string(in.Relation) + "'")
	}
	if (in.To.Version == "") == (in.To.URI == "") {
		return nil, domain.Invalid("lineage 'to' must set exactly one of version or uri")
	}
	e := &domain.LineageEdge{
		ID: domain.NewID(), SrcType: "model_version", SrcID: v.ID,
		Relation: in.Relation, Properties: in.Properties, CreatedAt: domain.NowMillis(),
	}
	if in.To.Version != "" {
		target, err := s.store.GetVersion(ctx, model, in.To.Version)
		if err != nil {
			return nil, err
		}
		e.DstType, e.DstID = "model_version", target.ID
	} else {
		e.DstRef = in.To.URI
	}
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.AddLineageEdge(ctx, e); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "lineage.add", "model_version", v.ID, model+"@"+version+" "+string(in.Relation), nil)
	}); err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Service) ListLineage(ctx context.Context, model, version string) ([]*domain.LineageEdge, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	return s.store.ListLineage(ctx, v.ID)
}

func (s *Service) DeleteLineage(ctx context.Context, actor, model, version, edgeID string) error {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return err
	}
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.DeleteLineageEdge(ctx, edgeID, v.ID); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "lineage.delete", "model_version", v.ID, "removed lineage edge "+edgeID, nil)
	}); err != nil {
		return err
	}
	return nil
}

// ---- Deployments ----

// DeploymentInput describes a serving record. Lineage does not orchestrate serving; these
// are informational (§03.8).
type DeploymentInput struct {
	Environment string                  `json:"environment"`
	EndpointURI string                  `json:"endpointUri"`
	Status      domain.DeploymentStatus `json:"status"`
	ExternalRef string                  `json:"externalRef"`
}

func (s *Service) CreateDeployment(ctx context.Context, actor, model, version string, in DeploymentInput) (*domain.Deployment, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if in.Environment == "" {
		return nil, domain.Invalid("deployment requires an environment")
	}
	if in.Status == "" {
		in.Status = domain.DeployActive
	}
	now := domain.NowMillis()
	d := &domain.Deployment{
		ID: domain.NewID(), VersionID: v.ID, Environment: in.Environment,
		EndpointURI: in.EndpointURI, Status: in.Status, ExternalRef: in.ExternalRef,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.CreateDeployment(ctx, d); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "deployment.create", "deployment", d.ID, "deployed "+model+"@"+version+" to "+in.Environment, nil)
	}); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Service) ListDeployments(ctx context.Context, model, version string) ([]*domain.Deployment, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	return s.store.ListDeployments(ctx, v.ID)
}

func (s *Service) GetDeployment(ctx context.Context, id string) (*domain.Deployment, error) {
	return s.store.GetDeployment(ctx, id)
}

// DeploymentPatch updates a deployment's mutable fields.
type DeploymentPatch struct {
	EndpointURI *string                  `json:"endpointUri"`
	Status      *domain.DeploymentStatus `json:"status"`
	ExternalRef *string                  `json:"externalRef"`
}

func (s *Service) PatchDeployment(ctx context.Context, actor, id string, in DeploymentPatch) (*domain.Deployment, error) {
	d, err := s.store.GetDeployment(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.EndpointURI != nil {
		d.EndpointURI = *in.EndpointURI
	}
	if in.Status != nil {
		d.Status = *in.Status
	}
	if in.ExternalRef != nil {
		d.ExternalRef = *in.ExternalRef
	}
	d.UpdatedAt = domain.NowMillis()
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.UpdateDeployment(ctx, d); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "deployment.update", "deployment", d.ID, "updated deployment "+d.ID, nil)
	}); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Service) DeleteDeployment(ctx context.Context, actor, id string) error {
	if err := s.store.InTx(ctx, func(tx domain.MetadataStore) error {
		if err := tx.DeleteDeployment(ctx, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "deployment.delete", "deployment", id, "deleted deployment "+id, nil)
	}); err != nil {
		return err
	}
	return nil
}

// ---- Audit feed ----

// ListModelAudit returns the audit trail for a model (§03.2). ListAudit is the global feed.
func (s *Service) ListModelAudit(ctx context.Context, model string, o domain.ListOptions) ([]*domain.AuditEvent, string, error) {
	m, err := s.store.GetModel(ctx, model)
	if err != nil {
		return nil, "", err
	}
	return s.store.ListAudit(ctx, "model", m.ID, o)
}

func (s *Service) ListAudit(ctx context.Context, subjectType, subjectID string, o domain.ListOptions) ([]*domain.AuditEvent, string, error) {
	return s.store.ListAudit(ctx, subjectType, subjectID, o)
}
