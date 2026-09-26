package sqlstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/proseria-research/lineage/internal/domain"
)

// sqliteDialect is the minimum Open needs; the real one lives in the sqlite adapter, which
// imports this package and so cannot be imported back from here.
type sqliteDialect struct{}

func (sqliteDialect) Name() string                     { return "sqlite" }
func (sqliteDialect) Rebind(q string) string           { return q }
func (sqliteDialect) LockModelByVersionSQL() string    { return "" }
func (sqliteDialect) LockVersionRowSQL() string        { return "" }
func (sqliteDialect) JSONContainsClause(string) string { return "" }
func (sqliteDialect) IsUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// TestMRMMigrationUpgradesExistingData runs the §20.8 migrations over a database written by
// the schema before them. The conformance suites only ever see a fresh schema, and the two
// things that could go wrong here only show on existing rows: the classification rebuild
// losing or reshaping an EU row, and the stage_changed_at backfill reading the wrong event.
func TestMRMMigrationUpgradesExistingData(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "up.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	first := -1
	for i, m := range migrations {
		if strings.Contains(m, "ADD COLUMN stage_changed_at") {
			first = i
			break
		}
	}
	if first < 0 {
		t.Fatal("could not find the first §20.8 migration")
	}

	// The schema as M16 left it.
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	for i := 0; i < first; i++ {
		exec(migrations[i])
		exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}

	exec(`INSERT INTO model (id,name,state,created_at,updated_at) VALUES ('m1','fraud','ACTIVE',1,1)`)
	// v1 was promoted twice (the latest event wins); v2 never moved (creation time).
	exec(`INSERT INTO model_version (id,model_id,name,stage,created_at,updated_at) VALUES ('v1','m1','1.0.0','production',100,900)`)
	exec(`INSERT INTO model_version (id,model_id,name,stage,created_at,updated_at) VALUES ('v2','m1','2.0.0','draft',200,200)`)
	for _, e := range []struct {
		id, action, subject string
		at                  int64
	}{
		{"a1", "version.stage_changed", "v1", 300},
		{"a2", "version.stage_changed", "v1", 500},
		{"a3", "version.update", "v1", 800}, // not a stage move
		{"a4", "version.stage_changed", "m1", 950},
	} {
		exec(`INSERT INTO audit_event (id,at,action,subject_type,subject_id) VALUES (?,?,?,'model_version',?)`,
			e.id, e.at, e.action, e.subject)
	}
	exec(`INSERT INTO classification (model_id,regime,eu_gpai_tier,eu_system_risk_class,intended_purpose,basis,classified_at,classified_by,review_due_at)
	      VALUES ('m1','eu_ai_act','none','high_annex_iii','purpose','basis',1773,'risk@acme.example',1804)`)

	s, err := Open(db, sqliteDialect{})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for id, want := range map[string]int64{"v1": 500, "v2": 200} {
		v, err := s.GetVersionByID(ctx, id)
		if err != nil {
			t.Fatalf("GetVersionByID %s: %v", id, err)
		}
		if v.StageChangedAt != want {
			t.Fatalf("%s backfilled stage_changed_at = %d, want %d", id, v.StageChangedAt, want)
		}
	}

	c, err := s.GetClassification(ctx, "m1", domain.RegimeEUAIAct)
	if err != nil {
		t.Fatalf("EU row lost in the rebuild: %v", err)
	}
	if c.EUSystemRiskClass != domain.EUClassHighAnnexIII || c.EUGpaiTier != domain.EUGpaiNone ||
		c.IntendedPurpose != "purpose" || c.Basis != "basis" || c.ClassifiedAt != 1773 ||
		c.ClassifiedBy != "risk@acme.example" || c.ReviewDueAt == nil || *c.ReviewDueAt != 1804 || c.MRMTier != "" {
		t.Fatalf("EU row reshaped by the rebuild: %+v", c)
	}

	// The rebuilt table carries the new branch, and still enforces the old one.
	if err := s.PutClassification(ctx, &domain.RiskClassification{ModelID: "m1", Regime: domain.RegimeMRM,
		MRMTier: domain.MRMTier1, Basis: "b", ClassifiedAt: 2000}); err != nil {
		t.Fatalf("mrm row after upgrade: %v", err)
	}
	if err := s.PutClassification(ctx, &domain.RiskClassification{ModelID: "m1", Regime: domain.RegimeEUAIAct,
		EUGpaiTier: domain.EUGpaiNone, ClassifiedAt: 2000}); err == nil {
		t.Fatal("the rebuilt CHECK let a null eu_system_risk_class through")
	}
	// ON DELETE CASCADE from model survived the rename.
	if _, err := db.ExecContext(ctx, `DELETE FROM model WHERE id='m1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM classification`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("classification rows survived their model: %v n=%d", err, n)
	}
}

// TestLockedAtMigrationBackfill runs the §00.11.19 migration over a database written by the
// schema before it. A version is locked if its history shows it ever entered staging or
// production (earliest such event), else if it sits there now (stage_changed_at).
func TestLockedAtMigrationBackfill(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "up.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	first := -1
	for i, m := range migrations {
		if strings.Contains(m, "ADD COLUMN locked_at") {
			first = i
			break
		}
	}
	if first < 0 {
		t.Fatal("could not find the locked_at migration")
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	for i := 0; i < first; i++ {
		exec(migrations[i])
		exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	seedLockHistory(t, exec)

	s, err := Open(db, sqliteDialect{})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	checkLockBackfill(t, s)
}

// seedLockHistory writes the versions and events the backfill must read, in the pre-migration
// schema.
func seedLockHistory(t *testing.T, exec func(string, ...any)) {
	t.Helper()
	exec(`INSERT INTO model (id,name,state,created_at,updated_at) VALUES ('m1','fraud','ACTIVE',1,1)`)
	for _, v := range []struct {
		id, stage string
		changed   int64
	}{
		{"staged", "staging", 400},      // no history: falls back to stage_changed_at
		{"returned", "draft", 700},      // staging at 300, back to draft at 700
		{"retired", "archived", 900},    // staging 200, production 500, archived 900
		{"shelved", "archived", 250},    // draft → archived directly: never locked
		{"fresh", "draft", 100},         // never moved
		{"prodnohist", "production", 0}, // no history, no stage_changed_at: updated_at
	} {
		exec(`INSERT INTO model_version (id,model_id,name,stage,created_at,updated_at,stage_changed_at) VALUES (?,'m1',?,?,100,950,NULLIF(?,0))`,
			v.id, v.id, v.stage, v.changed)
	}
	for _, e := range []struct {
		id, subject, data string
		at                int64
	}{
		{"e1", "returned", `{"from":"draft","reason":"","to":"staging"}`, 300},
		{"e2", "returned", `{"from":"staging","reason":"","to":"draft"}`, 700},
		{"e3", "retired", `{"from":"draft","reason":"","to":"staging"}`, 200},
		{"e4", "retired", `{"from":"staging","reason":"","to":"production"}`, 500},
		{"e5", "retired", `{"from":"production","reason":"","to":"archived"}`, 900},
		{"e6", "shelved", `{"from":"draft","reason":"to staging later","to":"archived"}`, 250},
	} {
		exec(`INSERT INTO audit_event (id,at,action,subject_type,subject_id,data) VALUES (?,?,'version.stage_changed','model_version',?,?)`,
			e.id, e.at, e.subject, e.data)
	}
}

func checkLockBackfill(t *testing.T, s *Store) {
	t.Helper()
	for id, want := range map[string]int64{
		"staged": 400, "returned": 300, "retired": 200, "shelved": 0, "fresh": 0, "prodnohist": 950,
	} {
		v, err := s.GetVersionByID(context.Background(), id)
		if err != nil {
			t.Fatalf("GetVersionByID %s: %v", id, err)
		}
		if v.LockedAt != want {
			t.Fatalf("%s backfilled locked_at = %d, want %d", id, v.LockedAt, want)
		}
	}
}
