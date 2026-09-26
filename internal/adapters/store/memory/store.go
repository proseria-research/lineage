// Package memstore is an in-memory MetadataStore for dev/tests. The production
// adapters are per-dialect sqlite/postgres behind the same port (§02.7); this exists so
// the scaffold runs end-to-end with zero dependencies.
package memstore

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/proseria-research/lineage/internal/domain"
)

// Store is one type in two roles, like sqlstore's. Opened, mu is a real lock over *state.
// Inside InTx it is a transaction's view: state is a private working copy and mu a no-op,
// because the outer InTx already holds the real lock for the whole unit of work.
type Store struct {
	mu   rwLocker
	inTx bool
	*state
}

// state is everything the store holds. It is separate from Store so a unit of work can clone
// it, run against the clone, and swap it in on success — rollback is simply not swapping.
type state struct {
	models      map[string]*domain.Model        // id -> model
	modelByNm   map[string]string               // name -> id
	versions    map[string]*domain.ModelVersion // id -> version
	artifacts   map[string]*domain.Artifact     // id -> artifact
	lineage     map[string]*domain.LineageEdge  // id -> edge
	deployments map[string]*domain.Deployment   // id -> deployment
	audit       []*domain.AuditEvent

	// Insights (§11), all keyed by version id.
	insights    map[string]*domain.VersionInsight
	layers      map[string][]*domain.LayerBlock
	footprints  map[string][]*domain.Footprint
	evaluations map[string][]*domain.Evaluation

	// Classifications (§16.7.1): model id -> regime -> row. Nested rather than keyed on a
	// composite string so the per-regime isolation the schema's PK gives us is structural
	// here too — writing one regime cannot reach another's row.
	classifications map[string]map[domain.Regime]*domain.RiskClassification

	// Modification reviews (§17.5.1). A flat append-only slice rather than a map keyed by
	// (version, edge): the table has no unique key to be a map key, because a re-review is a
	// new row and the whole history is kept.
	reviews []*domain.ModificationReview

	// Model-risk validations (§20.8.2). Append-only for the same reason as reviews.
	validations []*domain.Validation

	// Change control plans (§22.6.1). Append-only; the one later write is closing a
	// superseded plan's window.
	changePlans []*domain.ChangePlan

	// Sealed audit epochs (§19.6.1), keyed by window index. Append-only: AppendEpoch
	// refuses an index already present rather than replacing it.
	epochs map[int64]*domain.AuditEpoch
}

func New() *Store {
	return &Store{mu: &sync.RWMutex{}, state: &state{
		models:      map[string]*domain.Model{},
		modelByNm:   map[string]string{},
		versions:    map[string]*domain.ModelVersion{},
		artifacts:   map[string]*domain.Artifact{},
		lineage:     map[string]*domain.LineageEdge{},
		deployments: map[string]*domain.Deployment{},
		insights:    map[string]*domain.VersionInsight{},
		layers:      map[string][]*domain.LayerBlock{},
		footprints:  map[string][]*domain.Footprint{},
		evaluations: map[string][]*domain.Evaluation{},

		classifications: map[string]map[domain.Regime]*domain.RiskClassification{},
		epochs:          map[int64]*domain.AuditEpoch{},
	}}
}

var _ domain.MetadataStore = (*Store)(nil)

// ---- Models ----

func (s *Store) CreateModel(_ context.Context, m *domain.Model) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.modelByNm[m.Name]; ok {
		return domain.Exists("model '" + m.Name + "' already exists")
	}
	s.models[m.ID] = deepCopy(m)
	s.modelByNm[m.Name] = m.ID
	return nil
}

func (s *Store) GetModel(_ context.Context, nameOrID string) (*domain.Model, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.lookupModel(nameOrID)
	return deepCopy(m), err
}

// lookupModel returns the *live* row. It is internal-only: every exported read copies before
// handing the result out (see clone.go).
func (s *Store) lookupModel(nameOrID string) (*domain.Model, error) {
	if id, ok := s.modelByNm[nameOrID]; ok {
		return s.models[id], nil
	}
	if m, ok := s.models[nameOrID]; ok {
		return m, nil
	}
	return nil, domain.NotFound("model '" + nameOrID + "' not found")
}

func (s *Store) ListModels(_ context.Context, o domain.ListOptions) ([]*domain.Model, string, error) {
	if len(o.CustomProps) > 0 {
		return nil, "", domain.Invalid("custom-property (cp.*) filtering requires the postgres engine (§02.7)")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.matchModels(o)
	items, next := domain.Page(out, func(m *domain.Model) (int64, string) { return m.CreatedAt, m.ID }, o.PageToken, o.PageSize)
	return deepCopyAll(items), next, nil
}

// matchModels is ListModels' filtering without its paging, oldest first. The inventory
// queries page only after applying a computed state, so they need every match, not the first
// page of them. The caller holds the lock.
func (s *Store) matchModels(o domain.ListOptions) []*domain.Model {
	var out []*domain.Model
	for _, m := range s.models {
		if o.Q != "" && !strings.Contains(m.Name, o.Q) {
			continue
		}
		if st := o.Filters["state"]; st != "" && string(m.State) != st {
			continue
		}
		if !hasLabels(m.Labels, o.Labels) {
			continue
		}
		out = append(out, m)
	}
	sortByCreated(out, func(m *domain.Model) (int64, string) { return m.CreatedAt, m.ID })
	return out
}

// allModels is every model matching o, unpaged, as copies. Used by the inventory reads.
func (s *Store) allModels(o domain.ListOptions) ([]*domain.Model, error) {
	if len(o.CustomProps) > 0 {
		return nil, domain.Invalid("custom-property (cp.*) filtering requires the postgres engine (§02.7)")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return deepCopyAll(s.matchModels(o)), nil
}

func (s *Store) UpdateModel(_ context.Context, m *domain.Model) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.models[m.ID]
	if !ok {
		return domain.NotFound("model not found")
	}
	stored := deepCopy(m)
	// The hold is not a writable field: only SetHold moves it (§19.3.1). This adapter swaps
	// the whole entity, so without carrying it over, any metadata PATCH built from a copy
	// taken before the hold — or one that simply left the field nil — would release it. The
	// SQL adapters get this from the column list; here it has to be explicit.
	stored.LegalHold = cur.LegalHold
	s.models[m.ID] = stored
	return nil
}

func (s *Store) DeleteModel(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.models[id]
	if !ok {
		return domain.NotFound("model not found")
	}
	// Cascade to versions/artifacts/deployments/lineage (§02.5), and to classifications,
	// whose FK is ON DELETE CASCADE from model (§16.7.1).
	for vid, v := range s.versions {
		if v.ModelID == id {
			s.deleteVersionLocked(vid)
		}
	}
	delete(s.classifications, id)
	kept := s.changePlans[:0]
	for _, p := range s.changePlans {
		if p.ModelID != id {
			kept = append(kept, p)
		}
	}
	s.changePlans = kept
	delete(s.models, id)
	delete(s.modelByNm, m.Name)
	return nil
}

// ---- Versions ----

func (s *Store) CreateVersion(_ context.Context, v *domain.ModelVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.versions {
		if ex.ModelID == v.ModelID && ex.Name == v.Name {
			return domain.Exists("version '" + v.Name + "' already exists")
		}
	}
	// Model is a derived (denormalized) name; sqlstore fills it via JOIN, we fill it here so
	// reads are consistent across adapters.
	if v.Model == "" {
		if m, ok := s.models[v.ModelID]; ok {
			v.Model = m.Name
		}
	}
	// A version enters its first stage when it is created (§20.8.3), as in sqlstore.
	if v.StageChangedAt == 0 {
		v.StageChangedAt = v.CreatedAt
	}
	s.versions[v.ID] = deepCopy(v)
	return nil
}

func (s *Store) GetVersion(_ context.Context, model, version string) (*domain.ModelVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.lookupModel(model)
	if err != nil {
		return nil, err
	}
	for _, v := range s.versions {
		if v.ModelID == m.ID && v.Name == version {
			return deepCopy(v), nil
		}
	}
	return nil, domain.NotFound("version '" + version + "' not found")
}

// LockVersionForArtifacts is a plain read: InTx already holds the store's write lock for the
// whole unit, so nothing can promote the version underneath it.
func (s *Store) LockVersionForArtifacts(ctx context.Context, id string) (*domain.ModelVersion, error) {
	return s.GetVersionByID(ctx, id)
}

func (s *Store) GetVersionByID(_ context.Context, id string) (*domain.ModelVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.versions[id]; ok {
		return deepCopy(v), nil
	}
	return nil, domain.NotFound("version '" + id + "' not found")
}

func (s *Store) ListVersions(_ context.Context, model string, o domain.ListOptions) ([]*domain.ModelVersion, string, error) {
	if len(o.CustomProps) > 0 {
		return nil, "", domain.Invalid("custom-property (cp.*) filtering requires the postgres engine (§02.7)")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.lookupModel(model)
	if err != nil {
		return nil, "", err
	}
	var out []*domain.ModelVersion
	for _, v := range s.versions {
		if v.ModelID != m.ID {
			continue
		}
		if stg := o.Filters["stage"]; stg != "" && string(v.Stage) != stg {
			continue
		}
		if !hasLabels(v.Labels, o.Labels) {
			continue
		}
		out = append(out, v)
	}
	sortByCreated(out, func(v *domain.ModelVersion) (int64, string) { return v.CreatedAt, v.ID })
	items, next := domain.Page(out, func(v *domain.ModelVersion) (int64, string) { return v.CreatedAt, v.ID }, o.PageToken, o.PageSize)
	return deepCopyAll(items), next, nil
}

func (s *Store) UpdateVersion(_ context.Context, v *domain.ModelVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.versions[v.ID]
	if !ok {
		return domain.NotFound("version not found")
	}
	stored := deepCopy(v)
	stored.LegalHold = cur.LegalHold // not writable through an update; see UpdateModel
	// Only SetStage moves a stage, so only SetStage moves its timestamp (§20.8.3).
	stored.StageChangedAt = cur.StageChangedAt
	stored.LockedAt = cur.LockedAt // set by SetStage alone, never cleared (§00.11.19)
	s.versions[v.ID] = stored
	return nil
}

func (s *Store) DeleteVersion(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.versions[id]; !ok {
		return domain.NotFound("version not found")
	}
	s.deleteVersionLocked(id)
	return nil
}

// deleteVersionLocked removes a version and its dependent rows. Caller holds s.mu.
func (s *Store) deleteVersionLocked(id string) {
	delete(s.versions, id)
	for aid, a := range s.artifacts {
		if a.VersionID == id {
			delete(s.artifacts, aid)
		}
	}
	for did, d := range s.deployments {
		if d.VersionID == id {
			delete(s.deployments, did)
		}
	}
	for eid, e := range s.lineage {
		if e.SrcID == id || e.DstID == id {
			delete(s.lineage, eid)
		}
	}
	// Insight rows cascade with the version (§11.3).
	delete(s.insights, id)
	delete(s.layers, id)
	delete(s.footprints, id)
	delete(s.evaluations, id)
	// Reviews cascade from model_version, not from the edge (§17.5.1): with the version gone
	// there is no subject left to have reviewed. Deleting the *edge* above deliberately
	// leaves its reviews behind.
	kept := s.reviews[:0]
	for _, r := range s.reviews {
		if r.VersionID != id {
			kept = append(kept, r)
		}
	}
	s.reviews = kept
	// Validations cascade from model_version (§20.8.2).
	keptVals := s.validations[:0]
	for _, v := range s.validations {
		if v.VersionID != id {
			keptVals = append(keptVals, v)
		}
	}
	s.validations = keptVals
}

func (s *Store) CountVersionsInStage(_ context.Context, modelID string, stage domain.Stage) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, v := range s.versions {
		if v.ModelID == modelID && v.Stage == stage {
			n++
		}
	}
	return n, nil
}

func (s *Store) SetStage(_ context.Context, versionID string, to domain.Stage, singleton bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[versionID]
	if !ok {
		return domain.NotFound("version not found")
	}
	now := domain.NowMillis()
	if singleton {
		for _, o := range s.versions {
			if o.ModelID == v.ModelID && o.ID != v.ID && o.Stage == to {
				o.Stage = domain.StageArchived
				o.UpdatedAt, o.StageChangedAt = now, now
			}
		}
	}
	v.Stage = to
	v.UpdatedAt, v.StageChangedAt = now, now
	if domain.LocksArtifacts(to) && v.LockedAt == 0 {
		v.LockedAt = now
	}
	return nil
}

func (s *Store) Resolve(_ context.Context, model string, sel domain.Selector) (*domain.ModelVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.lookupModel(model)
	if err != nil {
		return nil, err
	}
	stage := sel.Stage
	if sel.Version == "" && sel.LabelKey == "" && stage == "" {
		stage = domain.StageProduction // default serving stage (§04.2)
	}
	var match []*domain.ModelVersion
	for _, v := range s.versions {
		if v.ModelID != m.ID {
			continue
		}
		switch {
		case sel.Version != "":
			if v.Name == sel.Version {
				match = append(match, v)
			}
		case sel.LabelKey != "":
			if v.Labels[sel.LabelKey] == sel.LabelValue {
				match = append(match, v)
			}
		default:
			if v.Stage == stage {
				match = append(match, v)
			}
		}
	}
	if len(match) == 0 {
		return nil, domain.Precondition("no version matches selector", nil)
	}
	sort.Slice(match, func(i, j int) bool { return match[i].CreatedAt > match[j].CreatedAt })
	return deepCopy(match[0]), nil // newest wins for non-singleton
}

// ---- Artifacts ----

func (s *Store) ArtifactRefsURI(_ context.Context, uri string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.artifacts {
		if a.URI == uri {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) CreateArtifact(_ context.Context, a *domain.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.artifacts {
		if ex.VersionID == a.VersionID && ex.Name == a.Name {
			return domain.Exists("artifact '" + a.Name + "' already exists")
		}
	}
	s.artifacts[a.ID] = deepCopy(a)
	return nil
}

func (s *Store) GetArtifact(_ context.Context, versionID, name string) (*domain.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.artifacts {
		if a.VersionID == versionID && a.Name == name {
			return deepCopy(a), nil
		}
	}
	return nil, domain.NotFound("artifact '" + name + "' not found")
}

func (s *Store) GetArtifactByID(_ context.Context, id string) (*domain.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if a, ok := s.artifacts[id]; ok {
		return deepCopy(a), nil
	}
	return nil, domain.NotFound("artifact '" + id + "' not found")
}

func (s *Store) ListArtifacts(_ context.Context, versionID string) ([]*domain.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*domain.Artifact
	for _, a := range s.artifacts {
		if a.VersionID == versionID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return deepCopyAll(out), nil
}

func (s *Store) UpdateArtifact(_ context.Context, a *domain.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.artifacts[a.ID]; !ok {
		return domain.NotFound("artifact not found")
	}
	s.artifacts[a.ID] = deepCopy(a)
	return nil
}

func (s *Store) DeleteArtifact(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.artifacts[id]; !ok {
		return domain.NotFound("artifact not found")
	}
	delete(s.artifacts, id)
	return nil
}

// ---- Lineage & audit ----

func (s *Store) AddLineageEdge(_ context.Context, e *domain.LineageEdge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lineage[e.ID] = deepCopy(e)
	return nil
}

func (s *Store) ListLineage(_ context.Context, versionID string) ([]*domain.LineageEdge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*domain.LineageEdge
	for _, e := range s.lineage {
		if e.SrcID == versionID || e.DstID == versionID {
			out = append(out, e)
		}
	}
	return deepCopyAll(out), nil
}

func (s *Store) DeleteLineageEdge(_ context.Context, id, versionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.lineage[id]
	if !ok || (e.SrcID != versionID && e.DstID != versionID) {
		return domain.NotFound("lineage edge not found")
	}
	delete(s.lineage, id)
	return nil
}

// ---- Deployments ----

func (s *Store) CreateDeployment(_ context.Context, d *domain.Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deployments[d.ID] = deepCopy(d)
	return nil
}

func (s *Store) GetDeployment(_ context.Context, id string) (*domain.Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if d, ok := s.deployments[id]; ok {
		return deepCopy(d), nil
	}
	return nil, domain.NotFound("deployment '" + id + "' not found")
}

func (s *Store) ListDeployments(_ context.Context, versionID string) ([]*domain.Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*domain.Deployment
	for _, d := range s.deployments {
		if d.VersionID == versionID {
			out = append(out, d)
		}
	}
	sortByCreated(out, func(d *domain.Deployment) (int64, string) { return d.CreatedAt, d.ID })
	return deepCopyAll(out), nil
}

func (s *Store) UpdateDeployment(_ context.Context, d *domain.Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.deployments[d.ID]; !ok {
		return domain.NotFound("deployment not found")
	}
	s.deployments[d.ID] = deepCopy(d)
	return nil
}

func (s *Store) DeleteDeployment(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.deployments[id]; !ok {
		return domain.NotFound("deployment not found")
	}
	delete(s.deployments, id)
	return nil
}

func (s *Store) AppendAudit(_ context.Context, e *domain.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, deepCopy(e))
	return nil
}

func (s *Store) ListAudit(_ context.Context, subjectType, subjectID string, o domain.ListOptions) ([]*domain.AuditEvent, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*domain.AuditEvent
	for _, e := range s.audit {
		if subjectType != "" && e.SubjectType != subjectType {
			continue
		}
		if subjectID != "" && e.SubjectID != subjectID {
			continue
		}
		if o.AsOf > 0 && e.At > o.AsOf {
			continue
		}
		out = append(out, e)
	}
	sortByCreated(out, func(e *domain.AuditEvent) (int64, string) { return e.At, e.ID })
	items, next := domain.Page(out, func(e *domain.AuditEvent) (int64, string) { return e.At, e.ID }, o.PageToken, o.PageSize)
	return deepCopyAll(items), next, nil
}

// ---- helpers ----

func hasLabels(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// sortByCreated orders items newest-first by (createdAt, id) — the stable order cursor
// pagination expects (§03.3).
func sortByCreated[T any](items []T, key func(T) (int64, string)) {
	sort.Slice(items, func(i, j int) bool {
		ai, aid := key(items[i])
		bj, bid := key(items[j])
		if ai != bj {
			return ai > bj
		}
		return aid > bid
	})
}
