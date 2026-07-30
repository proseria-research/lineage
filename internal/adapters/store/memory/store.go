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
	mu        sync.RWMutex
	models    map[string]*domain.Model        // id -> model
	modelByNm map[string]string               // name -> id
	versions  map[string]*domain.ModelVersion // id -> version
	artifacts map[string]*domain.Artifact     // id -> artifact
	lineage   map[string]*domain.LineageEdge  // id -> edge
	audit     []*domain.AuditEvent
}

func New() *Store {
	return &Store{
		models:    map[string]*domain.Model{},
		modelByNm: map[string]string{},
		versions:  map[string]*domain.ModelVersion{},
		artifacts: map[string]*domain.Artifact{},
		lineage:   map[string]*domain.LineageEdge{},
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
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return page(out, o.PageSize)
}

func (s *Store) UpdateModel(_ context.Context, m *domain.Model) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.models[m.ID]; !ok {
		return domain.NotFound("model not found")
	}
	s.models[m.ID] = m
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

func (s *Store) ListVersions(_ context.Context, model string, o domain.ListOptions) ([]*domain.ModelVersion, string, error) {
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
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return page(out, o.PageSize)
}

func (s *Store) UpdateVersion(_ context.Context, v *domain.ModelVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.versions[v.ID]; !ok {
		return domain.NotFound("version not found")
	}
	s.versions[v.ID] = v
	return nil
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

func (s *Store) AppendAudit(_ context.Context, e *domain.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, e)
	return nil
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

// page applies a naive single-page cutoff. Cursor tokens are a TODO for the real
// per-dialect stores; the scaffold returns an empty nextToken.
func page[T any](items []T, size int) ([]T, string, error) {
	if size <= 0 || size > 500 {
		size = 50
	}
	if len(items) > size {
		items = items[:size]
	}
	return items, "", nil
}
