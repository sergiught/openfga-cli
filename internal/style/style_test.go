package style

import (
	"image/color"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/theme"
)

func TestGradientPreservesText(t *testing.T) {
	// lipgloss v2 Style.Render always emits full-fidelity ANSI codes;
	// downsampling happens at the output/writer layer, not here.
	Apply(theme.Default()) // aurora has gradient endpoints
	out := Gradient("ofga")
	stripped := stripANSI(out)
	if stripped != "ofga" {
		t.Errorf("Gradient visible text = %q, want %q", stripped, "ofga")
	}
	if !strings.Contains(out, "\x1b[") {
		t.Error("expected ANSI color codes in gradient output")
	}
}

func TestGradientMonoIsPlain(t *testing.T) {
	Apply(theme.Mono())
	if out := Gradient("ofga"); out != "ofga" {
		t.Errorf("mono Gradient = %q, want plain %q", out, "ofga")
	}
	Apply(theme.Default()) // restore for other tests
}

func TestDotRendersGlyph(t *testing.T) {
	Apply(theme.Default())
	for _, st := range []DotState{DotOnline, DotBusy, DotError, DotOffline} {
		if got := stripANSI(Dot(st)); got != IconDot {
			t.Errorf("Dot(%d) glyph = %q, want %q", st, got, IconDot)
		}
	}
}

func TestBlend(t *testing.T) {
	Apply(theme.Default())
	if got := Blend(Green, Faintc, 0); got != Green {
		t.Errorf("Blend(a, b, 0) = %v, want a %v", got, Green)
	}
	// Midway blend must not panic and must render as a valid foreground color.
	mid := Blend(Green, Faintc, 0.5)
	if w := lipgloss.Width(lipgloss.NewStyle().Foreground(mid).Render("●")); w != 1 {
		t.Fatalf("blended dot width = %d, want 1", w)
	}
	// Falls back to a when a color can't be converted (e.g. fully transparent).
	if got := Blend(Green, color.Transparent, 0.5); got != Green {
		t.Errorf("Blend with unconvertible b = %v, want fallback a %v", got, Green)
	}
}

func TestChipKeycapPillRender(t *testing.T) {
	if w := lipgloss.Width(Chip("CHECK", Fg, BgHighlight)); w != 7 {
		t.Fatalf("Chip width = %d, want 7 (padded)", w)
	}
	if w := lipgloss.Width(Keycap("q")); w != 3 {
		t.Fatalf("Keycap width = %d, want 3", w)
	}
	if w := lipgloss.Width(GradientPill("ofga")); w != 6 {
		t.Fatalf("GradientPill width = %d, want 6", w)
	}
}

func TestShimmerPreservesShape(t *testing.T) {
	art := "ABC\nDEF"
	plain := stripANSI(GradientBlockShimmer(art, 0.5))
	if plain != art {
		t.Fatalf("shimmer altered content: %q", plain)
	}
}

// stripANSI removes CSI sequences for assertion purposes.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
