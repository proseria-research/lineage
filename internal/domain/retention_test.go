package domain

import "testing"

// now is an arbitrary fixed clock; nothing here depends on the wall time.
const rtNow int64 = 1_800_000_000_000

func daysAgo(n int64) int64 { return rtNow - n*millisPerDay }

// reasonOf pulls details.reason, which is where §19.7.4 puts the specificity.
func reasonOf(t *testing.T, e *Error) string {
	t.Helper()
	if e == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if e.Code != CodeFailedPrecondition {
		t.Fatalf("code = %q, want %q", e.Code, CodeFailedPrecondition)
	}
	r, _ := e.Details["reason"].(string)
	return r
}

func TestCheckDeletable_NoHoldNoFloor(t *testing.T) {
	// 0 disables the floor and is a real choice (§19.4) — a brand-new subject deletes.
	if err := CheckDeletable(DeleteGuard{NewestCreatedAt: rtNow}, RetentionConfig{}, rtNow); err != nil {
		t.Fatalf("want nil with the floor disabled, got %v", err)
	}
}

func TestCheckDeletable_HoldRefuses(t *testing.T) {
	g := DeleteGuard{Hold: Hold{Held: true, HeldSince: daysAgo(3), HeldBy: "counsel@acme.example"}}
	err := CheckDeletable(g, RetentionConfig{}, rtNow)
	if got := reasonOf(t, err); got != RefusedLegalHold {
		t.Fatalf("reason = %q, want %q", got, RefusedLegalHold)
	}
	// Provenance travels with the refusal: the caller learns who to go and ask.
	if err.Details["heldBy"] != "counsel@acme.example" {
		t.Fatalf("heldBy = %v", err.Details["heldBy"])
	}
	if err.Details["heldSince"] != daysAgo(3) {
		t.Fatalf("heldSince = %v", err.Details["heldSince"])
	}
	if _, ok := err.Details["heldSubject"]; ok {
		t.Fatal("heldSubject must be absent when the subject is held in its own right")
	}
}

func TestCheckDeletable_InheritedHoldNamesTheAncestor(t *testing.T) {
	// §19.3.1: refusing a version under a held model has to say which model, or the caller
	// is told no and given nothing to release.
	g := DeleteGuard{
		Hold:        Hold{Held: true, HeldSince: daysAgo(3), HeldBy: "counsel@acme.example"},
		HeldSubject: "model/fraud-detector",
	}
	err := CheckDeletable(g, RetentionConfig{}, rtNow)
	if got := reasonOf(t, err); got != RefusedLegalHold {
		t.Fatalf("reason = %q", got)
	}
	if err.Details["heldSubject"] != "model/fraud-detector" {
		t.Fatalf("heldSubject = %v", err.Details["heldSubject"])
	}
}

func TestCheckDeletable_FloorRefusesYoungSubject(t *testing.T) {
	cfg := RetentionConfig{MinArchivedVersionDays: 3650}
	err := CheckDeletable(DeleteGuard{NewestCreatedAt: daysAgo(30)}, cfg, rtNow)
	if got := reasonOf(t, err); got != RefusedRetentionFloor {
		t.Fatalf("reason = %q, want %q", got, RefusedRetentionFloor)
	}
	if err.Details["floorDays"] != 3650 {
		t.Fatalf("floorDays = %v", err.Details["floorDays"])
	}
	if err.Details["ageDays"] != int64(30) {
		t.Fatalf("ageDays = %v", err.Details["ageDays"])
	}
}

func TestCheckDeletable_FloorPassesOldSubject(t *testing.T) {
	cfg := RetentionConfig{MinArchivedVersionDays: 30}
	if err := CheckDeletable(DeleteGuard{NewestCreatedAt: daysAgo(31)}, cfg, rtNow); err != nil {
		t.Fatalf("31 days old against a 30-day floor should delete, got %v", err)
	}
	// The boundary: a subject exactly at the floor has *met* it. 30 < 30 is false.
	if err := CheckDeletable(DeleteGuard{NewestCreatedAt: daysAgo(30)}, cfg, rtNow); err != nil {
		t.Fatalf("exactly at the floor should delete, got %v", err)
	}
	if err := CheckDeletable(DeleteGuard{NewestCreatedAt: daysAgo(29)}, cfg, rtNow); err == nil {
		t.Fatal("one day short of the floor should refuse")
	}
}

func TestCheckDeletable_HoldWinsOverFloor(t *testing.T) {
	// Both apply. The caller hears about the hold, because that is the one a human decided
	// and can release; the floor merely expires.
	g := DeleteGuard{Hold: Hold{Held: true, HeldBy: "counsel@acme.example"}, NewestCreatedAt: daysAgo(1)}
	err := CheckDeletable(g, RetentionConfig{MinArchivedVersionDays: 3650}, rtNow)
	if got := reasonOf(t, err); got != RefusedLegalHold {
		t.Fatalf("reason = %q, want %q", got, RefusedLegalHold)
	}
}

func TestCheckDeletable_FloorMeasuresTheYoungestRecord(t *testing.T) {
	// The reason NewestCreatedAt exists. A decade-old model that published a version
	// yesterday must not be deletable: the cascade would destroy a one-day-old record.
	cfg := RetentionConfig{MinArchivedVersionDays: 3650}
	if err := CheckDeletable(DeleteGuard{NewestCreatedAt: daysAgo(1)}, cfg, rtNow); err == nil {
		t.Fatal("a delete destroying a one-day-old record must be refused")
	}
}

func TestRetentionConfigValidate(t *testing.T) {
	if err := DefaultRetention.Validate(); err != nil {
		t.Fatalf("the shipped default must validate: %v", err)
	}
	if err := (RetentionConfig{}).Validate(); err != nil {
		t.Fatalf("all-zero (floors disabled) is a valid choice: %v", err)
	}
	for _, c := range []RetentionConfig{{MinAuditAgeDays: -1}, {MinArchivedVersionDays: -1}} {
		if err := c.Validate(); err == nil {
			t.Fatalf("negative floor must be rejected, not read as disabled: %+v", c)
		} else if err.Code != CodeInvalidArgument {
			t.Fatalf("code = %q, want %q", err.Code, CodeInvalidArgument)
		}
	}
}
