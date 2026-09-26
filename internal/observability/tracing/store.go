package tracing

import (
	"context"

	"github.com/proseria-research/lineage/internal/domain"
)

// Store wraps a MetadataStore so every call becomes a child span of whatever the core is
// doing. Decorating the *port* rather than a dialect means SQLite, Postgres, and the memory
// store are all instrumented once, and the store adapters stay free of telemetry (§01).
//
// A nil or no-op tracer returns next unchanged, so the decorator is not in the call path at
// all when tracing is off.
func Store(next domain.MetadataStore, t domain.Tracer) domain.MetadataStore {
	if next == nil || t == nil {
		return next
	}
	if _, off := t.(domain.NopTracer); off {
		return next
	}
	if e, ok := t.(interface{ Enabled() bool }); ok && !e.Enabled() {
		return next
	}
	return &tracedStore{next: next, t: t}
}

type tracedStore struct {
	next domain.MetadataStore
	t    domain.Tracer
}

var _ domain.MetadataStore = (*tracedStore)(nil)

// The three helpers below cover every arity the port uses. They exist so each method stays a
// single readable line instead of six lines of identical span bookkeeping.

func do0(ctx context.Context, t domain.Tracer, name string, fn func(context.Context) error) error {
	ctx, sp := t.Start(ctx, name)
	defer sp.End()
	err := fn(ctx)
	sp.RecordError(err)
	return err
}

func do1[T any](ctx context.Context, t domain.Tracer, name string, fn func(context.Context) (T, error)) (T, error) {
	ctx, sp := t.Start(ctx, name)
	defer sp.End()
	v, err := fn(ctx)
	sp.RecordError(err)
	return v, err
}

func do2[A, B any](ctx context.Context, t domain.Tracer, name string, fn func(context.Context) (A, B, error)) (A, B, error) {
	ctx, sp := t.Start(ctx, name)
	defer sp.End()
	a, b, err := fn(ctx)
	sp.RecordError(err)
	return a, b, err
}

// ---- Unit of work ----

// InTx is one span around the transaction, and the tx handed to fn is decorated too, so the
// statements inside it are its children rather than vanishing from the trace.
func (s *tracedStore) InTx(ctx context.Context, fn func(tx domain.MetadataStore) error) error {
	return do0(ctx, s.t, "store.InTx", func(c context.Context) error {
		return s.next.InTx(c, func(tx domain.MetadataStore) error { return fn(&tracedStore{next: tx, t: s.t}) })
	})
}

// ---- Models ----

func (s *tracedStore) CreateModel(ctx context.Context, m *domain.Model) error {
	return do0(ctx, s.t, "store.CreateModel", func(c context.Context) error { return s.next.CreateModel(c, m) })
}

func (s *tracedStore) GetModel(ctx context.Context, nameOrID string) (*domain.Model, error) {
	return do1(ctx, s.t, "store.GetModel", func(c context.Context) (*domain.Model, error) { return s.next.GetModel(c, nameOrID) })
}

func (s *tracedStore) ListModels(ctx context.Context, o domain.ListOptions) ([]*domain.Model, string, error) {
	return do2(ctx, s.t, "store.ListModels", func(c context.Context) ([]*domain.Model, string, error) { return s.next.ListModels(c, o) })
}

func (s *tracedStore) UpdateModel(ctx context.Context, m *domain.Model) error {
	return do0(ctx, s.t, "store.UpdateModel", func(c context.Context) error { return s.next.UpdateModel(c, m) })
}

func (s *tracedStore) DeleteModel(ctx context.Context, id string) error {
	return do0(ctx, s.t, "store.DeleteModel", func(c context.Context) error { return s.next.DeleteModel(c, id) })
}

// ---- Versions ----

func (s *tracedStore) CreateVersion(ctx context.Context, v *domain.ModelVersion) error {
	return do0(ctx, s.t, "store.CreateVersion", func(c context.Context) error { return s.next.CreateVersion(c, v) })
}

func (s *tracedStore) GetVersion(ctx context.Context, model, version string) (*domain.ModelVersion, error) {
	return do1(ctx, s.t, "store.GetVersion", func(c context.Context) (*domain.ModelVersion, error) { return s.next.GetVersion(c, model, version) })
}

func (s *tracedStore) GetVersionByID(ctx context.Context, id string) (*domain.ModelVersion, error) {
	return do1(ctx, s.t, "store.GetVersionByID", func(c context.Context) (*domain.ModelVersion, error) { return s.next.GetVersionByID(c, id) })
}

func (s *tracedStore) LockVersionForArtifacts(ctx context.Context, id string) (*domain.ModelVersion, error) {
	return do1(ctx, s.t, "store.LockVersionForArtifacts", func(c context.Context) (*domain.ModelVersion, error) {
		return s.next.LockVersionForArtifacts(c, id)
	})
}

func (s *tracedStore) ListVersions(ctx context.Context, model string, o domain.ListOptions) ([]*domain.ModelVersion, string, error) {
	return do2(ctx, s.t, "store.ListVersions", func(c context.Context) ([]*domain.ModelVersion, string, error) {
		return s.next.ListVersions(c, model, o)
	})
}

func (s *tracedStore) UpdateVersion(ctx context.Context, v *domain.ModelVersion) error {
	return do0(ctx, s.t, "store.UpdateVersion", func(c context.Context) error { return s.next.UpdateVersion(c, v) })
}

func (s *tracedStore) DeleteVersion(ctx context.Context, id string) error {
	return do0(ctx, s.t, "store.DeleteVersion", func(c context.Context) error { return s.next.DeleteVersion(c, id) })
}

func (s *tracedStore) CountVersionsInStage(ctx context.Context, modelID string, stage domain.Stage) (int, error) {
	return do1(ctx, s.t, "store.CountVersionsInStage", func(c context.Context) (int, error) { return s.next.CountVersionsInStage(c, modelID, stage) })
}

// SetStage carries the target stage: it is the one store call whose argument decides whether a
// singleton demotion happens, so it is worth seeing on the span.
func (s *tracedStore) SetStage(ctx context.Context, versionID string, to domain.Stage, singleton bool) error {
	ctx, sp := s.t.Start(ctx, "store.SetStage")
	defer sp.End()
	sp.SetString("lineage.stage", string(to))
	err := s.next.SetStage(ctx, versionID, to, singleton)
	sp.RecordError(err)
	return err
}

func (s *tracedStore) Resolve(ctx context.Context, model string, sel domain.Selector) (*domain.ModelVersion, error) {
	return do1(ctx, s.t, "store.Resolve", func(c context.Context) (*domain.ModelVersion, error) { return s.next.Resolve(c, model, sel) })
}

// ---- Artifacts ----

func (s *tracedStore) CreateArtifact(ctx context.Context, a *domain.Artifact) error {
	return do0(ctx, s.t, "store.CreateArtifact", func(c context.Context) error { return s.next.CreateArtifact(c, a) })
}

func (s *tracedStore) GetArtifact(ctx context.Context, versionID, name string) (*domain.Artifact, error) {
	return do1(ctx, s.t, "store.GetArtifact", func(c context.Context) (*domain.Artifact, error) { return s.next.GetArtifact(c, versionID, name) })
}

func (s *tracedStore) GetArtifactByID(ctx context.Context, id string) (*domain.Artifact, error) {
	return do1(ctx, s.t, "store.GetArtifactByID", func(c context.Context) (*domain.Artifact, error) { return s.next.GetArtifactByID(c, id) })
}

func (s *tracedStore) ListArtifacts(ctx context.Context, versionID string) ([]*domain.Artifact, error) {
	return do1(ctx, s.t, "store.ListArtifacts", func(c context.Context) ([]*domain.Artifact, error) { return s.next.ListArtifacts(c, versionID) })
}

func (s *tracedStore) UpdateArtifact(ctx context.Context, a *domain.Artifact) error {
	return do0(ctx, s.t, "store.UpdateArtifact", func(c context.Context) error { return s.next.UpdateArtifact(c, a) })
}

func (s *tracedStore) DeleteArtifact(ctx context.Context, id string) error {
	return do0(ctx, s.t, "store.DeleteArtifact", func(c context.Context) error { return s.next.DeleteArtifact(c, id) })
}

func (s *tracedStore) ArtifactRefsURI(ctx context.Context, uri string) (bool, error) {
	return do1(ctx, s.t, "store.ArtifactRefsURI", func(c context.Context) (bool, error) { return s.next.ArtifactRefsURI(c, uri) })
}

// ---- Lineage ----

func (s *tracedStore) AddLineageEdge(ctx context.Context, e *domain.LineageEdge) error {
	return do0(ctx, s.t, "store.AddLineageEdge", func(c context.Context) error { return s.next.AddLineageEdge(c, e) })
}

func (s *tracedStore) ListLineage(ctx context.Context, versionID string) ([]*domain.LineageEdge, error) {
	return do1(ctx, s.t, "store.ListLineage", func(c context.Context) ([]*domain.LineageEdge, error) { return s.next.ListLineage(c, versionID) })
}

func (s *tracedStore) DeleteLineageEdge(ctx context.Context, id, versionID string) error {
	return do0(ctx, s.t, "store.DeleteLineageEdge", func(c context.Context) error { return s.next.DeleteLineageEdge(c, id, versionID) })
}

// ---- Deployments ----

func (s *tracedStore) CreateDeployment(ctx context.Context, d *domain.Deployment) error {
	return do0(ctx, s.t, "store.CreateDeployment", func(c context.Context) error { return s.next.CreateDeployment(c, d) })
}

func (s *tracedStore) GetDeployment(ctx context.Context, id string) (*domain.Deployment, error) {
	return do1(ctx, s.t, "store.GetDeployment", func(c context.Context) (*domain.Deployment, error) { return s.next.GetDeployment(c, id) })
}

func (s *tracedStore) ListDeployments(ctx context.Context, versionID string) ([]*domain.Deployment, error) {
	return do1(ctx, s.t, "store.ListDeployments", func(c context.Context) ([]*domain.Deployment, error) { return s.next.ListDeployments(c, versionID) })
}

func (s *tracedStore) UpdateDeployment(ctx context.Context, d *domain.Deployment) error {
	return do0(ctx, s.t, "store.UpdateDeployment", func(c context.Context) error { return s.next.UpdateDeployment(c, d) })
}

func (s *tracedStore) DeleteDeployment(ctx context.Context, id string) error {
	return do0(ctx, s.t, "store.DeleteDeployment", func(c context.Context) error { return s.next.DeleteDeployment(c, id) })
}

// ---- Audit ----

func (s *tracedStore) AppendAudit(ctx context.Context, e *domain.AuditEvent) error {
	return do0(ctx, s.t, "store.AppendAudit", func(c context.Context) error { return s.next.AppendAudit(c, e) })
}

func (s *tracedStore) ListAudit(ctx context.Context, subjectType, subjectID string, o domain.ListOptions) ([]*domain.AuditEvent, string, error) {
	return do2(ctx, s.t, "store.ListAudit", func(c context.Context) ([]*domain.AuditEvent, string, error) {
		return s.next.ListAudit(c, subjectType, subjectID, o)
	})
}

// ---- Insights (§11) ----

func (s *tracedStore) GetInsight(ctx context.Context, versionID string) (*domain.VersionInsight, error) {
	return do1(ctx, s.t, "store.GetInsight", func(c context.Context) (*domain.VersionInsight, error) { return s.next.GetInsight(c, versionID) })
}

func (s *tracedStore) UpsertInsight(ctx context.Context, in *domain.VersionInsight) error {
	return do0(ctx, s.t, "store.UpsertInsight", func(c context.Context) error { return s.next.UpsertInsight(c, in) })
}

func (s *tracedStore) ReplaceLayerBlocks(ctx context.Context, versionID string, blocks []*domain.LayerBlock) error {
	return do0(ctx, s.t, "store.ReplaceLayerBlocks", func(c context.Context) error { return s.next.ReplaceLayerBlocks(c, versionID, blocks) })
}

func (s *tracedStore) ListLayerBlocks(ctx context.Context, versionID string) ([]*domain.LayerBlock, error) {
	return do1(ctx, s.t, "store.ListLayerBlocks", func(c context.Context) ([]*domain.LayerBlock, error) { return s.next.ListLayerBlocks(c, versionID) })
}

func (s *tracedStore) UpsertFootprint(ctx context.Context, f *domain.Footprint) error {
	return do0(ctx, s.t, "store.UpsertFootprint", func(c context.Context) error { return s.next.UpsertFootprint(c, f) })
}

func (s *tracedStore) ListFootprints(ctx context.Context, versionID string) ([]*domain.Footprint, error) {
	return do1(ctx, s.t, "store.ListFootprints", func(c context.Context) ([]*domain.Footprint, error) { return s.next.ListFootprints(c, versionID) })
}

func (s *tracedStore) CreateEvaluation(ctx context.Context, e *domain.Evaluation) error {
	return do0(ctx, s.t, "store.CreateEvaluation", func(c context.Context) error { return s.next.CreateEvaluation(c, e) })
}

func (s *tracedStore) ListEvaluations(ctx context.Context, versionID string) ([]*domain.Evaluation, error) {
	return do1(ctx, s.t, "store.ListEvaluations", func(c context.Context) ([]*domain.Evaluation, error) { return s.next.ListEvaluations(c, versionID) })
}

// ---- Risk classification (§16) ----

func (s *tracedStore) PutClassification(ctx context.Context, c *domain.RiskClassification) error {
	return do0(ctx, s.t, "store.PutClassification", func(cc context.Context) error { return s.next.PutClassification(cc, c) })
}

func (s *tracedStore) GetClassification(ctx context.Context, modelID string, regime domain.Regime) (*domain.RiskClassification, error) {
	return do1(ctx, s.t, "store.GetClassification", func(c context.Context) (*domain.RiskClassification, error) {
		return s.next.GetClassification(c, modelID, regime)
	})
}

func (s *tracedStore) ListClassifications(ctx context.Context, modelID string) ([]*domain.RiskClassification, error) {
	return do1(ctx, s.t, "store.ListClassifications", func(c context.Context) ([]*domain.RiskClassification, error) {
		return s.next.ListClassifications(c, modelID)
	})
}

func (s *tracedStore) DriftFactsFor(ctx context.Context, modelID string) (domain.DriftFacts, error) {
	return do1(ctx, s.t, "store.DriftFactsFor", func(c context.Context) (domain.DriftFacts, error) {
		return s.next.DriftFactsFor(c, modelID)
	})
}

func (s *tracedStore) ListInventory(ctx context.Context, o domain.ListOptions, f domain.ClassificationFilter) ([]*domain.ModelInventoryRow, error) {
	return do1(ctx, s.t, "store.ListInventory", func(c context.Context) ([]*domain.ModelInventoryRow, error) {
		return s.next.ListInventory(c, o, f)
	})
}

// ---- Modification review (§17) ----

func (s *tracedStore) CreateReview(ctx context.Context, r *domain.ModificationReview) error {
	return do0(ctx, s.t, "store.CreateReview", func(c context.Context) error { return s.next.CreateReview(c, r) })
}

func (s *tracedStore) ListReviews(ctx context.Context, versionID string) ([]*domain.ModificationReview, error) {
	return do1(ctx, s.t, "store.ListReviews", func(c context.Context) ([]*domain.ModificationReview, error) {
		return s.next.ListReviews(c, versionID)
	})
}

func (s *tracedStore) ListDerivations(ctx context.Context, regime domain.Regime, modelID string) ([]*domain.DerivationRow, error) {
	return do1(ctx, s.t, "store.ListDerivations", func(c context.Context) ([]*domain.DerivationRow, error) {
		return s.next.ListDerivations(c, regime, modelID)
	})
}

// ---- Model risk management (§20) ----

func (s *tracedStore) CreateValidation(ctx context.Context, v *domain.Validation) error {
	return do0(ctx, s.t, "store.CreateValidation", func(c context.Context) error { return s.next.CreateValidation(c, v) })
}

func (s *tracedStore) ListValidations(ctx context.Context, versionID string) ([]*domain.Validation, error) {
	return do1(ctx, s.t, "store.ListValidations", func(c context.Context) ([]*domain.Validation, error) {
		return s.next.ListValidations(c, versionID)
	})
}

func (s *tracedStore) ClearValidationConditions(ctx context.Context, versionID, id string, at int64) error {
	return do0(ctx, s.t, "store.ClearValidationConditions", func(c context.Context) error {
		return s.next.ClearValidationConditions(c, versionID, id, at)
	})
}

func (s *tracedStore) ListMRMInventory(ctx context.Context, o domain.ListOptions, f domain.MRMFilter) ([]*domain.MRMInventoryRow, error) {
	return do1(ctx, s.t, "store.ListMRMInventory", func(c context.Context) ([]*domain.MRMInventoryRow, error) {
		return s.next.ListMRMInventory(c, o, f)
	})
}

// ---- Change control plans (§22) ----

func (s *tracedStore) CreateChangePlan(ctx context.Context, p *domain.ChangePlan, supersedes string) error {
	return do0(ctx, s.t, "store.CreateChangePlan", func(c context.Context) error {
		return s.next.CreateChangePlan(c, p, supersedes)
	})
}

func (s *tracedStore) ListChangePlans(ctx context.Context, modelID string) ([]*domain.ChangePlan, error) {
	return do1(ctx, s.t, "store.ListChangePlans", func(c context.Context) ([]*domain.ChangePlan, error) {
		return s.next.ListChangePlans(c, modelID)
	})
}

func (s *tracedStore) ListPlanDerivations(ctx context.Context, modelID, versionID string) ([]*domain.PlanDerivationRow, error) {
	return do1(ctx, s.t, "store.ListPlanDerivations", func(c context.Context) ([]*domain.PlanDerivationRow, error) {
		return s.next.ListPlanDerivations(c, modelID, versionID)
	})
}

// ---- Retention (§19.3) ----

func (s *tracedStore) SetHold(ctx context.Context, subjectType, subjectID string, h *domain.Hold) error {
	return do0(ctx, s.t, "store.SetHold", func(c context.Context) error {
		return s.next.SetHold(c, subjectType, subjectID, h)
	})
}

func (s *tracedStore) DeleteGuardFor(ctx context.Context, subjectType, subjectID string) (domain.DeleteGuard, error) {
	return do1(ctx, s.t, "store.DeleteGuardFor", func(c context.Context) (domain.DeleteGuard, error) {
		return s.next.DeleteGuardFor(c, subjectType, subjectID)
	})
}

// ---- Attestation (§19.5) ----

func (s *tracedStore) SealableEpochs(ctx context.Context, notAfter int64, limit int) ([]int64, error) {
	return do1(ctx, s.t, "store.SealableEpochs", func(c context.Context) ([]int64, error) {
		return s.next.SealableEpochs(c, notAfter, limit)
	})
}

func (s *tracedStore) AuditEventsInEpoch(ctx context.Context, epoch int64) ([]*domain.AuditEvent, error) {
	return do1(ctx, s.t, "store.AuditEventsInEpoch", func(c context.Context) ([]*domain.AuditEvent, error) {
		return s.next.AuditEventsInEpoch(c, epoch)
	})
}

func (s *tracedStore) AppendEpoch(ctx context.Context, e *domain.AuditEpoch) error {
	return do0(ctx, s.t, "store.AppendEpoch", func(c context.Context) error { return s.next.AppendEpoch(c, e) })
}

func (s *tracedStore) LatestSealedEpoch(ctx context.Context) (*domain.AuditEpoch, error) {
	return do1(ctx, s.t, "store.LatestSealedEpoch", func(c context.Context) (*domain.AuditEpoch, error) {
		return s.next.LatestSealedEpoch(c)
	})
}

func (s *tracedStore) ListEpochs(ctx context.Context, from, to int64) ([]*domain.AuditEpoch, error) {
	return do1(ctx, s.t, "store.ListEpochs", func(c context.Context) ([]*domain.AuditEpoch, error) {
		return s.next.ListEpochs(c, from, to)
	})
}

func (s *tracedStore) GetAuditEvent(ctx context.Context, id string) (*domain.AuditEvent, error) {
	return do1(ctx, s.t, "store.GetAuditEvent", func(c context.Context) (*domain.AuditEvent, error) {
		return s.next.GetAuditEvent(c, id)
	})
}

func (s *tracedStore) FirstAttestedEpoch(ctx context.Context) (int64, bool, error) {
	ctx, sp := s.t.Start(ctx, "store.FirstAttestedEpoch")
	defer sp.End()
	v, ok, err := s.next.FirstAttestedEpoch(ctx)
	sp.RecordError(err)
	return v, ok, err
}
