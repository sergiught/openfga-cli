package fga

import (
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"
)

// weightModel exercises every cost path: a direct terminal (cheap), a computed
// chain of increasing depth (moderate -> expensive), and a self-referential
// nested-group relation (recursive / ∞).
func weightModel() *openfga.AuthorizationModel {
	direct := func(types ...string) openfga.RelationMetadata {
		refs := make([]openfga.RelationReference, len(types))
		for i, t := range types {
			refs[i] = openfga.DirectType(t)
		}
		return openfga.RelationMetadata{DirectlyRelatedUserTypes: refs}
	}
	return &openfga.AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{Type: "group", Relations: map[string]openfga.Userset{
				"parent": openfga.This(),
				"member": openfga.Union(openfga.This(), openfga.TupleTo("parent", "member")),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"parent": direct("group"),
				"member": direct("user"),
			}}},
			{Type: "doc", Relations: map[string]openfga.Userset{
				"owner":      openfga.This(),
				"viewer":     openfga.ComputedUserset("owner"),
				"editor":     openfga.ComputedUserset("viewer"),
				"super":      openfga.ComputedUserset("editor"),
				"both":       openfga.Intersection(openfga.ComputedUserset("owner"), openfga.ComputedUserset("editor")),
				"restricted": openfga.Exclusion(openfga.ComputedUserset("viewer"), openfga.ComputedUserset("owner")),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"owner": direct("user"),
			}}},
		},
	}
}

func findRel(t *testing.T, g Graph, typ, rel string) Relation {
	t.Helper()
	for _, td := range g.Types {
		if td.Name != typ {
			continue
		}
		for _, r := range td.Relations {
			if r.Name == rel {
				return r
			}
		}
	}
	t.Fatalf("relation %s#%s not found", typ, rel)
	return Relation{}
}

func TestComputeWeights(t *testing.T) {
	g := ParseModel(weightModel())
	cases := []struct {
		typ, rel  string
		weight    int
		recursive bool
	}{
		{"doc", "owner", 1, false},      // direct -> user
		{"doc", "viewer", 2, false},     // computed owner
		{"doc", "editor", 3, false},     // computed viewer
		{"doc", "super", 4, false},      // computed editor
		{"doc", "both", 4, false},       // intersection(owner=1,editor=3) -> max(inc(1),inc(3))=4
		{"doc", "restricted", 3, false}, // difference(viewer=2,owner=1) -> max(inc(2),inc(1))=3
		{"group", "parent", 1, false},   // direct -> group (terminal)
		{"group", "member", -1, true},   // member -> member via parent (cycle)
	}
	for _, c := range cases {
		r := findRel(t, g, c.typ, c.rel)
		if r.Weight != c.weight || r.Recursive != c.recursive {
			t.Errorf("%s#%s: got weight=%d recursive=%v, want weight=%d recursive=%v",
				c.typ, c.rel, r.Weight, r.Recursive, c.weight, c.recursive)
		}
	}
}

func TestCostBuckets(t *testing.T) {
	cases := []struct {
		weight    int
		recursive bool
		want      costBucket
		glyph     rune
	}{
		{1, false, bucketCheap, '●'},
		{2, false, bucketModerate, '●'},
		{3, false, bucketModerate, '●'},
		{4, false, bucketExpensive, '●'},
		{10, false, bucketExpensive, '●'},
		{-1, true, bucketRecursive, '∞'},
	}
	for _, c := range cases {
		r := Relation{Weight: c.weight, Recursive: c.recursive}
		if got := r.bucket(); got != c.want {
			t.Errorf("weight=%d rec=%v: bucket=%d want %d", c.weight, c.recursive, got, c.want)
		}
		if gr, _ := r.heatGlyph(); gr != c.glyph {
			t.Errorf("weight=%d: glyph=%q want %q", c.weight, gr, c.glyph)
		}
	}
}

func TestWeightLegend(t *testing.T) {
	l := weightLegend()
	for _, want := range []string{"cheap", "moderate", "costly", "recursive"} {
		if !strings.Contains(l, want) {
			t.Errorf("weightLegend() missing %q: %s", want, l)
		}
	}
}
