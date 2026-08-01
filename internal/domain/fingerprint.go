package domain

// Architecture-fingerprint classification (§11.4). The registry computes this over hashes
// producers submitted — it never reads an artifact to derive them. Because the four hash
// levels are separable, the verdict is a table lookup rather than a heuristic (§11.4.1).

type Verdict string

const (
	VerdictIdentical     Verdict = "identical"     // repackage / re-publish
	VerdictReweighted    Verdict = "reweighted"    // fine-tune, continued training, RL
	VerdictRecast        Verdict = "recast"        // same shape, different precision
	VerdictRescaled      Verdict = "rescaled"      // same family, different width or depth
	VerdictRearchitected Verdict = "rearchitected" // new blocks or backbone
	VerdictUnknown       Verdict = "unknown"       // the submitted facts do not determine one
)

// HashCmp reports one level of the ladder for both sides of a diff.
type HashCmp struct {
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Changed *bool  `json:"changed,omitempty"` // nil when either side is absent
	Present bool   `json:"present"`           // both sides supplied this level
}

// Classification is the verdict plus the evidence behind it. When the facts narrow the
// answer without settling it, Verdict is unknown and Candidates names the survivors —
// the registry does not infer a verdict from absent data (§11.4.3, §11.6.2).
type Classification struct {
	Verdict    Verdict            `json:"verdict"`
	Candidates []Verdict          `json:"candidates,omitempty"`
	Missing    []string           `json:"missing,omitempty"`
	Hashes     map[string]HashCmp `json:"hashes"`
}

// Classify applies the §11.4.1 lookup to two hash ladders.
func Classify(from, to Hashes) Classification {
	c := Classification{Hashes: map[string]HashCmp{
		"topology": cmp(from.Topology, to.Topology),
		"shape":    cmp(from.Shape, to.Shape),
		"dtype":    cmp(from.Dtype, to.Dtype),
		"weights":  cmp(from.Weights, to.Weights),
	}}

	// Topology is the discriminator the whole table hangs off; without it nothing follows.
	if !c.Hashes["topology"].Present {
		c.Verdict = VerdictUnknown
		c.Missing = []string{"topology"}
		return c
	}
	if *c.Hashes["topology"].Changed {
		c.Verdict = VerdictRearchitected
		return c
	}

	if !c.Hashes["shape"].Present {
		c.Verdict = VerdictUnknown
		c.Missing = []string{"shape"}
		c.Candidates = []Verdict{VerdictIdentical, VerdictReweighted, VerdictRecast, VerdictRescaled}
		return c
	}
	if *c.Hashes["shape"].Changed {
		c.Verdict = VerdictRescaled
		return c
	}

	if !c.Hashes["dtype"].Present {
		c.Verdict = VerdictUnknown
		c.Missing = []string{"dtype"}
		c.Candidates = []Verdict{VerdictIdentical, VerdictReweighted, VerdictRecast}
		return c
	}
	if *c.Hashes["dtype"].Changed {
		c.Verdict = VerdictRecast
		return c
	}

	// Topology, shape, and dtype all match. Only the weights hash separates an untouched
	// republish from a fine-tune, so without it the diff narrows and says so (§11.4.3).
	if !c.Hashes["weights"].Present {
		c.Verdict = VerdictUnknown
		c.Missing = []string{"weights"}
		c.Candidates = []Verdict{VerdictIdentical, VerdictReweighted}
		return c
	}
	if *c.Hashes["weights"].Changed {
		c.Verdict = VerdictReweighted
	} else {
		c.Verdict = VerdictIdentical
	}
	return c
}

func cmp(from, to string) HashCmp {
	h := HashCmp{From: from, To: to, Present: from != "" && to != ""}
	if h.Present {
		changed := from != to
		h.Changed = &changed
	}
	return h
}
