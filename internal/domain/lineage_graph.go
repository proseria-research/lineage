package domain

// Lineage graph traversal types (§07.3). The traversal itself lives in the core (a bounded,
// cycle-safe walk over lineage_edge); these are the shapes it produces.

// LineageDirection selects which way to walk from the start node.
type LineageDirection string

const (
	// Upstream = provenance: "what produced this?" — follow edges out of a version (src→dst):
	// derived_from / trained_on / produced_by lead to base models, datasets, runs.
	Upstream LineageDirection = "upstream"
	// Downstream = impact: "what derives from / was deployed off this?" — reverse walk (dst→src).
	Downstream LineageDirection = "downstream"
	// Both walks provenance and impact together (neighborhood).
	BothDirections LineageDirection = "both"
)

// ValidDirection reports whether d is a known traversal direction.
func ValidDirection(d LineageDirection) bool {
	return d == Upstream || d == Downstream || d == BothDirections
}

// LineageNode is a node in a traversed graph: an internal model_version or an external ref.
type LineageNode struct {
	Type    string `json:"type"` // "model_version" | "external"
	ID      string `json:"id,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Model   string `json:"model,omitempty"`
	Version string `json:"version,omitempty"`
	Stage   Stage  `json:"stage,omitempty"`
	Label   string `json:"label"`
	Depth   int    `json:"depth"` // hops from the root (root = 0)
}

// LineageGraph is the result of a traversal (§07.3).
type LineageGraph struct {
	Root      string           `json:"root"` // node key of the starting version
	Direction LineageDirection `json:"direction"`
	Nodes     []LineageNode    `json:"nodes"`
	Edges     []*LineageEdge   `json:"edges"`
}
