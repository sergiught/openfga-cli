package fga

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/sergiught/go-openfga/openfga"
)

func githubModel() *openfga.AuthorizationModel {
	direct := func(types ...string) map[string]any {
		var list []any
		for _, t := range types {
			list = append(list, map[string]any{"type": t})
		}
		return map[string]any{"directly_related_user_types": list}
	}
	return &openfga.AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{Type: "organization", Relations: map[string]any{
				"member": map[string]any{"this": map[string]any{}},
			}, Metadata: map[string]any{"relations": map[string]any{
				"member": direct("user"),
			}}},
			{Type: "repo", Relations: map[string]any{
				"owner": map[string]any{"this": map[string]any{}},
				"admin": map[string]any{"union": map[string]any{"child": []any{
					map[string]any{"this": map[string]any{}},
					map[string]any{"tupleToUserset": map[string]any{
						"tupleset":        map[string]any{"relation": "owner"},
						"computedUserset": map[string]any{"relation": "member"},
					}},
				}}},
			}, Metadata: map[string]any{"relations": map[string]any{
				"owner": direct("organization"),
				"admin": direct("user"),
			}}},
		},
	}
}

func TestParseModelInterTypeEdges(t *testing.T) {
	g := ParseModel(githubModel())

	want := map[string]bool{
		"organization|direct|user":  true, // organization#member: [user]
		"repo|direct|user":          true, // repo#admin: [user]
		"repo|direct|organization":  true, // repo#owner: [organization]
		"repo|ttu|organization":     true, // repo#admin: member from owner -> organization
	}
	got := map[string]bool{}
	for _, e := range g.Edges {
		got[e.From+"|"+e.Kind+"|"+e.To] = true
		if e.From == e.To {
			t.Errorf("unexpected self-edge on %q", e.From)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing edge %q; got %v", k, got)
		}
	}
}

func TestRenderDiagramShowsTypesAndEdges(t *testing.T) {
	g := ParseModel(githubModel())
	out := ansi.Strip(g.RenderDiagram())

	for _, typ := range []string{"user", "organization", "repo"} {
		if !strings.Contains(out, typ) {
			t.Errorf("diagram missing type %q", typ)
		}
	}
	if !strings.ContainsRune(out, '╭') {
		t.Error("diagram should draw rounded node cards")
	}
	if !strings.ContainsRune(out, '▸') {
		t.Error("diagram should draw directed edges")
	}
}

func TestRenderDiagramEmpty(t *testing.T) {
	g := Graph{}
	if got := g.RenderDiagram(); !strings.Contains(got, "no authorization model") {
		t.Errorf("empty diagram = %q", got)
	}
}
