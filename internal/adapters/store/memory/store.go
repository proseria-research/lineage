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

type Store struct {
	mu          sync.RWMutex
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
}

func New() *Store {
	return &Store{
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
	}
}

var _ domain.MetadataStore = (*Store)(nil)

// ---- Models ----

func (s *Store) CreateModel(_ context.Context, m *domain.Model) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.modelByNm[m.Name]; ok {
		return domain.Exists("model '" + m.Name + "' already exists")
	}
	s.models[m.ID] = m
	s.modelByNm[m.Name] = m.ID
	return nil
}

func (s *Store) GetModel(_ context.Context, nameOrID string) (*domain.Model, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lookupModel(nameOrID)
}

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
	items, next := domain.Page(out, func(m *domain.Model) (int64, string) { return m.CreatedAt, m.ID }, o.PageToken, o.PageSize)
	return items, next, nil
}

func (s *Store) UpdateModel(_ context.Context, m *domain.Model) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.models[m.ID]
	if !ok {
		return domain.NotFound("model not found")
	}
	// The hold is not a writable field: only SetHold moves it (§19.3.1). This adapter swaps
	// the whole entity, so without carrying it over, any metadata PATCH built from a copy
	// taken before the hold — or one that simply left the field nil — would release it. The
	// SQL adapters get this from the column list; here it has to be explicit.
	m.LegalHold = cur.LegalHold
	s.models[m.ID] = m
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
	s.versions[v.ID] = v
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
			return v, nil
		}
	}
	return nil, domain.NotFound("version '" + version + "' not found")
}

func (s *Store) GetVersionByID(_ context.Context, id string) (*domain.ModelVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.versions[id]; ok {
		return v, nil
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
	return items, next, nil
}

func (s *Store) UpdateVersion(_ context.Context, v *domain.ModelVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.versions[v.ID]
	if !ok {
		return domain.NotFound("version not found")
	}
	v.LegalHold = cur.LegalHold // not writable through an update; see UpdateModel
	s.versions[v.ID] = v
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
	if singleton {
		for _, o := range s.versions {
			if o.ModelID == v.ModelID && o.ID != v.ID && o.Stage == to {
				o.Stage = domain.StageArchived
				o.UpdatedAt = domain.NowMillis()
			}
		}
	}
	v.Stage = to
	v.UpdatedAt = domain.NowMillis()
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
	return match[0], nil // newest wins for non-singleton
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
	s.artifacts[a.ID] = a
	return nil
}

func (s *Store) GetArtifact(_ context.Context, versionID, name string) (*domain.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.artifacts {
		if a.VersionID == versionID && a.Name == name {
			return a, nil
		}
	}
	return nil, domain.NotFound("artifact '" + name + "' not found")
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
	return out, nil
}

func (s *Store) UpdateArtifact(_ context.Context, a *domain.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.artifacts[a.ID]; !ok {
		return domain.NotFound("artifact not found")
	}
	s.artifacts[a.ID] = a
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
	s.lineage[e.ID] = e
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
	return out, nil
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
	s.deployments[d.ID] = d
	return nil
}

func (s *Store) GetDeployment(_ context.Context, id string) (*domain.Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if d, ok := s.deployments[id]; ok {
		return d, nil
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
	return out, nil
}

func (s *Store) UpdateDeployment(_ context.Context, d *domain.Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.deployments[d.ID]; !ok {
		return domain.NotFound("deployment not found")
	}
	s.deployments[d.ID] = d
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
	s.audit = append(s.audit, e)
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
		out = append(out, e)
	}
	sortByCreated(out, func(e *domain.AuditEvent) (int64, string) { return e.At, e.ID })
	items, next := domain.Page(out, func(e *domain.AuditEvent) (int64, string) { return e.At, e.ID }, o.PageToken, o.PageSize)
	return items, next, nil
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
