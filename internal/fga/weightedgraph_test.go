package fga

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/sergiught/go-openfga/openfga"
)

// wgModel is the model-visualizer default model: nested (recursive) groups, a
// folder with two terminal types, and a document whose viewer unions a direct
// grouping with a tuple-to-userset.
func wgModel() *openfga.AuthorizationModel {
	ref := func(labels ...string) openfga.RelationMetadata {
		refs := make([]openfga.RelationReference, len(labels))
		for i, l := range labels {
			if typ, rel, ok := strings.Cut(l, "#"); ok {
				refs[i] = openfga.RelationReference{Type: typ, Relation: rel}
			} else {
				refs[i] = openfga.DirectType(l)
			}
		}
		return openfga.RelationMetadata{DirectlyRelatedUserTypes: refs}
	}
	return &openfga.AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{Type: "employee"},
			{Type: "group", Relations: map[string]openfga.Userset{
				"member": openfga.Union(openfga.This(), openfga.TupleTo("parent", "member")),
				"parent": openfga.This(),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"member": ref("user"), "parent": ref("group"),
			}}},
			{Type: "folder", Relations: map[string]openfga.Userset{
				"viewer": openfga.This(),
				"owner":  openfga.This(),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"viewer": ref("user"), "owner": ref("employee"),
			}}},
			{Type: "document", Relations: map[string]openfga.Userset{
				"viewer": openfga.Union(openfga.This(), openfga.TupleTo("parent", "viewer")),
				"parent": openfga.This(),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"viewer": ref("user", "group#member"), "parent": ref("folder"),
			}}},
		},
	}
}

func wgNodeByID(t *testing.T, g weightedGraph, id string) *wgNode {
	t.Helper()
	if n, ok := g.index[id]; ok {
		return n
	}
	t.Fatalf("weighted graph missing node %q", id)
	return nil
}

func TestWeightedGraphWeights(t *testing.T) {
	g := buildWeightedGraph(wgModel())
	cases := []struct {
		id, typ string
		weight  int
	}{
		{"group#parent", "group", 1},                 // direct → group object
		{"folder#viewer", "user", 1},                 // direct → user
		{"folder#owner", "employee", 1},              // direct → employee
		{"document#parent", "folder", 1},             // direct → folder
		{"group#member", "user", weightRecursive},    // member → member via parent (cycle)
		{"document#viewer", "user", weightRecursive}, // reaches the recursive group#member
	}
	for _, c := range cases {
		n := wgNodeByID(t, g, c.id)
		got, ok := n.weights[c.typ]
		if !ok {
			t.Errorf("%s: no weight for terminal type %q (weights=%v)", c.id, c.typ, n.weights)
			continue
		}
		if got != c.weight {
			t.Errorf("%s weight[%s] = %d, want %d", c.id, c.typ, got, c.weight)
		}
	}
}

func TestWeightedGraphNodeKinds(t *testing.T) {
	g := buildWeightedGraph(wgModel())
	want := map[wgKind]bool{}
	for _, n := range g.nodes {
		want[n.kind] = true
	}
	for _, k := range []wgKind{wgRelation, wgType, wgOperator, wgGrouping} {
		if !want[k] {
			t.Errorf("weighted graph produced no %v node", k)
		}
	}
	// The operator node for a union shows the operator name, not an id.
	if op, ok := g.index["op:1:group#member"]; !ok {
		t.Error("missing operator node for group#member's union")
	} else if op.display != "union" {
		t.Errorf("operator display = %q, want %q", op.display, "union")
	}
	// A terminal type node has no weights.
	if u := wgNodeByID(t, g, "user"); len(u.weights) != 0 {
		t.Errorf("terminal type user should have no weights, got %v", u.weights)
	}
}

func TestWeightedGraphEdgeKinds(t *testing.T) {
	g := buildWeightedGraph(wgModel())
	seen := map[wgEdgeKind]bool{}
	for _, e := range g.edges {
		seen[e.kind] = true
	}
	for _, k := range []wgEdgeKind{wgDirect, wgRewrite, wgTTU, wgLogical} {
		if !seen[k] {
			t.Errorf("weighted graph produced no edge of kind %v", k)
		}
	}
}

func TestWeightedGraphRender(t *testing.T) {
	out := ansi.Strip(buildWeightedGraph(wgModel()).render())
	for _, want := range []string{
		"weighted graph",              // legend
		"document#viewer",             // relation node
		"union",                       // operator node
		"document#direct:viewer",      // grouping node
		string(weightIcon) + " user:", // weight row with icon
		"∞",                           // recursive weight
	} {
		if !strings.Contains(out, want) {
			t.Errorf("weighted-graph render missing %q", want)
		}
	}
}

func TestWeightedGraphRenderEmpty(t *testing.T) {
	out := buildWeightedGraph(&openfga.AuthorizationModel{SchemaVersion: "1.1"}).render()
	if strings.TrimSpace(ansi.Strip(out)) == "" {
		t.Error("empty model should render a placeholder, got blank")
	}
}

// TestWeightedGraphNoBoxCollision guards the routing invariant: no edge is ever
// painted inside a box interior. This is what keeps the boxes readable, so a
// regression in the layered router (e.g. a mishandled back-edge) must fail here.
func TestWeightedGraphNoBoxCollision(t *testing.T) {
	g := buildWeightedGraph(wgModel())
	boxes, _, edges := g.toBoxes()
	l := newLayered(boxes, edges)
	l.order()
	w, h := l.position()
	c := newCanvas(w, h)
	l.route(c)
	for _, n := range l.real {
		for y := n.y + 1; y <= n.y+n.h-2; y++ {
			for x := n.x + 1; x <= n.x+n.w-2; x++ {
				if r := c.at(x, y); r != ' ' && r != 0 {
					t.Fatalf("edge glyph %q painted inside box %s at (%d,%d)", string(r), n.id, x, y)
				}
			}
		}
	}
}
