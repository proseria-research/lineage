// Package sqlstore is a database/sql MetadataStore shared by the SQLite and Postgres
// adapters (§02.7). The per-dialect packages supply a Dialect (placeholders, unique-
// violation detection, row locking) and the driver; the logical schema is shared. Engine
// -specific power (JSONB filtering, GIN, pgvector) is layered on later behind the Dialect.
package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/proseria-research/lineage/internal/domain"
)

// Dialect abstracts the small SQL differences between engines.
type Dialect interface {
	Name() string
	// Rebind converts ?-style placeholders to the dialect's style.
	Rebind(q string) string
	// IsUniqueViolation reports whether err is a unique-constraint violation.
	IsUniqueViolation(err error) bool
	// LockModelByVersionSQL returns a statement (one ? = version id) that locks the owning
	// model row FOR UPDATE, or "" if the engine needs no explicit lock (SQLite: single-writer).
	LockModelByVersionSQL() string
	// JSONContainsClause returns a WHERE fragment (one ? = the JSON operand) that tests
	// whether a JSON column contains an object, or "" if the engine can't push this down and
	// the store must filter in Go. Postgres: `<col>::jsonb @> ?::jsonb` (§02.7).
	JSONContainsClause(column string) string
}

type Store struct {
	db *sql.DB
	d  Dialect
}

var _ domain.MetadataStore = (*Store)(nil)

// Open migrates the schema and returns a ready store.
func Open(db *sql.DB, d Dialect) (*Store, error) {
	s := &Store{db: db, d: d}
	if err := s.migrate(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) DB() *sql.DB        { return s.db }
func (s *Store) Close() error       { return s.db.Close() }
func (s *Store) rb(q string) string { return s.d.Rebind(q) }

// jsonFilters builds label/custom_properties containment WHERE fragments when the dialect can
// push them down (Postgres JSONB, §02.7). labelsPushed reports whether label filtering was
// handled in SQL (so the caller skips the Go fallback). Custom-property filtering is
// Postgres-only — on engines without push-down it is an explicit invalid-argument error.
func (s *Store) jsonFilters(o domain.ListOptions, labelCol, cpCol string) (clauses []string, args []any, labelsPushed bool, err error) {
	if len(o.Labels) > 0 {
		if c := s.d.JSONContainsClause(labelCol); c != "" {
			clauses = append(clauses, c)
			args = append(args, marshalMap(o.Labels))
			labelsPushed = true
		}
	}
	if len(o.CustomProps) > 0 {
		c := s.d.JSONContainsClause(cpCol)
		if c == "" {
			return nil, nil, false, domain.Invalid("custom-property (cp.*) filtering requires the postgres engine (§02.7)")
		}
		clauses = append(clauses, c)
		args = append(args, marshalMap(o.CustomProps))
	}
	return clauses, args, labelsPushed, nil
}

// ---- Models ----

const modelCols = "id,name,description,owner,state,labels,custom_properties,created_at,updated_at"

func (s *Store) CreateModel(ctx context.Context, m *domain.Model) error {
	_, err := s.db.ExecContext(ctx, s.rb(
		`INSERT INTO model (`+modelCols+`) VALUES (?,?,?,?,?,?,?,?,?)`),
		m.ID, m.Name, m.Description, m.Owner, string(m.State), marshalMap(m.Labels),
		jsonText(m.CustomProperties), m.CreatedAt, m.UpdatedAt)
	if s.d.IsUniqueViolation(err) {
		return domain.Exists("model '" + m.Name + "' already exists")
	}
	return err
}

func (s *Store) GetModel(ctx context.Context, nameOrID string) (*domain.Model, error) {
	row := s.db.QueryRowContext(ctx, s.rb(
		`SELECT `+modelCols+` FROM model WHERE name=? OR id=?`), nameOrID, nameOrID)
	m, err := scanModel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("model '" + nameOrID + "' not found")
	}
	return m, err
}

func (s *Store) ListModels(ctx context.Context, o domain.ListOptions) ([]*domain.Model, string, error) {
	q := `SELECT ` + modelCols + ` FROM model WHERE 1=1`
	var args []any
	if st := o.Filters["state"]; st != "" {
		q += ` AND state=?`
		args = append(args, st)
	}
	if o.Q != "" {
		q += ` AND name LIKE '%'||?||'%'`
		args = append(args, o.Q)
	}
	jc, ja, labelsPushed, err := s.jsonFilters(o, "labels", "custom_properties")
	if err != nil {
		return nil, "", err
	}
	for _, c := range jc {
		q += " AND " + c
	}
	args = append(args, ja...)
	q += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, s.rb(q), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*domain.Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, "", err
		}
		if labelsPushed || hasLabels(m.Labels, o.Labels) {
			out = append(out, m)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	items, next := domain.Page(out, func(m *domain.Model) (int64, string) { return m.CreatedAt, m.ID }, o.PageToken, o.PageSize)
	return items, next, nil
}

func (s *Store) UpdateModel(ctx context.Context, m *domain.Model) error {
	res, err := s.db.ExecContext(ctx, s.rb(
		`UPDATE model SET description=?,owner=?,state=?,labels=?,custom_properties=?,updated_at=? WHERE id=?`),
		m.Description, m.Owner, string(m.State), marshalMap(m.Labels), jsonText(m.CustomProperties), m.UpdatedAt, m.ID)
	return affected(res, err, "model")
}

// ---- Versions ----

// vSel joins the model to fill the denormalized ModelVersion.Model (name) field.
const vSel = `SELECT v.id,v.model_id,v.name,v.description,v.author,v.stage,v.labels,v.custom_properties,v.created_at,v.updated_at,m.name ` +
	`FROM model_version v JOIN model m ON m.id=v.model_id`

func (s *Store) CreateVersion(ctx context.Context, v *domain.ModelVersion) error {
	_, err := s.db.ExecContext(ctx, s.rb(
		`INSERT INTO model_version (id,model_id,name,description,author,stage,labels,custom_properties,created_at,updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`),
		v.ID, v.ModelID, v.Name, v.Description, v.Author, string(v.Stage),
		marshalMap(v.Labels), jsonText(v.CustomProperties), v.CreatedAt, v.UpdatedAt)
	if s.d.IsUniqueViolation(err) {
		return domain.Exists("version '" + v.Name + "' already exists")
	}
	return err
}

func (s *Store) GetVersion(ctx context.Context, model, version string) (*domain.ModelVersion, error) {
	m, err := s.GetModel(ctx, model)
	if err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, s.rb(vSel+` WHERE v.model_id=? AND v.name=?`), m.ID, version)
	v, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("version '" + version + "' not found")
	}
	return v, err
}

func (s *Store) GetVersionByID(ctx context.Context, id string) (*domain.ModelVersion, error) {
	row := s.db.QueryRowContext(ctx, s.rb(vSel+` WHERE v.id=?`), id)
	v, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("version '" + id + "' not found")
	}
	return v, err
}

func (s *Store) ListVersions(ctx context.Context, model string, o domain.ListOptions) ([]*domain.ModelVersion, string, error) {
	m, err := s.GetModel(ctx, model)
	if err != nil {
		return nil, "", err
	}
	q := vSel + ` WHERE v.model_id=?`
	args := []any{m.ID}
	if stg := o.Filters["stage"]; stg != "" {
		q += ` AND v.stage=?`
		args = append(args, stg)
	}
	jc, ja, labelsPushed, err := s.jsonFilters(o, "v.labels", "v.custom_properties")
	if err != nil {
		return nil, "", err
	}
	for _, c := range jc {
		q += " AND " + c
	}
	args = append(args, ja...)
	q += ` ORDER BY v.created_at DESC, v.id DESC`
	rows, err := s.db.QueryContext(ctx, s.rb(q), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*domain.ModelVersion
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, "", err
		}
		if labelsPushed || hasLabels(v.Labels, o.Labels) {
			out = append(out, v)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	items, next := domain.Page(out, func(v *domain.ModelVersion) (int64, string) { return v.CreatedAt, v.ID }, o.PageToken, o.PageSize)
	return items, next, nil
}

func (s *Store) UpdateVersion(ctx context.Context, v *domain.ModelVersion) error {
	res, err := s.db.ExecContext(ctx, s.rb(
		`UPDATE model_version SET description=?,author=?,labels=?,custom_properties=?,updated_at=? WHERE id=?`),
		v.Description, v.Author, marshalMap(v.Labels), jsonText(v.CustomProperties), v.UpdatedAt, v.ID)
	return affected(res, err, "version")
}

func (s *Store) SetStage(ctx context.Context, versionID string, to domain.Stage, singleton bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	now := domain.NowMillis()
	if singleton {
		if lock := s.d.LockModelByVersionSQL(); lock != "" {
			if _, err := tx.ExecContext(ctx, s.rb(lock), versionID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, s.rb(
			`UPDATE model_version SET stage=?, updated_at=?
			 WHERE model_id=(SELECT model_id FROM model_version WHERE id=?) AND stage=? AND id<>?`),
			string(domain.StageArchived), now, versionID, string(to), versionID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, s.rb(`UPDATE model_version SET stage=?, updated_at=? WHERE id=?`),
		string(to), now, versionID)
	if err := affected(res, err, "version"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Resolve(ctx context.Context, model string, sel domain.Selector) (*domain.ModelVersion, error) {
	m, err := s.GetModel(ctx, model)
	if err != nil {
		return nil, err
	}
	switch {
	case sel.Version != "":
		return s.GetVersion(ctx, model, sel.Version)
	case sel.LabelKey != "":
		all, _, err := s.ListVersions(ctx, model, domain.ListOptions{})
		if err != nil {
			return nil, err
		}
		for _, v := range all { // ListVersions is newest-first
			if v.Labels[sel.LabelKey] == sel.LabelValue {
				return v, nil
			}
		}
		return nil, domain.Precondition("no version matches selector", nil)
	default:
		stage := sel.Stage
		if stage == "" {
			stage = domain.StageProduction // default serving stage (§04.2)
		}
		row := s.db.QueryRowContext(ctx, s.rb(
			vSel+` WHERE v.model_id=? AND v.stage=? ORDER BY v.created_at DESC LIMIT 1`), m.ID, string(stage))
		v, err := scanVersion(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.Precondition("no version matches selector", nil)
		}
		return v, err
	}
}

// ---- Artifacts ----

const artCols = "id,version_id,kind,name,uri,storage_backend,storage_path,size_bytes,digest,media_type,model_format_name,model_format_version,service_account,custom_properties,created_at,updated_at"

func (s *Store) CreateArtifact(ctx context.Context, a *domain.Artifact) error {
	var fname, fver string
	if a.ModelFormat != nil {
		fname, fver = a.ModelFormat.Name, a.ModelFormat.Version
	}
	_, err := s.db.ExecContext(ctx, s.rb(
		`INSERT INTO artifact (`+artCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		a.ID, a.VersionID, string(a.Kind), a.Name, a.URI, a.StorageBackend, a.StoragePath,
		a.SizeBytes, a.Digest, a.MediaType, fname, fver, a.ServiceAccount,
		jsonText(a.CustomProperties), a.CreatedAt, a.UpdatedAt)
	if s.d.IsUniqueViolation(err) {
		return domain.Exists("artifact '" + a.Name + "' already exists")
	}
	return err
}

func (s *Store) ArtifactRefsURI(ctx context.Context, uri string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, s.rb(`SELECT 1 FROM artifact WHERE uri=? LIMIT 1`), uri).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ListArtifacts(ctx context.Context, versionID string) ([]*domain.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, s.rb(
		`SELECT `+artCols+` FROM artifact WHERE version_id=? ORDER BY name`), versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- Lineage & audit ----

func (s *Store) AddLineageEdge(ctx context.Context, e *domain.LineageEdge) error {
	_, err := s.db.ExecContext(ctx, s.rb(
		`INSERT INTO lineage_edge (id,src_type,src_id,relation,dst_type,dst_id,dst_ref,created_at)
		 VALUES (?,?,?,?,?,?,?,?)`),
		e.ID, e.SrcType, e.SrcID, string(e.Relation), e.DstType, e.DstID, e.DstRef, e.CreatedAt)
	return err
}

func (s *Store) ListLineage(ctx context.Context, versionID string) ([]*domain.LineageEdge, error) {
	rows, err := s.db.QueryContext(ctx, s.rb(
		`SELECT id,src_type,src_id,relation,dst_type,dst_id,dst_ref,created_at
		 FROM lineage_edge WHERE src_id=? OR dst_id=?`), versionID, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.LineageEdge
	for rows.Next() {
		var e domain.LineageEdge
		var rel string
		if err := rows.Scan(&e.ID, &e.SrcType, &e.SrcID, &rel, &e.DstType, &e.DstID, &e.DstRef, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Relation = domain.LineageRelation(rel)
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *Store) AppendAudit(ctx context.Context, e *domain.AuditEvent) error {
	_, err := s.db.ExecContext(ctx, s.rb(
		`INSERT INTO audit_event (id,at,actor,action,subject_type,subject_id,summary,data)
		 VALUES (?,?,?,?,?,?,?,?)`),
		e.ID, e.At, e.Actor, e.Action, e.SubjectType, e.SubjectID, e.Summary, jsonText(e.Data))
	return err
}

func (s *Store) DeleteModel(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.rb(`DELETE FROM model WHERE id=?`), id)
	return affected(res, err, "model")
}

func (s *Store) DeleteVersion(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.rb(`DELETE FROM model_version WHERE id=?`), id)
	return affected(res, err, "version")
}

func (s *Store) CountVersionsInStage(ctx context.Context, modelID string, stage domain.Stage) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.rb(
		`SELECT COUNT(*) FROM model_version WHERE model_id=? AND stage=?`), modelID, string(stage)).Scan(&n)
	return n, err
}

func (s *Store) GetArtifact(ctx context.Context, versionID, name string) (*domain.Artifact, error) {
	row := s.db.QueryRowContext(ctx, s.rb(
		`SELECT `+artCols+` FROM artifact WHERE version_id=? AND name=?`), versionID, name)
	a, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("artifact '" + name + "' not found")
	}
	return a, err
}

func (s *Store) UpdateArtifact(ctx context.Context, a *domain.Artifact) error {
	res, err := s.db.ExecContext(ctx, s.rb(
		`UPDATE artifact SET media_type=?,service_account=?,custom_properties=?,updated_at=? WHERE id=?`),
		a.MediaType, a.ServiceAccount, jsonText(a.CustomProperties), a.UpdatedAt, a.ID)
	return affected(res, err, "artifact")
}

func (s *Store) DeleteArtifact(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.rb(`DELETE FROM artifact WHERE id=?`), id)
	return affected(res, err, "artifact")
}

func (s *Store) DeleteLineageEdge(ctx context.Context, id, versionID string) error {
	res, err := s.db.ExecContext(ctx, s.rb(
		`DELETE FROM lineage_edge WHERE id=? AND (src_id=? OR dst_id=?)`), id, versionID, versionID)
	return affected(res, err, "lineage edge")
}

// ---- Deployments ----

const depCols = "id,version_id,environment,endpoint_uri,status,external_ref,created_at,updated_at"

func (s *Store) CreateDeployment(ctx context.Context, d *domain.Deployment) error {
	_, err := s.db.ExecContext(ctx, s.rb(
		`INSERT INTO deployment (`+depCols+`) VALUES (?,?,?,?,?,?,?,?)`),
		d.ID, d.VersionID, d.Environment, d.EndpointURI, string(d.Status), d.ExternalRef, d.CreatedAt, d.UpdatedAt)
	return err
}

func (s *Store) GetDeployment(ctx context.Context, id string) (*domain.Deployment, error) {
	row := s.db.QueryRowContext(ctx, s.rb(`SELECT `+depCols+` FROM deployment WHERE id=?`), id)
	d, err := scanDeployment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("deployment '" + id + "' not found")
	}
	return d, err
}

func (s *Store) ListDeployments(ctx context.Context, versionID string) ([]*domain.Deployment, error) {
	rows, err := s.db.QueryContext(ctx, s.rb(
		`SELECT `+depCols+` FROM deployment WHERE version_id=? ORDER BY created_at DESC, id DESC`), versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) UpdateDeployment(ctx context.Context, d *domain.Deployment) error {
	res, err := s.db.ExecContext(ctx, s.rb(
		`UPDATE deployment SET environment=?,endpoint_uri=?,status=?,external_ref=?,updated_at=? WHERE id=?`),
		d.Environment, d.EndpointURI, string(d.Status), d.ExternalRef, d.UpdatedAt, d.ID)
	return affected(res, err, "deployment")
}

func (s *Store) DeleteDeployment(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.rb(`DELETE FROM deployment WHERE id=?`), id)
	return affected(res, err, "deployment")
}

// ---- Audit feed ----

func (s *Store) ListAudit(ctx context.Context, subjectType, subjectID string, o domain.ListOptions) ([]*domain.AuditEvent, string, error) {
	q := `SELECT id,at,actor,action,subject_type,subject_id,summary,data FROM audit_event WHERE 1=1`
	var args []any
	if subjectType != "" {
		q += ` AND subject_type=?`
		args = append(args, subjectType)
	}
	if subjectID != "" {
		q += ` AND subject_id=?`
		args = append(args, subjectID)
	}
	q += ` ORDER BY at DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, s.rb(q), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*domain.AuditEvent
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	items, next := domain.Page(out, func(e *domain.AuditEvent) (int64, string) { return e.At, e.ID }, o.PageToken, o.PageSize)
	return items, next, nil
}

// ---- scan helpers ----

type scanner interface{ Scan(dest ...any) error }

func scanModel(sc scanner) (*domain.Model, error) {
	var m domain.Model
	var state, labels string
	var cp sql.NullString
	if err := sc.Scan(&m.ID, &m.Name, &m.Description, &m.Owner, &state, &labels, &cp, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.State = domain.ModelState(state)
	m.Labels = unmarshalMap(labels)
	m.CustomProperties = fromNull(cp)
	return &m, nil
}

func scanVersion(sc scanner) (*domain.ModelVersion, error) {
	var v domain.ModelVersion
	var stage, labels string
	var cp sql.NullString
	if err := sc.Scan(&v.ID, &v.ModelID, &v.Name, &v.Description, &v.Author, &stage, &labels, &cp, &v.CreatedAt, &v.UpdatedAt, &v.Model); err != nil {
		return nil, err
	}
	v.Stage = domain.Stage(stage)
	v.Labels = unmarshalMap(labels)
	v.CustomProperties = fromNull(cp)
	return &v, nil
}

func scanArtifact(sc scanner) (*domain.Artifact, error) {
	var a domain.Artifact
	var kind, fname, fver string
	var cp sql.NullString
	if err := sc.Scan(&a.ID, &a.VersionID, &kind, &a.Name, &a.URI, &a.StorageBackend, &a.StoragePath,
		&a.SizeBytes, &a.Digest, &a.MediaType, &fname, &fver, &a.ServiceAccount, &cp, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.Kind = domain.ArtifactKind(kind)
	if fname != "" {
		a.ModelFormat = &domain.ModelFormat{Name: fname, Version: fver}
	}
	a.CustomProperties = fromNull(cp)
	return &a, nil
}

func scanDeployment(sc scanner) (*domain.Deployment, error) {
	var d domain.Deployment
	var status string
	if err := sc.Scan(&d.ID, &d.VersionID, &d.Environment, &d.EndpointURI, &status, &d.ExternalRef, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.Status = domain.DeploymentStatus(status)
	return &d, nil
}

func scanAudit(sc scanner) (*domain.AuditEvent, error) {
	var e domain.AuditEvent
	var data sql.NullString
	if err := sc.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.SubjectType, &e.SubjectID, &e.Summary, &data); err != nil {
		return nil, err
	}
	e.Data = fromNull(data)
	return &e, nil
}

// ---- misc helpers ----

func affected(res sql.Result, err error, kind string) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.NotFound(kind + " not found")
	}
	return nil
}

func jsonText(r json.RawMessage) any {
	if len(r) == 0 {
		return nil
	}
	return string(r)
}

func fromNull(ns sql.NullString) json.RawMessage {
	if ns.Valid && ns.String != "" {
		return json.RawMessage(ns.String)
	}
	return nil
}

func marshalMap(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func unmarshalMap(s string) map[string]string {
	if s == "" || s == "{}" {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	return m
}

func hasLabels(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}
