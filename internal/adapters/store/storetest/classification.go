package storetest

import (
	"context"
	"sort"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// runClassifications holds every adapter to identical risk-classification behavior
// (§16.7.1): one row per (model, regime), replaced whole on write, absent means
// `unclassified` rather than an error, and one model's rows never leak into another's.
//
// It takes a domain.ComplianceStore rather than the full MetadataStore because the SQL
// adapters implement it in later steps; the suite skips adapters that do not yet.
func runClassifications(t *testing.T, cs domain.ComplianceStore, modelID, otherModelID string) {
	t.Helper()
	ctx := context.Background()

	// Absent is `unclassified` (§16.4) — reported as NotFound, which the core reads as a
	// state rather than surfacing as a failure.
	if _, err := cs.GetClassification(ctx, modelID, domain.RegimeEUAIAct); err == nil {
		t.Fatal("expected not_found before any classification is recorded")
	} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}
	if list, err := cs.ListClassifications(ctx, modelID); err != nil || len(list) != 0 {
		t.Fatalf("ListClassifications on an unclassified model: %v len=%d", err, len(list))
	}

	const wantDue int64 = 1_804_500_000_000
	due := wantDue
	c := &domain.RiskClassification{
		ModelID:           modelID,
		Regime:            domain.RegimeEUAIAct,
		EUSystemRiskClass: domain.EUClassHighAnnexIII,
		EUGpaiTier:        domain.EUGpaiNone,
		IntendedPurpose:   "Scores card-not-present transactions for manual review.",
		Basis:             "Annex III §5(b) — creditworthiness adjacent.",
		ClassifiedAt:      1_773_000_000_000,
		ClassifiedBy:      "risk@acme.example",
		ReviewDueAt:       &due,
	}
	if err := cs.PutClassification(ctx, c); err != nil {
		t.Fatalf("PutClassification: %v", err)
	}

	got, err := cs.GetClassification(ctx, modelID, domain.RegimeEUAIAct)
	if err != nil {
		t.Fatalf("GetClassification: %v", err)
	}
	if got.EUSystemRiskClass != domain.EUClassHighAnnexIII || got.EUGpaiTier != domain.EUGpaiNone {
		t.Fatalf("enum round-trip: class=%q tier=%q", got.EUSystemRiskClass, got.EUGpaiTier)
	}
	if got.IntendedPurpose != c.IntendedPurpose || got.Basis != c.Basis {
		t.Fatalf("narrative round-trip: %+v", got)
	}
	if got.ClassifiedAt != c.ClassifiedAt || got.ClassifiedBy != c.ClassifiedBy {
		t.Fatalf("anchor round-trip: at=%d by=%q", got.ClassifiedAt, got.ClassifiedBy)
	}
	if got.ReviewDueAt == nil || *got.ReviewDueAt != wantDue {
		t.Fatalf("reviewDueAt round-trip: %v", got.ReviewDueAt)
	}

	// The store must not alias the caller's pointer: mutating the submitted struct after a
	// write must not reach stored state.
	*c.ReviewDueAt = 1
	c.Basis = "mutated after write"
	if again, _ := cs.GetClassification(ctx, modelID, domain.RegimeEUAIAct); again.Basis != got.Basis || *again.ReviewDueAt != wantDue {
		t.Fatalf("store aliased the caller's struct: %+v", again)
	}

	// PUT replaces whole (§16.8). Fields omitted on the second write are *cleared*, not
	// merged from the first — an old basis surviving under a new class is exactly what PUT
	// semantics exist to prevent.
	replacement := &domain.RiskClassification{
		ModelID:           modelID,
		Regime:            domain.RegimeEUAIAct,
		EUSystemRiskClass: domain.EUClassLimited,
		EUGpaiTier:        domain.EUGpaiNone,
		IntendedPurpose:   "Reclassified after counsel review.",
		ClassifiedAt:      1_776_000_000_000,
		ClassifiedBy:      "counsel@acme.example",
	}
	if err := cs.PutClassification(ctx, replacement); err != nil {
		t.Fatalf("PutClassification replace: %v", err)
	}
	got, err = cs.GetClassification(ctx, modelID, domain.RegimeEUAIAct)
	if err != nil {
		t.Fatalf("GetClassification after replace: %v", err)
	}
	if got.Basis != "" {
		t.Fatalf("PUT left a stale basis under a new class: %q", got.Basis)
	}
	if got.ReviewDueAt != nil {
		t.Fatalf("PUT left a stale reviewDueAt: %v", *got.ReviewDueAt)
	}
	if got.EUSystemRiskClass != domain.EUClassLimited || got.ClassifiedAt != replacement.ClassifiedAt {
		t.Fatalf("replace did not take: %+v", got)
	}

	// Still exactly one row: a replace is not an append.
	if list, err := cs.ListClassifications(ctx, modelID); err != nil || len(list) != 1 {
		t.Fatalf("ListClassifications after replace: %v len=%d", err, len(list))
	}

	// Another model's rows are its own.
	if list, err := cs.ListClassifications(ctx, otherModelID); err != nil || len(list) != 0 {
		t.Fatalf("classification leaked across models: %v len=%d", err, len(list))
	}

	// The other half of the key — that a write under one regime leaves another regime's
	// ClassifiedAt anchor alone (§16.3.2) — is asserted in M17, which adds the second regime
	// there is currently nothing to isolate from.
}

// RunSQLConstraints asserts the schema-level guarantees a SQL engine enforces and the memory
// adapter structurally cannot, so it is called from the SQLite and Postgres tests rather than
// from Run.
//
// The subject is the §16.7.1 CHECK tying each enum group to its discriminator. Domain
// validation already rejects these writes (§16.6); the constraint is the second line, for a
// caller that reaches the store without passing through it.
func RunSQLConstraints(t *testing.T, store domain.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	now := domain.NowMillis()

	m := &domain.Model{ID: domain.NewID(), Name: "constraint-subject", State: domain.StateActive, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	cases := []struct {
		name string
		c    domain.RiskClassification
	}{{
		// The EU branch requires its own group populated. A null class here is how an
		// `unclassified` model would look if `unclassified` were an absence rather than a
		// value — and §16.3 is emphatic that it is a value.
		name: "eu_ai_act row with a null eu_system_risk_class",
		c:    domain.RiskClassification{ModelID: m.ID, Regime: domain.RegimeEUAIAct, EUGpaiTier: domain.EUGpaiNone},
	}, {
		name: "eu_ai_act row with a null eu_gpai_tier",
		c:    domain.RiskClassification{ModelID: m.ID, Regime: domain.RegimeEUAIAct, EUSystemRiskClass: domain.EUClassMinimal},
	}, {
		// Today the CHECK has one branch, so it also refuses a regime this build does not
		// define. M17 adds the `mrm` branch alongside it (`20.8.1`).
		name: "a regime with no branch in the CHECK",
		c:    domain.RiskClassification{ModelID: m.ID, Regime: "uk_ai_bill", ClassifiedAt: now},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.c
			c.ClassifiedAt = now
			if err := store.PutClassification(ctx, &c); err == nil {
				t.Fatal("expected the CHECK to reject this row, but the write succeeded")
			}
		})
	}

	// A rejected write leaves nothing behind — the delete-then-insert runs in one transaction.
	if list, err := store.ListClassifications(ctx, m.ID); err != nil || len(list) != 0 {
		t.Fatalf("rejected writes left rows behind: %v len=%d", err, len(list))
	}
}

// RunInventory holds every adapter to identical §16.8.2 inventory semantics: the LEFT join
// keeps unclassified models, a stored-enum filter drops them, and the drift facts come back
// alongside so the caller can compute state without a second query per model.
//
// It builds its own models so it can run after Run's subject has been deleted.
func RunInventory(t *testing.T, store domain.MetadataStore) {
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
	high := mk("inv-high")
	minimal := mk("inv-minimal")
	mk("inv-unclassified") // deliberately never classified

	classify := func(m *domain.Model, class domain.EUSystemRiskClass) {
		c := &domain.RiskClassification{
			ModelID: m.ID, Regime: domain.RegimeEUAIAct,
			EUSystemRiskClass: class, EUGpaiTier: domain.EUGpaiNone,
			IntendedPurpose: "p", Basis: "b", ClassifiedAt: now, ClassifiedBy: "risk@acme.example",
		}
		if err := store.PutClassification(ctx, c); err != nil {
			t.Fatalf("PutClassification %s: %v", m.Name, err)
		}
	}
	classify(high, domain.EUClassHighAnnexIII)
	classify(minimal, domain.EUClassMinimal)

	rowsByName := func(f domain.ClassificationFilter) map[string]*domain.ModelInventoryRow {
		f.Regime = domain.RegimeEUAIAct
		// Scoped by name: Run and RunSQLConstraints leave their own models in the store, and
		// this exercises the shared model-level clause on every assertion rather than once.
		rows, err := store.ListInventory(ctx, domain.ListOptions{Q: "inv-"}, f)
		if err != nil {
			t.Fatalf("ListInventory: %v", err)
		}
		out := map[string]*domain.ModelInventoryRow{}
		for _, r := range rows {
			out[r.Model.Name] = r
		}
		return out
	}

	// Unfiltered: every model comes back, classified or not. `unclassified` is a state to
	// filter *for*, not an absence to drop (§16.4).
	all := rowsByName(domain.ClassificationFilter{})
	for _, name := range []string{"inv-high", "inv-minimal", "inv-unclassified"} {
		if all[name] == nil {
			t.Fatalf("unfiltered inventory missing %s: %v", name, keysOf(all))
		}
	}
	if all["inv-unclassified"].Classification != nil {
		t.Fatal("a never-classified model came back with a classification row")
	}
	if all["inv-high"].Classification == nil ||
		all["inv-high"].Classification.EUSystemRiskClass != domain.EUClassHighAnnexIII {
		t.Fatalf("classification not joined: %+v", all["inv-high"].Classification)
	}

	// A stored-enum filter selects exactly one and drops the unclassified model — asking for
	// high_annex_iii cannot mean "and also everything nobody classified".
	only := rowsByName(domain.ClassificationFilter{EUSystemRiskClass: domain.EUClassHighAnnexIII})
	if len(only) != 1 || only["inv-high"] == nil {
		t.Fatalf("class filter returned %v, want just inv-high", keysOf(only))
	}
	if tiered := rowsByName(domain.ClassificationFilter{EUGpaiTier: domain.EUGpaiNone}); len(tiered) != 2 {
		t.Fatalf("tier filter returned %v, want the two classified models", keysOf(tiered))
	}
	if none := rowsByName(domain.ClassificationFilter{EUSystemRiskClass: domain.EUClassProhibited}); len(none) != 0 {
		t.Fatalf("filter matching nothing returned %v", keysOf(none))
	}

	// A narrower model-level filter still applies, through the same shared clause.
	named, err := store.ListInventory(ctx, domain.ListOptions{Q: "inv-high"}, domain.ClassificationFilter{Regime: domain.RegimeEUAIAct})
	if err != nil || len(named) != 1 || named[0].Model.Name != "inv-high" {
		t.Fatalf("q filter: %v rows=%d", err, len(named))
	}

	// Drift facts ride along: publishing a version must show up without a second query.
	if all["inv-high"].Facts.LatestVersionCreatedAt != 0 {
		t.Fatalf("no versions yet, got %d", all["inv-high"].Facts.LatestVersionCreatedAt)
	}
	v := &domain.ModelVersion{
		ID: domain.NewID(), ModelID: high.ID, Name: "1.0.0", Stage: domain.StageProduction,
		CreatedAt: now + 5_000, UpdatedAt: now + 5_000,
	}
	if err := store.CreateVersion(ctx, v); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	after := rowsByName(domain.ClassificationFilter{})
	f := after["inv-high"].Facts
	if f.LatestVersionCreatedAt != now+5_000 {
		t.Fatalf("latestVersionCreatedAt = %d, want %d", f.LatestVersionCreatedAt, now+5_000)
	}
	if f.LatestProductionUpdatedAt != now+5_000 {
		t.Fatalf("latestProductionUpdatedAt = %d, want %d", f.LatestProductionUpdatedAt, now+5_000)
	}
	// One model's versions must not leak into another's facts — the join groups by model.
	if after["inv-minimal"].Facts.LatestVersionCreatedAt != 0 {
		t.Fatalf("version facts leaked across models: %+v", after["inv-minimal"].Facts)
	}
	// A row per model, not a row per version.
	if len(after) != 3 {
		t.Fatalf("expected 3 rows after adding a version, got %v", keysOf(after))
	}

}

func keysOf(m map[string]*domain.ModelInventoryRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
