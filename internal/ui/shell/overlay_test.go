package shell

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestOverlayKeepsBaseDimensions(t *testing.T) {
	base := strings.Repeat("abcdefghij\n", 10) // 10×10 grid
	base = strings.TrimRight(base, "\n")
	dialog := Dialog("Pick", "one\ntwo", 12)
	out := Overlay(base, dialog, 10, 10)
	if got := len(strings.Split(out, "\n")); got != 10 {
		t.Fatalf("overlay line count = %d, want 10 (must match base height)", got)
	}
	if !strings.Contains(stripANSI(out), "Pick") {
		t.Error("dialog title should appear in the overlay")
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 10 {
			t.Fatalf("overlay line %d width %d exceeds base width 10", i, w)
		}
	}
}

func TestDialogHasTitleAndBorder(t *testing.T) {
	d := Dialog("Switch model", "01JC…\n01JB…", 24)
	plain := stripANSI(d)
	if !strings.Contains(plain, "Switch model") {
		t.Error("dialog should render its title")
	}
	if !strings.Contains(plain, "╭") && !strings.Contains(plain, "┌") {
		t.Error("dialog should have a rounded/box border")
	}
}
