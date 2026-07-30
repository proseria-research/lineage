package core

import (
	"context"
	"sort"

	"github.com/proseria-research/lineage/internal/domain"
)

// Lineage graph traversal (§07.3): the queries that make provenance a first-class graph —
// ancestry ("what produced this?") upstream, and impact analysis ("what derives from this?")
// downstream. Implemented as a bounded, cycle-safe breadth-first walk over lineage_edge using
// the store's indexed edge lookup; the walk is one place, tested once, and dialect-agnostic.
// (A per-dialect WITH RECURSIVE can replace the neighbor-walk behind the port for scale.)

const (
	defaultLineageDepth = 3
	maxLineageDepth     = 25
)

// LineageQuery parameterizes a traversal. Relations empty ⇒ follow all edge types.
type LineageQuery struct {
	Direction domain.LineageDirection
	Depth     int
	Relations map[domain.LineageRelation]bool
}

// TraverseLineage walks the lineage graph from model@version and returns the reachable
// subgraph. Bounded by Depth (default 3, capped) and cycle-safe (each version expanded once).
func (s *Service) TraverseLineage(ctx context.Context, model, version string, q LineageQuery) (*domain.LineageGraph, error) {
	root, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	if q.Direction == "" {
		q.Direction = domain.BothDirections
	}
	if !domain.ValidDirection(q.Direction) {
		return nil, domain.Invalid("unknown lineage direction '" + string(q.Direction) + "'")
	}
	depth := q.Depth
	if depth <= 0 {
		depth = defaultLineageDepth
	}
	if depth > maxLineageDepth {
		depth = maxLineageDepth
	}

	nodes := map[string]domain.LineageNode{}
	edges := map[string]*domain.LineageEdge{}
	visited := map[string]bool{}

	type item struct {
		id    string
		depth int
	}
	var queue []item

	// enqueue records a version node (labeled via a by-id lookup) and schedules it once.
	enqueue := func(id string, d int) {
		key := "v:" + id
		if _, ok := nodes[key]; !ok {
			if v, err := s.store.GetVersionByID(ctx, id); err == nil {
				nodes[key] = versionNode(v, d)
			} else {
				nodes[key] = domain.LineageNode{Type: "model_version", ID: id, Label: id, Depth: d}
			}
		}
		if !visited[id] {
			visited[id] = true
			queue = append(queue, item{id, d})
		}
	}

	nodes["v:"+root.ID] = versionNode(root, 0)
	visited[root.ID] = true
	queue = append(queue, item{root.ID, 0})

	up := q.Direction == domain.Upstream || q.Direction == domain.BothDirections
	down := q.Direction == domain.Downstream || q.Direction == domain.BothDirections

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= depth {
			continue
		}
		touching, err := s.store.ListLineage(ctx, cur.id)
		if err != nil {
			return nil, err
		}
		for _, e := range touching {
			if len(q.Relations) > 0 && !q.Relations[e.Relation] {
				continue
			}
			// Upstream: this node is the edge source → the dst is an ancestor (version or ref).
			if up && e.SrcID == cur.id {
				edges[e.ID] = e
				switch {
				case e.DstID != "":
					enqueue(e.DstID, cur.depth+1)
				case e.DstRef != "":
					key := "ext:" + e.DstRef
					if _, ok := nodes[key]; !ok {
						nodes[key] = domain.LineageNode{Type: "external", Ref: e.DstRef, Label: e.DstRef, Depth: cur.depth + 1}
					}
				}
			}
			// Downstream: this node is the edge target → the src derives from it (impact).
			if down && e.DstID == cur.id && e.SrcID != "" {
				edges[e.ID] = e
				enqueue(e.SrcID, cur.depth+1)
			}
		}
	}

	return &domain.LineageGraph{
		Root: "v:" + root.ID, Direction: q.Direction,
		Nodes: nodeSlice(nodes), Edges: edgeSlice(edges),
	}, nil
}

func versionNode(v *domain.ModelVersion, depth int) domain.LineageNode {
	return domain.LineageNode{
		Type: "model_version", ID: v.ID, Model: v.Model, Version: v.Name,
		Stage: v.Stage, Label: v.Model + "@" + v.Name, Depth: depth,
	}
}

func nodeSlice(m map[string]domain.LineageNode) []domain.LineageNode {
	out := make([]domain.LineageNode, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func edgeSlice(m map[string]*domain.LineageEdge) []*domain.LineageEdge {
	out := make([]*domain.LineageEdge, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}
