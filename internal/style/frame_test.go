package style

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/theme"
)

func TestFrameSizesToItsContent(t *testing.T) {
	const cw = 30
	out := Frame("hello", cw)
	lines := strings.Split(out, "\n")

	// Border (2) + horizontal padding (4) sit outside the content width.
	want := cw + 6
	for i, ln := range lines {
		if got := lipgloss.Width(ln); got != want {
			t.Fatalf("line %d is %d wide, want %d:\n%s", i, got, want, out)
		}
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("body missing:\n%s", out)
	}
}

func TestFrameKeepsMultilineBodies(t *testing.T) {
	out := Frame("one\ntwo\nthree", 20)
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from frame:\n%s", want, out)
		}
	}
}

// The width checks above measure visible cells and the body checks look for
// plain substrings, so both stay green if the border loses its tint. Frame is
// the one border every wizard screen draws, so an untinted one is a visible
// regression nothing else would catch.
func TestFrameTintsItsBorder(t *testing.T) {
	Apply(theme.Default())
	if out := Frame("hello", 20); !strings.Contains(out, "\x1b[") {
		t.Fatalf("border lost its colour — BorderForeground dropped:\n%q", out)
	}
}
