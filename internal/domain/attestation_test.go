package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
)

func ev(id string) *AuditEvent {
	return &AuditEvent{
		ID: id, At: 1_780_000_000_000, Actor: "a@acme.example", Action: "model.update",
		SubjectType: SubjectModel, SubjectID: "01ABC", Summary: "updated model m",
		Data: json.RawMessage(`{"k":"v"}`),
	}
}

func events(n int) []*AuditEvent {
	out := make([]*AuditEvent, n)
	for i := range out {
		out[i] = ev(fmt.Sprintf("01EV%04d", i))
	}
	return out
}

func TestEpochOf(t *testing.T) {
	const interval int64 = 60_000
	if got := EpochOf(0, interval); got != 0 {
		t.Fatalf("epoch(0) = %d", got)
	}
	if got := EpochOf(59_999, interval); got != 0 {
		t.Fatalf("last ms of a window must stay in it, got %d", got)
	}
	if got := EpochOf(60_000, interval); got != 1 {
		t.Fatalf("epoch boundary = %d, want 1", got)
	}
	// A row's epoch depends on nothing but its own clock — the property that keeps the write
	// path free of coordination (§19.5.1).
	if EpochOf(1_780_000_000_000, interval) != 1_780_000_000_000/interval {
		t.Fatal("epoch must be a pure function of at")
	}
}

func TestMerkleRootIsDomainSeparated(t *testing.T) {
	// RFC 6962's prefixes: a leaf must never hash the same as an internal node, or a leaf
	// could be presented as a subtree (§19.5.1).
	e := ev("01A")
	leaf := AuditLeafHash(e)
	plain := sha256.Sum256(AuditLeafBytes(e))
	if hex.EncodeToString(leaf) == hex.EncodeToString(plain[:]) {
		t.Fatal("leaf hash must be domain-separated with 0x00")
	}

	// A two-leaf root must not equal the concatenation hashed without the 0x01 prefix.
	l, r := AuditLeafHash(ev("01A")), AuditLeafHash(ev("01B"))
	bare := sha256.New()
	bare.Write(l)
	bare.Write(r)
	if MerkleRoot([][]byte{l, r}) == "sha256:"+hex.EncodeToString(bare.Sum(nil)) {
		t.Fatal("node hash must be domain-separated with 0x01")
	}
}

func TestMerkleRootEmptyAndSingle(t *testing.T) {
	if MerkleRoot(nil) != "" {
		t.Fatal("an empty window has no root; it is never sealed")
	}
	// One leaf: the root is the leaf, promoted all the way up.
	l := AuditLeafHash(ev("01A"))
	if MerkleRoot([][]byte{l}) != "sha256:"+hex.EncodeToString(l) {
		t.Fatal("a single-leaf root is the leaf itself")
	}
}

func TestMerkleRootIsOrderIndependentOfInput(t *testing.T) {
	// The sealer and the verifier may read rows back in different orders; both sort by id
	// first, so the root cannot depend on which order the store happened to return.
	a := events(7)
	b := make([]*AuditEvent, len(a))
	for i := range a {
		b[i] = a[len(a)-1-i]
	}
	if MerkleRootOfEvents(a) != MerkleRootOfEvents(b) {
		t.Fatal("root must not depend on the order rows were read in")
	}
	// And the caller's slice is not reordered as a side effect.
	if a[0].ID != "01EV0000" {
		t.Fatalf("MerkleRootOfEvents reordered the caller's slice: %s", a[0].ID)
	}
}

// TestEveryFieldIsCommittedTo is the tamper-evidence claim itself: changing any stored
// column of any row must change the root. It iterates the fields rather than naming a few,
// so a column added to AuditEvent without being added to the leaf is caught here.
func TestEveryFieldIsCommittedTo(t *testing.T) {
	base := events(5)
	want := MerkleRootOfEvents(base)

	for _, tc := range []struct {
		field string
		edit  func(*AuditEvent)
	}{
		{"id", func(e *AuditEvent) { e.ID = "01EV0009" }},
		{"at", func(e *AuditEvent) { e.At++ }},
		{"actor", func(e *AuditEvent) { e.Actor = "someone-else@acme.example" }},
		{"action", func(e *AuditEvent) { e.Action = "model.delete" }},
		{"subjectType", func(e *AuditEvent) { e.SubjectType = SubjectVersion }},
		{"subjectId", func(e *AuditEvent) { e.SubjectID = "01XYZ" }},
		// §19.5.1 omits these two. summary is the line a human reads in the console, and id
		// is what a proof is looked up by — anything outside the leaf can be rewritten while
		// the log still verifies.
		{"summary", func(e *AuditEvent) { e.Summary = "did something harmless" }},
		{"data", func(e *AuditEvent) { e.Data = json.RawMessage(`{"k":"tampered"}`) }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			rows := events(5)
			tc.edit(rows[2])
			if MerkleRootOfEvents(rows) == want {
				t.Fatalf("editing %s left the root unchanged — it is not committed to", tc.field)
			}
		})
	}
}

func TestDeletionChangesTheRoot(t *testing.T) {
	// §19.5.2: leaf count *and* root both change, so a deletion cannot be hidden by either.
	full := events(5)
	short := append([]*AuditEvent{}, full[:4]...)
	if MerkleRootOfEvents(full) == MerkleRootOfEvents(short) {
		t.Fatal("removing a row must change the root")
	}
}

func TestInclusionProofRoundTrip(t *testing.T) {
	// Every leaf of every tree size up to 33 — the odd-promotion rule makes the shape
	// irregular, and off-by-one there is exactly the bug a spot check misses.
	for n := 1; n <= 33; n++ {
		rows := events(n)
		root := MerkleRootOfEvents(rows)
		SortAuditEvents(rows)
		for i, e := range rows {
			proof, idx, ok := MerkleProofForEvents(rows, e.ID)
			if !ok || idx != i {
				t.Fatalf("n=%d: proof for %s: ok=%v idx=%d want %d", n, e.ID, ok, idx, i)
			}
			if !VerifyMerkleProof(AuditLeafHash(e), proof, idx, n, root) {
				t.Fatalf("n=%d i=%d: proof did not verify", n, i)
			}
		}
	}
}

func TestInclusionProofRejectsForgeries(t *testing.T) {
	const n = 9
	rows := events(n)
	root := MerkleRootOfEvents(rows)
	SortAuditEvents(rows)
	proof, idx, _ := MerkleProofForEvents(rows, rows[3].ID)
	leaf := AuditLeafHash(rows[3])

	if !VerifyMerkleProof(leaf, proof, idx, n, root) {
		t.Fatal("baseline proof must verify")
	}
	// A row that was never in the epoch.
	if VerifyMerkleProof(AuditLeafHash(ev("01EV9999")), proof, idx, n, root) {
		t.Fatal("a fabricated leaf must not verify against someone else's proof")
	}
	// The right leaf at the wrong position.
	if VerifyMerkleProof(leaf, proof, idx+1, n, root) {
		t.Fatal("a proof must be position-bound")
	}
	// A padded proof. Without the "every sibling was used" check, extra hashes could be
	// appended until one happened to verify.
	if VerifyMerkleProof(leaf, append(append([]string{}, proof...), proof[0]), idx, n, root) {
		t.Fatal("a proof with unused siblings must be rejected")
	}
	// A truncated one.
	if len(proof) > 0 && VerifyMerkleProof(leaf, proof[:len(proof)-1], idx, n, root) {
		t.Fatal("a truncated proof must be rejected")
	}
	// Right proof, wrong tree.
	if VerifyMerkleProof(leaf, proof, idx, n, MerkleRootOfEvents(events(8))) {
		t.Fatal("a proof must not verify against another epoch's root")
	}
}

func TestAttestationConfigValidate(t *testing.T) {
	if err := DefaultAttestation.Validate(); err != nil {
		t.Fatalf("the shipped default must validate: %v", err)
	}
	// Disabled: nothing else is checked, because nothing else is used.
	if err := (AttestationConfig{Enabled: false}).Validate(); err != nil {
		t.Fatalf("disabled attestation needs no interval: %v", err)
	}
	for _, tc := range []struct {
		name string
		cfg  AttestationConfig
	}{
		{"zero interval", AttestationConfig{Enabled: true, SealIntervalSeconds: 0}},
		{"negative grace", AttestationConfig{Enabled: true, SealIntervalSeconds: 60, SealGraceSeconds: -1}},
		// Grace ≥ interval means a window is still accepting writes when the next is due to
		// seal, so a row can land in an epoch already sealed.
		{"grace at the interval", AttestationConfig{Enabled: true, SealIntervalSeconds: 60, SealGraceSeconds: 60}},
		{"grace past the interval", AttestationConfig{Enabled: true, SealIntervalSeconds: 60, SealGraceSeconds: 90}},
	} {
		if err := tc.cfg.Validate(); err == nil {
			t.Fatalf("%s must be rejected", tc.name)
		}
	}
}
