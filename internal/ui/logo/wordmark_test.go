package logo

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/theme"
)

func TestWordmarkDimensions(t *testing.T) {
	w, h := WordmarkSize()
	if w != 23 || h != 11 {
		t.Fatalf("WordmarkSize = (%d,%d), want (23,11)", w, h)
	}
	lines := strings.Split(Wordmark(-1), "\n")
	if len(lines) != h {
		t.Fatalf("wordmark rows = %d, want %d", len(lines), h)
	}
	for i, ln := range lines {
		if lw := lipgloss.Width(ln); lw != w {
			t.Fatalf("row %d width = %d, want %d", i, lw, w)
		}
	}
}

func TestWordmarkHasGradientColors(t *testing.T) {
	out := Wordmark(-1)
	if !strings.Contains(out, "38;2;") {
		t.Fatal("wordmark must carry truecolor foregrounds under aurora")
	}
	// the block glyphs survive an ANSI strip
	if !strings.Contains(ansi.Strip(out), "▟███▙") {
		t.Fatal("wordmark must contain the rounded block glyphs")
	}
}

func TestWordmarkMonoIsUncolored(t *testing.T) {
	style.Apply(theme.Mono())
	t.Cleanup(func() { style.Apply(theme.Default()) })
	out := Wordmark(-1)
	if strings.Contains(out, "38;2;") || strings.Contains(out, "48;2;") {
		t.Fatal("mono wordmark must carry no truecolor sequences")
	}
}

func TestWordmarkShimmerDiffersButKeepsShape(t *testing.T) {
	a, b := Wordmark(-1), Wordmark(0.5)
	if a == b {
		t.Fatal("shimmer phase must change colors under aurora")
	}
	if ansi.Strip(a) != ansi.Strip(b) {
		t.Fatal("shimmer must not alter the glyph layout")
	}
}
