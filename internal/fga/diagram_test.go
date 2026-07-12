package fga

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/sergiught/go-openfga/openfga"
)

func githubModel() *openfga.AuthorizationModel {
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
			{Type: "organization", Relations: map[string]openfga.Userset{
				"member": openfga.This(),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"member": direct("user"),
			}}},
			{Type: "repo", Relations: map[string]openfga.Userset{
				"owner": openfga.This(),
				"admin": openfga.Union(openfga.This(), openfga.TupleTo("owner", "member")),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"owner": direct("organization"),
				"admin": direct("user"),
			}}},
		},
	}
}

func TestParseModelInterTypeEdges(t *testing.T) {
	g := ParseModel(githubModel())

	want := map[string]bool{
		"organization|direct|user": true, // organization#member: [user]
		"repo|direct|user":         true, // repo#admin: [user]
		"repo|direct|organization": true, // repo#owner: [organization]
		"repo|ttu|organization":    true, // repo#admin: member from owner -> organization
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
