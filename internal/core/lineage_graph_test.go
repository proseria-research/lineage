package core_test

import (
	"context"
	"testing"

	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

// chain builds:  v3 --derived_from--> v2 --derived_from--> v1 --trained_on--> s3://ds/q2
func lineageChain(t *testing.T) *core.Service {
	t.Helper()
	ctx := context.Background()
	s := newSvc(t)
	if _, err := s.CreateModel(ctx, "me", core.CreateModelInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		if _, _, err := s.PublishVersion(ctx, "me", "m", core.PublishVersionInput{Name: v}); err != nil {
			t.Fatal(err)
		}
	}
	add := func(ver string, in core.LineageInput) {
		if _, err := s.AddLineage(ctx, "me", "m", ver, in); err != nil {
			t.Fatal(err)
		}
	}
	add("2.0.0", lin("derived_from", "1.0.0", ""))
	add("3.0.0", lin("derived_from", "2.0.0", ""))
	add("1.0.0", lin("trained_on", "", "s3://ds/q2"))
	return s
}

func lin(rel, ver, uri string) core.LineageInput {
	in := core.LineageInput{Relation: domain.LineageRelation(rel)}
	in.To.Version = ver
	in.To.URI = uri
	return in
}

func labels(g *domain.LineageGraph) map[string]bool {
	m := map[string]bool{}
	for _, n := range g.Nodes {
		m[n.Label] = true
	}
	return m
}

func TestLineageUpstreamAncestry(t *testing.T) {
	ctx := context.Background()
	s := lineageChain(t)
	g, err := s.TraverseLineage(ctx, "m", "3.0.0", core.LineageQuery{Direction: domain.Upstream})
	if err != nil {
		t.Fatal(err)
	}
	l := labels(g)
	for _, want := range []string{"m@3.0.0", "m@2.0.0", "m@1.0.0", "s3://ds/q2"} {
		if !l[want] {
			t.Fatalf("upstream ancestry missing %q; got %v", want, l)
		}
	}
	if len(g.Edges) != 3 {
		t.Fatalf("upstream edges = %d, want 3", len(g.Edges))
	}
}

func TestLineageDepthBound(t *testing.T) {
	ctx := context.Background()
	s := lineageChain(t)
	g, err := s.TraverseLineage(ctx, "m", "3.0.0", core.LineageQuery{Direction: domain.Upstream, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	// depth 1: only the immediate parent 2.0.0.
	if len(g.Edges) != 1 || labels(g)["m@1.0.0"] {
		t.Fatalf("depth=1 should stop at one hop; got %d edges, nodes %v", len(g.Edges), labels(g))
	}
}

func TestLineageDownstreamImpact(t *testing.T) {
	ctx := context.Background()
	s := lineageChain(t)
	g, err := s.TraverseLineage(ctx, "m", "1.0.0", core.LineageQuery{Direction: domain.Downstream})
	if err != nil {
		t.Fatal(err)
	}
	l := labels(g)
	// Impact of 1.0.0: 2.0.0 (derived from it) and transitively 3.0.0.
	if !l["m@2.0.0"] || !l["m@3.0.0"] {
		t.Fatalf("downstream impact missing descendants; got %v", l)
	}
	if l["s3://ds/q2"] {
		t.Fatalf("dataset is upstream of 1.0.0, must not appear downstream: %v", l)
	}
}

func TestLineageRelationFilter(t *testing.T) {
	ctx := context.Background()
	s := lineageChain(t)
	g, err := s.TraverseLineage(ctx, "m", "3.0.0", core.LineageQuery{
		Direction: domain.Upstream,
		Relations: map[domain.LineageRelation]bool{domain.RelDerivedFrom: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// trained_on filtered out ⇒ the dataset ref is not reached.
	if labels(g)["s3://ds/q2"] {
		t.Fatalf("relation filter should exclude trained_on dataset: %v", labels(g))
	}
}

// A cycle (1.0.0 derived_from 3.0.0, closing the loop) must not hang or duplicate nodes.
func TestLineageCycleSafe(t *testing.T) {
	ctx := context.Background()
	s := lineageChain(t)
	if _, err := s.AddLineage(ctx, "me", "m", "1.0.0", lin("derived_from", "3.0.0", "")); err != nil {
		t.Fatal(err)
	}
	g, err := s.TraverseLineage(ctx, "m", "3.0.0", core.LineageQuery{Direction: domain.Upstream, Depth: 25})
	if err != nil {
		t.Fatal(err)
	}
	// Three versions + the dataset ref, each once, despite the cycle.
	versionNodes := 0
	for _, n := range g.Nodes {
		if n.Type == "model_version" {
			versionNodes++
		}
	}
	if versionNodes != 3 {
		t.Fatalf("cycle produced %d version nodes, want 3 (deduped): %v", versionNodes, labels(g))
	}
}
