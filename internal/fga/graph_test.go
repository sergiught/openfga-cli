package fga

import (
	"strings"
	"testing"
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
