package fga

import (
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestRenderDiagramHeat(t *testing.T) {
	g := ParseModel(weightModel())
	out := ansi.Strip(g.RenderDiagram())

	for _, want := range []string{"cheap", "moderate", "costly", "recursive"} {
		if !strings.Contains(out, want) {
			t.Errorf("diagram missing heat legend %q", want)
		}
	}
	if !strings.ContainsRune(out, '∞') {
		t.Error("diagram should show ∞ for recursive relations")
	}
}

// cardLines returns the stripped rows of the card whose title row holds
// "│ <name>", from the "╭" row through the "╰" row, cropped to the card's own
// columns so neighbouring cards and edges do not leak in. top is the index of
// the "╭" row in out.
func cardLines(t *testing.T, out, name string) (lines []string, top int) {
	t.Helper()
	all := strings.Split(out, "\n")
	for i, l := range all {
		idx := strings.Index(l, "│ "+name)
		if idx < 0 {
			continue
		}
		top = i - 1
		x := len([]rune(l[:idx]))
		topRunes := []rune(all[top])
		end := x
		for end < len(topRunes) && topRunes[end] != '╮' {
			end++
		}
		for j := top; j < len(all); j++ {
			r := []rune(all[j])
			lines = append(lines, string(r[x:min(end+1, len(r))]))
			if strings.Contains(lines[len(lines)-1], "╰") {
				return lines, top
			}
		}
	}
	t.Fatalf("card %q not found", name)
	return nil, 0
}

func TestRenderDiagramWithCompactRowsKeepNames(t *testing.T) {
	g := ParseModel(githubModel())
	out := ansi.Strip(g.RenderDiagramWith(DiagramOptions{}))
	for _, name := range []string{"member", "owner", "admin"} {
		if !strings.Contains(out, name) {
			t.Errorf("compact diagram missing relation %q", name)
		}
	}
	if strings.Contains(out, "←user") || strings.Contains(out, "from owner") {
		t.Errorf("compact diagram must not show resolution detail:\n%s", out)
	}
}

func TestRenderDiagramWithAllExpandedEqualsRenderDiagram(t *testing.T) {
	g := ParseModel(githubModel())
	if a, b := g.RenderDiagram(), g.RenderDiagramWith(DiagramOptions{Expanded: ExpandAll(g)}); a != b {
		t.Errorf("all-expanded render differs from RenderDiagram")
	}
}

func TestSelectedCardBorderUsesAccent(t *testing.T) {
	g := ParseModel(githubModel())
	plain := g.RenderDiagramWith(DiagramOptions{})
	sel := g.RenderDiagramWith(DiagramOptions{Selected: "repo"})
	if plain == sel {
		t.Fatal("selecting repo must change the render")
	}
	if ansi.Strip(plain) != ansi.Strip(sel) {
		t.Fatal("selection must only change colors, not glyphs")
	}
	// Locate the repo card's top-left corner in the stripped text, then
	// compare the colored renders from that line on.
	stripped := strings.Split(ansi.Strip(sel), "\n")
	_, top := cardLines(t, ansi.Strip(sel), "repo")
	idx := strings.Index(stripped[top], "╭")
	if idx < 0 {
		t.Fatal("no corner above repo title")
	}
	col := len([]rune(stripped[top][:idx]))
	cellOf := func(out string) string {
		line := strings.Split(out, "\n")[top]
		return ansi.Cut(line, col, col+1)
	}
	if cellOf(plain) == cellOf(sel) {
		t.Errorf("repo corner cell identical in plain and selected: %q", cellOf(sel))
	}
}

func TestRenderDiagramWithMixedForms(t *testing.T) {
	g := ParseModel(githubModel())
	out := ansi.Strip(g.RenderDiagramWith(DiagramOptions{Expanded: map[string]bool{"repo": true}}))
	if !strings.Contains(out, "from owner") {
		t.Error("expanded repo should show ttu detail")
	}
	card, _ := cardLines(t, out, "organization")
	found := false
	for _, l := range card {
		if strings.Contains(l, "member") {
			found = true
			if strings.Contains(l, "←") {
				t.Errorf("compact organization member row carries detail: %q", l)
			}
		}
	}
	if !found {
		t.Error("organization member row not found")
	}
}

func TestCompactCardAtLeastTitleWide(t *testing.T) {
	g := ParseModel(&openfga.AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{{
			Type:      "a_very_long_type_name",
			Relations: map[string]openfga.Userset{"r": openfga.This()},
			Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"r": {DirectlyRelatedUserTypes: []openfga.RelationReference{openfga.DirectType("a_very_long_type_name")}},
			}},
		}},
	})
	card, _ := cardLines(t, ansi.Strip(g.RenderDiagramWith(DiagramOptions{})), "a_very_long_type_name")
	if w := len([]rune(card[0])); w != len("a_very_long_type_name")+4 {
		t.Errorf("card width %d, want %d", w, len("a_very_long_type_name")+4)
	}
}

func TestRenderDiagramWithWrapsTallCards(t *testing.T) {
	g := ParseModel(largeModel(t))
	out := ansi.Strip(g.RenderDiagramWith(DiagramOptions{ViewportHeight: 20}))
	lines := strings.Split(out, "\n")
	if len(lines) > 3+2*margin+2*20+vgap {
		t.Errorf("wrapped canvas too tall: %d lines", len(lines))
	}
	card, _ := cardLines(t, out, "res0 ")
	if len(card) > 20 {
		t.Errorf("wrapped res0 card is %d lines tall, want <= 20", len(card))
	}
	for r := 0; r < 25; r++ {
		if !strings.Contains(out, fmt.Sprintf("rel%d", r)) {
			t.Errorf("missing rel%d", r)
		}
	}
}

// Guards against pathological blowups only; BenchmarkRenderDiagramWithLargeModel
// is where real numbers live.
func TestRenderDiagramWithLargeModelIsFast(t *testing.T) {
	g := ParseModel(largeModel(t))
	start := time.Now()
	g.RenderDiagramWith(DiagramOptions{ViewportHeight: 40})
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("compact render took %v", d)
	}
	start = time.Now()
	g.RenderDiagram()
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("expanded render took %v", d)
	}
}

func BenchmarkRenderDiagramWithLargeModel(b *testing.B) {
	g := ParseModel(largeModel(b))
	b.Run("compact", func(b *testing.B) {
		for range b.N {
			g.RenderDiagramWith(DiagramOptions{ViewportHeight: 40})
		}
	})
	b.Run("expanded", func(b *testing.B) {
		for range b.N {
			g.RenderDiagram()
		}
	})
}
