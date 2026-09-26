package storetest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// RunChangePlans holds every adapter to identical change-control-plan behavior (§22.6):
// append-only rows whose one later write is closing a superseded window, the overlap rule
// refused under the model's lock, and an edge scan bounded to planned models unless narrowed.
//
// It builds its own models so it can run after the other suites.
func RunChangePlans(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	now := domain.NowMillis()

	mk := func(name string) *domain.Model {
		m := &domain.Model{ID: domain.NewID(), Name: name, State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateModel(ctx, m); err != nil {
			t.Fatalf("CreateModel %s: %v", name, err)
		}
		return m
	}
	plan := func(m *domain.Model, from int64) *domain.ChangePlan {
		return &domain.ChangePlan{
			ID: domain.NewID(), ModelID: m.ID, Ref: "K243117", Summary: "retraining; architecture frozen",
			AllowedVerdicts: []domain.Verdict{domain.VerdictIdentical, domain.VerdictReweighted},
			EffectiveFrom:   from, DeclaredBy: "ra@acme.example", DeclaredAt: now,
		}
	}

	t.Run("round trip", func(t *testing.T) {
		m := mk("plan-roundtrip")
		p := plan(m, 1_780_000_000_000)
		p.AllowedMethods = []string{"retrain", "fine_tune"}
		p.ProtocolArtifactID = "01JAVPROTOCOL"
		if err := store.CreateChangePlan(ctx, p, ""); err != nil {
			t.Fatalf("CreateChangePlan: %v", err)
		}
		got, err := store.ListChangePlans(ctx, m.ID)
		if err != nil || len(got) != 1 {
			t.Fatalf("ListChangePlans: %v %+v", err, got)
		}
		g := got[0]
		if g.Ref != p.Ref || g.Summary != p.Summary || g.ProtocolArtifactID != p.ProtocolArtifactID ||
			g.EffectiveFrom != p.EffectiveFrom || g.DeclaredBy != p.DeclaredBy || g.DeclaredAt != p.DeclaredAt {
			t.Fatalf("round trip: %+v", g)
		}
		if len(g.AllowedVerdicts) != 2 || g.AllowedVerdicts[1] != domain.VerdictReweighted {
			t.Fatalf("allowedVerdicts: %v", g.AllowedVerdicts)
		}
		if len(g.AllowedMethods) != 2 || g.AllowedMethods[0] != "retrain" {
			t.Fatalf("allowedMethods: %v", g.AllowedMethods)
		}
		if g.EffectiveTo != nil {
			t.Fatalf("a new plan is open: %v", *g.EffectiveTo)
		}

		// Unconstrained methods stay nil rather than coming back as an empty list, which
		// would read as "no method allowed" (§22.6.1).
		m2 := mk("plan-unconstrained")
		if err := store.CreateChangePlan(ctx, plan(m2, 1_780_000_000_000), ""); err != nil {
			t.Fatalf("CreateChangePlan: %v", err)
		}
		got, _ = store.ListChangePlans(ctx, m2.ID)
		if len(got) != 1 || got[0].AllowedMethods != nil {
			t.Fatalf("nil allowedMethods must survive: %+v", got)
		}

		// The unnarrowed list spans models.
		all, err := store.ListChangePlans(ctx, "")
		if err != nil {
			t.Fatalf("ListChangePlans all: %v", err)
		}
		seen := map[string]bool{}
		for _, q := range all {
			seen[q.ModelID] = true
		}
		if !seen[m.ID] || !seen[m2.ID] {
			t.Fatalf("every model's plans: %+v", all)
		}
	})

	t.Run("overlap refused, supersession keeps history", func(t *testing.T) {
		m := mk("plan-history")
		first := plan(m, 1_780_000_000_000)
		if err := store.CreateChangePlan(ctx, first, ""); err != nil {
			t.Fatalf("first: %v", err)
		}

		// A second open plan, not superseding the first, would leave two in force.
		dup := plan(m, 1_790_000_000_000)
		err := store.CreateChangePlan(ctx, dup, "")
		if !isCode(err, domain.CodeFailedPrecondition) {
			t.Fatalf("overlap must be refused, got %v", err)
		}
		if d := err.(*domain.Error).Details; d["reason"] != "plan_overlap" || d["planId"] != first.ID {
			t.Fatalf("overlap details: %v", d)
		}
		if got, _ := store.ListChangePlans(ctx, m.ID); len(got) != 1 {
			t.Fatalf("a refused plan must not be stored: %+v", got)
		}

		// Superseding at or before the old plan's start is not a supersession.
		early := plan(m, first.EffectiveFrom)
		if err := store.CreateChangePlan(ctx, early, first.ID); !isCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("supersede at the old start: %v", err)
		}
		if err := store.CreateChangePlan(ctx, plan(m, 1_790_000_000_000), "nope"); !isCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("supersede an unknown plan: %v", err)
		}

		second := plan(m, 1_790_000_000_000)
		second.AllowedVerdicts = []domain.Verdict{domain.VerdictReweighted, domain.VerdictRecast}
		if err := store.CreateChangePlan(ctx, second, first.ID); err != nil {
			t.Fatalf("supersede: %v", err)
		}
		got, err := store.ListChangePlans(ctx, m.ID)
		if err != nil || len(got) != 2 {
			t.Fatalf("both rows are kept: %v %+v", err, got)
		}
		if got[0].ID != second.ID || got[1].ID != first.ID {
			t.Fatalf("newest effective_from first: %s, %s", got[0].ID, got[1].ID)
		}
		if got[0].EffectiveTo != nil {
			t.Fatalf("the new plan is open: %v", *got[0].EffectiveTo)
		}
		if got[1].EffectiveTo == nil || *got[1].EffectiveTo != second.EffectiveFrom {
			t.Fatalf("the old plan closes where the new one starts: %v", got[1].EffectiveTo)
		}
		// The old row is otherwise untouched: it still says what was allowed then.
		if len(got[1].AllowedVerdicts) != 2 || got[1].AllowedVerdicts[0] != domain.VerdictIdentical {
			t.Fatalf("history rewritten: %+v", got[1])
		}

		// A closed plan cannot be superseded again, and a plan starting inside a closed
		// window overlaps it.
		err = store.CreateChangePlan(ctx, plan(m, 1_800_000_000_000), first.ID)
		if !isCode(err, domain.CodeFailedPrecondition) || err.(*domain.Error).Details["reason"] != "already_superseded" {
			t.Fatalf("re-supersede: %v", err)
		}
		err = store.CreateChangePlan(ctx, plan(m, 1_785_000_000_000), second.ID)
		if !isCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("supersede before the open plan's start: %v", err)
		}

		// Exactly one plan covers each instant, the boundary included.
		for _, c := range []struct {
			at   int64
			want string
		}{
			{first.EffectiveFrom - 1, ""},
			{first.EffectiveFrom, first.ID},
			{second.EffectiveFrom - 1, first.ID},
			{second.EffectiveFrom, second.ID},
			{second.EffectiveFrom + 1e12, second.ID},
		} {
			p := domain.PlanInForce(got, c.at)
			if (p == nil && c.want != "") || (p != nil && p.ID != c.want) {
				t.Fatalf("plan in force at %d = %v, want %q", c.at, p, c.want)
			}
		}
	})

	t.Run("derivation scan", func(t *testing.T) {
		planned := mk("plan-scan")
		bare := mk("plan-scan-bare")
		parent := mkVersion(planned.ID, "1.0.0")
		child := mkVersion(planned.ID, "1.1.0")
		mustCreateVersion(t, store, parent)
		mustCreateVersion(t, store, child)
		bp := mkVersion(bare.ID, "1.0.0")
		bc := mkVersion(bare.ID, "1.1.0")
		mustCreateVersion(t, store, bp)
		mustCreateVersion(t, store, bc)

		edge := func(src, dst *domain.ModelVersion, ref string) *domain.LineageEdge {
			e := &domain.LineageEdge{ID: domain.NewID(), SrcType: "model_version", SrcID: src.ID,
				Relation: domain.RelDerivedFrom, Properties: json.RawMessage(`{"method":"retrain"}`), CreatedAt: now}
			if dst != nil {
				e.DstType, e.DstID = "model_version", dst.ID
			} else {
				e.DstRef = ref
			}
			if err := store.AddLineageEdge(ctx, e); err != nil {
				t.Fatalf("AddLineageEdge: %v", err)
			}
			return e
		}
		internal := edge(child, parent, "")
		external := edge(child, nil, "hf://acme/base@main")
		bareEdge := edge(bc, bp, "")
		// A trained_on edge is never a derivation.
		if err := store.AddLineageEdge(ctx, &domain.LineageEdge{ID: domain.NewID(), SrcType: "model_version",
			SrcID: child.ID, Relation: "trained_on", DstRef: "s3://data", CreatedAt: now}); err != nil {
			t.Fatalf("AddLineageEdge trained_on: %v", err)
		}
		for _, in := range []struct {
			v *domain.ModelVersion
			w string
		}{{parent, "w1"}, {child, "w2"}} {
			if err := store.UpsertInsight(ctx, &domain.VersionInsight{VersionID: in.v.ID, Source: domain.SourceDeclared,
				Hashes: domain.Hashes{Topology: "t", Shape: "s", Dtype: "d", Weights: in.w}, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatalf("UpsertInsight: %v", err)
			}
		}
		if err := store.CreateChangePlan(ctx, plan(planned, 0), ""); err != nil {
			t.Fatalf("CreateChangePlan: %v", err)
		}

		find := func(rows []*domain.PlanDerivationRow, edgeID string) *domain.PlanDerivationRow {
			for _, r := range rows {
				if r.Edge.ID == edgeID {
					return r
				}
			}
			return nil
		}

		rows, err := store.ListPlanDerivations(ctx, "", "")
		if err != nil {
			t.Fatalf("ListPlanDerivations: %v", err)
		}
		if find(rows, bareEdge.ID) != nil {
			t.Fatal("an unnarrowed scan covers only planned models")
		}
		d := find(rows, internal.ID)
		if d == nil {
			t.Fatal("a planned model's derivation must appear")
		}
		if d.Model != "plan-scan" || d.Version != "1.1.0" || d.ModelID != planned.ID || d.PublishedAt != child.CreatedAt {
			t.Fatalf("subject: %+v", d)
		}
		if d.ParentModel != "plan-scan" || d.ParentVersion != "1.0.0" {
			t.Fatalf("parent: %q %q", d.ParentModel, d.ParentVersion)
		}
		if d.FromHashes.Weights != "w1" || d.ToHashes.Weights != "w2" {
			t.Fatalf("hashes per side: from=%+v to=%+v", d.FromHashes, d.ToHashes)
		}
		if string(d.Edge.Properties) != `{"method":"retrain"}` {
			t.Fatalf("declared method: %s", d.Edge.Properties)
		}
		x := find(rows, external.ID)
		if x == nil || x.ParentVersion != "" || x.FromHashes != (domain.Hashes{}) || x.Edge.DstRef != "hf://acme/base@main" {
			t.Fatalf("external ref row: %+v", x)
		}
		for _, r := range rows {
			if r.Edge.Relation != domain.RelDerivedFrom {
				t.Fatalf("only derived_from edges: %+v", r.Edge)
			}
		}

		// Narrowed to a model, the scan returns its edges plan or no plan — the per-version
		// read needs them to say no_plan.
		rows, err = store.ListPlanDerivations(ctx, bare.ID, "")
		if err != nil || len(rows) != 1 || rows[0].Edge.ID != bareEdge.ID {
			t.Fatalf("narrowed to an unplanned model: %v %+v", err, rows)
		}
		rows, err = store.ListPlanDerivations(ctx, planned.ID, parent.ID)
		if err != nil || len(rows) != 0 {
			t.Fatalf("a version with no derived_from edge: %v %+v", err, rows)
		}
		rows, err = store.ListPlanDerivations(ctx, planned.ID, child.ID)
		if err != nil || len(rows) != 2 {
			t.Fatalf("narrowed to a version: %v %+v", err, rows)
		}
	})

	t.Run("protocol lookup and cascade", func(t *testing.T) {
		m := mk("plan-cascade")
		v := mkVersion(m.ID, "1.0.0")
		mustCreateVersion(t, store, v)
		a := &domain.Artifact{ID: domain.NewID(), VersionID: v.ID, Kind: domain.KindDoc, Name: "pccp.pdf",
			URI: "s3://b/pccp.pdf", CreatedAt: now, UpdatedAt: now}
		if err := store.CreateArtifact(ctx, a); err != nil {
			t.Fatalf("CreateArtifact: %v", err)
		}
		got, err := store.GetArtifactByID(ctx, a.ID)
		if err != nil || got.VersionID != v.ID || got.Kind != domain.KindDoc {
			t.Fatalf("GetArtifactByID: %v %+v", err, got)
		}
		if _, err := store.GetArtifactByID(ctx, "missing"); !domain.IsNotFound(err) {
			t.Fatalf("missing artifact: %v", err)
		}

		p := plan(m, 0)
		p.ProtocolArtifactID = a.ID
		if err := store.CreateChangePlan(ctx, p, ""); err != nil {
			t.Fatalf("CreateChangePlan: %v", err)
		}
		// No foreign key on the document: deleting it leaves the plan saying what it rested on.
		if err := store.DeleteArtifact(ctx, a.ID); err != nil {
			t.Fatalf("DeleteArtifact: %v", err)
		}
		if got, _ := store.ListChangePlans(ctx, m.ID); len(got) != 1 || got[0].ProtocolArtifactID != a.ID {
			t.Fatalf("plan after its document is deleted: %+v", got)
		}

		if err := store.CreateChangePlan(ctx, plan(&domain.Model{ID: "missing"}, 0), ""); !domain.IsNotFound(err) {
			t.Fatalf("plan on a missing model: %v", err)
		}
		if err := store.DeleteModel(ctx, m.ID); err != nil {
			t.Fatalf("DeleteModel: %v", err)
		}
		if got, _ := store.ListChangePlans(ctx, m.ID); len(got) != 0 {
			t.Fatalf("plans cascade with the model: %+v", got)
		}
	})
}
