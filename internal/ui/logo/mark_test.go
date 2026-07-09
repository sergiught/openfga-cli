package logo

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/theme"
)

// stripMark strips ANSI styling from mark output for shape comparisons.
func stripMark(s string) string { return ansi.Strip(s) }

func TestMarkDimensions(t *testing.T) {
	lines := strings.Split(Mark(), "\n")
	w, h := MarkSize()
	if len(lines) != h {
		t.Fatalf("mark rows = %d, want %d", len(lines), h)
	}
	for i, ln := range lines {
		if lw := lipgloss.Width(ln); lw != w {
			t.Fatalf("row %d width = %d, want %d", i, lw, w)
		}
	}
}

func TestMarkCornersAreUnstyledSpaces(t *testing.T) {
	lines := strings.Split(Mark(), "\n")
	first, last := lines[0], lines[len(lines)-1]
	for name, ln := range map[string]string{"first": first, "last": last} {
		if !strings.HasPrefix(ln, " ") || !strings.HasSuffix(ln, " ") {
			t.Fatalf("%s row must start and end with a plain space (rounded corners), got %q…", name, ln[:12])
		}
	}
}

func TestMarkCarriesContainerAndColor(t *testing.T) {
	out := Mark()
	if !strings.Contains(out, "48;2;0;0;0") {
		t.Fatal("mark must carry the container's opaque black background")
	}
	if !strings.Contains(out, "38;2;") {
		t.Fatal("mark must carry truecolor foregrounds")
	}
}

func TestMarkMonoFallsBackToSlab(t *testing.T) {
	style.Apply(theme.Mono())
	t.Cleanup(func() { style.Apply(theme.Default()) })
	if Mark() != Word("ofga") {
		t.Fatal("mono mark must be the plain slab wordmark")
	}
	if MarkShimmer(0.5) != Word("ofga") {
		t.Fatal("mono shimmer must be the plain slab wordmark")
	}
}

func TestMarkShimmerDiffersButPreservesShape(t *testing.T) {
	a, b := Mark(), MarkShimmer(0.5)
	if a == b {
		t.Fatal("shimmer must change colors under aurora")
	}
	// content (glyph layout) identical, colors differ
	if stripMark(a) != stripMark(b) {
		t.Fatal("shimmer must not alter the glyph layout")
	}
}
