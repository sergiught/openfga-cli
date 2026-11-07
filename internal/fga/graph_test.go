package fga

import (
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"
)

func TestRenderHeatLegendAndGlyph(t *testing.T) {
	g := ParseModel(weightModel())
	out := g.Render()

	// Verify the weight legend is present in the header
	for _, word := range []string{"cheap", "moderate", "costly", "recursive"} {
		if !strings.Contains(out, word) {
			t.Errorf("weight legend missing %q in output:\n%s", word, out)
		}
	}

	// Verify heat glyphs appear before relation names
	for _, want := range []string{"●", "∞"} {
		if !strings.Contains(out, want) {
			t.Errorf("heat glyph %q not found in output:\n%s", want, out)
		}
	}

	// Spot-check a few relations have glyphs
	docOwner := findRel(t, g, "doc", "owner")
	if docOwner.Weight != 1 {
		t.Errorf("doc#owner should have weight 1, got %d", docOwner.Weight)
	}

	groupMember := findRel(t, g, "group", "member")
	if !groupMember.Recursive {
		t.Errorf("group#member should be recursive")
	}
}

func TestParseModelToRelation(t *testing.T) {
	// doc.viewer: [group, group#member]; group.member: [user]
	g := ParseModel(&openfga.AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{Type: "group", Relations: map[string]openfga.Userset{
				"member": openfga.This(),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"member": {DirectlyRelatedUserTypes: []openfga.RelationReference{{Type: "user"}}},
			}}},
			{Type: "doc", Relations: map[string]openfga.Userset{
				"viewer": openfga.This(),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"viewer": {DirectlyRelatedUserTypes: []openfga.RelationReference{
					{Type: "group"}, {Type: "group", Relation: "member"},
				}},
			}}},
		},
	})
	var toGroup []DiagramEdge
	for _, e := range g.Edges {
		if e.From == "doc" && e.To == "group" {
			toGroup = append(toGroup, e)
		}
	}
	if len(toGroup) != 2 {
		t.Fatalf("want 2 doc→group edges (type and userset), got %+v", toGroup)
	}
	got := map[string]bool{}
	for _, e := range toGroup {
		got[e.ToRelation] = true
	}
	if !got[""] || !got["member"] {
		t.Errorf("want ToRelation set {\"\", \"member\"}, got %v", got)
	}
}

func TestParseModelTTUEdgesHaveNoToRelation(t *testing.T) {
	g := ParseModel(githubModel())
	seen := false
	for _, e := range g.Edges {
		if e.Kind == "ttu" {
			seen = true
			if e.ToRelation != "" {
				t.Errorf("ttu edge %+v must have empty ToRelation", e)
			}
		}
	}
	if !seen {
		t.Fatal("fixture has no ttu edge")
	}
}
