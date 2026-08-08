package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
)

// Tamper-evident audit — Merkle epoch sealing (§19.5).
//
// The whole design exists to keep coordination off the write path. A per-row hash chain
// forces a total order and serializes every audit write behind one sequence; sealing in
// epochs does not. Rows are written concurrently and unordered, each carrying an epoch
// derived from its own clock, and a background sealer computes one root per closed window.
//
// Everything in this file is a pure function over supplied rows. The sealer, the verifier and
// the inclusion-proof endpoint all call the same three primitives, which is the only way
// "recompute and compare" means anything — a verifier with its own implementation of the
// hash would eventually disagree with the sealer for reasons that have nothing to do with
// tampering.

// Break kinds reported by :verify (§19.7.2). Each names what disagreed, because "the audit
// log is broken" is not actionable and "epoch 29738 has 16 leaves where 17 were sealed" is.
const (
	BreakRootMismatch      = "root_mismatch"       // a row inside the epoch was edited
	BreakLeafCountMismatch = "leaf_count_mismatch" // a row was deleted
	BreakPrevRootMismatch  = "prev_root_mismatch"  // an epoch was removed wholesale
)

// AttestationConfig configures sealing (§19.4).
//
// Enabled defaults **on** (§19.5.2). The reason the per-row chain was opt-in was its write
// cost, and that cost is gone — what remains is one derived integer column and a background
// job. Axiom 7 says auditable by default.
type AttestationConfig struct {
	Enabled bool `json:"enabled"`
	// SealIntervalSeconds is both the sealing period and the bound on the unsealed window
	// (§19.5.3). Rows in the currently-open epoch are not yet protected; this is how long
	// that lasts.
	SealIntervalSeconds int `json:"sealIntervalSeconds"`
	// SealGraceSeconds delays sealing past an epoch's end, so a transaction that began
	// inside the window commits before its epoch closes.
	SealGraceSeconds int `json:"sealGraceSeconds"`
}

// DefaultAttestation is §19.4's shipped configuration.
var DefaultAttestation = AttestationConfig{Enabled: true, SealIntervalSeconds: 60, SealGraceSeconds: 5}

func (c AttestationConfig) IntervalMillis() int64 { return int64(c.SealIntervalSeconds) * 1000 }
func (c AttestationConfig) GraceMillis() int64    { return int64(c.SealGraceSeconds) * 1000 }

// Validate rejects a configuration that would produce a log nobody can verify.
func (c AttestationConfig) Validate() *Error {
	if !c.Enabled {
		return nil
	}
	if c.SealIntervalSeconds <= 0 {
		return Invalid("auditAttestation.sealIntervalSeconds must be positive when attestation is enabled")
	}
	if c.SealGraceSeconds < 0 {
		return Invalid("auditAttestation.sealGraceSeconds must not be negative")
	}
	// A grace longer than the interval means an epoch is still accepting writes when the next
	// one is due to seal, so windows overlap and a row can land in an epoch already sealed.
	if int64(c.SealGraceSeconds) >= int64(c.SealIntervalSeconds) {
		return Invalid("auditAttestation.sealGraceSeconds must be shorter than sealIntervalSeconds, " +
			"or a row can arrive in an epoch that has already been sealed")
	}
	return nil
}

// AuditEpoch is one sealed window (§19.6.1). Append-only and never updated: re-sealing would
// be indistinguishable from tampering.
type AuditEpoch struct {
	Epoch int64  `json:"epoch"`
	Root  string `json:"root"`
	// PrevRoot is the previous *sealed* epoch's root — not epoch−1's. Empty windows are never
	// sealed, so the chain skips them, and a gap in epoch numbers is normal rather than
	// evidence of a removal. What proves nothing was removed is that the chain links.
	PrevRoot  string `json:"prevRoot,omitempty"`
	LeafCount int64  `json:"leafCount"`
	SealedAt  int64  `json:"sealedAt"`
}

// EpochOf assigns a row to a window from its own timestamp (§19.5.1). It reads no other row,
// which is the entire reason the write path needs no coordination.
func EpochOf(at, intervalMillis int64) int64 {
	if intervalMillis <= 0 {
		return 0
	}
	return at / intervalMillis
}

// SortAuditEvents puts rows in leaf order: byte-wise ascending on id (§19.5.1). ULIDs already
// sort by creation time (§02.1), so the order is deterministic without a sequence.
//
// The ordering rule lives here, beside the hashing it feeds, rather than in a store's ORDER
// BY. Two engines agreeing on a collation is not something to leave to chance when the answer
// is a legal attestation.
func SortAuditEvents(events []*AuditEvent) {
	sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
}

// AuditLeafBytes is the canonical byte stream for one row (§19.5.1), fields joined with \x00
// under §11.4.5's common rules: UTF-8, integers as plain decimal.
//
// **This hashes the whole row, including id and summary.** §19.5.1 lists only at, actor,
// action, subject_type, subject_id and data — and anything outside the leaf can be changed
// without detection. `summary` is the line a human reads in the console, so leaving it out
// would allow every audit entry to be silently reworded while the log still verified;
// `id` is what an inclusion proof is looked up by. Both belong in what the root commits to.
//
// `epoch` is deliberately absent: moving a row between epochs changes the leaf set of both,
// so membership already covers it.
func AuditLeafBytes(e *AuditEvent) []byte {
	var b []byte
	add := func(s string) {
		b = append(b, s...)
		b = append(b, 0)
	}
	add(e.ID)
	add(strconv.FormatInt(e.At, 10))
	add(e.Actor)
	add(e.Action)
	add(e.SubjectType)
	add(e.SubjectID)
	add(e.Summary)
	// Data is appended verbatim and last: it is the stored bytes, and re-serializing JSON to
	// canonicalize it here would hash something the row does not contain.
	b = append(b, e.Data...)
	return b
}

// AuditLeafHash is RFC 6962's leaf hash: sha256(0x00 ‖ canonical). The 0x00 prefix is what
// stops a leaf being forged as an internal node.
func AuditLeafHash(e *AuditEvent) []byte {
	h := sha256.New()
	h.Write([]byte{0x00})
	h.Write(AuditLeafBytes(e))
	return h.Sum(nil)
}

// nodeHash is RFC 6962's internal-node hash: sha256(0x01 ‖ left ‖ right).
func nodeHash(l, r []byte) []byte {
	h := sha256.New()
	h.Write([]byte{0x01})
	h.Write(l)
	h.Write(r)
	return h.Sum(nil)
}

// MerkleRoot reduces ordered leaves to one root, `sha256:` + lowercase hex.
//
// An odd node is promoted unchanged to the next level (§19.5.1). That is not RFC 6962's tree
// shape — the RFC splits at the largest power of two below n — but it is the rule this spec
// fixes, and the property that matters against second-preimage attacks is the domain
// separation above, not the split point. Both sides of every comparison run this function,
// so the shape only has to be defined, not conventional.
//
// Zero leaves returns "". An empty window is never sealed, so there is no root to disagree
// with; see AuditEpoch.PrevRoot.
func MerkleRoot(leaves [][]byte) string {
	if len(leaves) == 0 {
		return ""
	}
	level := leaves
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 == len(level) {
				next = append(next, level[i]) // odd node promoted
				continue
			}
			next = append(next, nodeHash(level[i], level[i+1]))
		}
		level = next
	}
	return "sha256:" + hex.EncodeToString(level[0])
}

// MerkleRootOfEvents is the sealer's and the verifier's shared entry point: sort, hash, reduce.
// It copies before sorting so a caller's slice order is not a side effect of asking.
func MerkleRootOfEvents(events []*AuditEvent) string {
	return MerkleRoot(auditLeaves(events))
}

func auditLeaves(events []*AuditEvent) [][]byte {
	ordered := make([]*AuditEvent, len(events))
	copy(ordered, events)
	SortAuditEvents(ordered)
	leaves := make([][]byte, len(ordered))
	for i, e := range ordered {
		leaves[i] = AuditLeafHash(e)
	}
	return leaves
}

// MerkleProof returns the sibling hashes from leaf to root for the leaf at index (§19.7.3).
//
// Which side each sibling is on is not encoded: it follows from the index and the leaf count,
// which the verifier has. A promoted odd node contributes no sibling at that level, so a
// proof is at most ceil(log2(n)) hashes and sometimes fewer.
//
// index out of range returns nil.
func MerkleProof(leaves [][]byte, index int) []string {
	if index < 0 || index >= len(leaves) {
		return nil
	}
	var proof []string
	level := leaves
	i := index
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for j := 0; j < len(level); j += 2 {
			if j+1 == len(level) {
				next = append(next, level[j])
				continue
			}
			next = append(next, nodeHash(level[j], level[j+1]))
		}
		if sib := i ^ 1; sib < len(level) {
			proof = append(proof, "sha256:"+hex.EncodeToString(level[sib]))
		}
		i /= 2
		level = next
	}
	return proof
}

// MerkleProofForEvents builds a proof for one event within its epoch's rows, returning the
// proof and the leaf's index. found is false when the event is not among them.
func MerkleProofForEvents(events []*AuditEvent, id string) (proof []string, index int, found bool) {
	ordered := make([]*AuditEvent, len(events))
	copy(ordered, events)
	SortAuditEvents(ordered)
	index = -1
	for i, e := range ordered {
		if e.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, 0, false
	}
	leaves := make([][]byte, len(ordered))
	for i, e := range ordered {
		leaves[i] = AuditLeafHash(e)
	}
	return MerkleProof(leaves, index), index, true
}

// VerifyMerkleProof recomputes the root from one leaf and its siblings — the point of the
// whole construction, since it lets a third party check one event without reading the log.
//
// It is exported and lives in the domain because it is also the reference a client
// implements against: a proof nobody outside can check is an assertion, not evidence.
func VerifyMerkleProof(leaf []byte, proof []string, index, leafCount int, root string) bool {
	if index < 0 || leafCount <= 0 || index >= leafCount {
		return false
	}
	cur := leaf
	width := leafCount
	p := 0
	for width > 1 {
		hasSibling := index^1 < width
		if hasSibling {
			if p >= len(proof) {
				return false
			}
			sib, err := hex.DecodeString(trimSHA256(proof[p]))
			if err != nil {
				return false
			}
			p++
			if index%2 == 0 {
				cur = nodeHash(cur, sib)
			} else {
				cur = nodeHash(sib, cur)
			}
		}
		index /= 2
		width = (width + 1) / 2
	}
	// Every sibling supplied must have been used: a proof carrying extras is not the proof
	// for this leaf, and accepting it would let one be padded until it verified.
	if p != len(proof) {
		return false
	}
	return "sha256:"+hex.EncodeToString(cur) == root
}

func trimSHA256(s string) string {
	const p = "sha256:"
	if len(s) > len(p) && s[:len(p)] == p {
		return s[len(p):]
	}
	return s
}
