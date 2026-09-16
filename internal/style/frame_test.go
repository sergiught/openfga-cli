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

func TestFrameTitledKeepsTheFrameItsSize(t *testing.T) {
	const cw = 40
	out := FrameTitled("hello", cw, "Rules › Rule", "esc ‹ Rules")
	for i, ln := range strings.Split(out, "\n") {
		if got := lipgloss.Width(ln); got != cw+6 {
			t.Fatalf("line %d is %d wide, want %d:\n%s", i, got, cw+6, out)
		}
	}
	top := strings.Split(out, "\n")[0]
	for _, want := range []string{"╭", "Rules › Rule", "esc ‹ Rules", "╮"} {
		if !strings.Contains(top, want) {
			t.Fatalf("%q is not on the border line: %q", want, top)
		}
	}
}

// The way out is the one affordance that has to survive a narrow terminal: a
// user who cannot read it is stuck. The location gives up its columns to it.
func TestFrameTitledDropsTheLocationBeforeTheWayOut(t *testing.T) {
	top := strings.Split(FrameTitled("x", 12, "Rules › Rule › Tuples", "esc ‹ Rules"), "\n")[0]
	if !strings.Contains(top, "esc ‹ Rules") {
		t.Fatalf("the way out was truncated away: %q", top)
	}
	if lipgloss.Width(top) != 18 {
		t.Fatalf("the border line is %d wide, want 18: %q", lipgloss.Width(top), top)
	}
}

// Narrower than either legend, the border goes back to being a border rather
// than rendering a line of ellipses.
func TestFrameTitledFallsBackToAPlainBorder(t *testing.T) {
	top := strings.Split(FrameTitled("x", 1, "Rules", "esc ‹ Rules"), "\n")[0]
	if strings.ContainsAny(top, "…") || strings.Contains(top, "esc") {
		t.Fatalf("the border kept a legend it had no room for: %q", top)
	}
	if lipgloss.Width(top) != 7 {
		t.Fatalf("the border line is %d wide, want 7: %q", lipgloss.Width(top), top)
	}
}

func TestFrameTitledWithNoLegendsIsJustAFrame(t *testing.T) {
	if got, want := FrameTitled("hello", 20, "", ""), Frame("hello", 20); got != want {
		t.Fatalf("an unlabelled titled frame differs from a plain one:\ngot  %q\nwant %q", got, want)
	}
}
