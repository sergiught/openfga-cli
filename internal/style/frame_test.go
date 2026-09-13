package style

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
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
