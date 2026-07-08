package shell

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

// TestViewClampsToExactSize reproduces the real-terminal breakage: long content
// (a 26-char store ID in the sidebar, a long error + key-hint status) WRAPS,
// inflating the view past the terminal height and scrolling the header off.
// The shell must truncate, never wrap: the frame must be exactly height lines,
// each no wider than width.
func TestViewClampsToExactSize(t *testing.T) {
	s := New()
	s.SetSize(100, 24)
	s.SetSidebar(
		[]string{"▣ 01KSE09DXMSJPS056SA89RX3GA-this-id-would-wrap-the-sidebar"},
		[]NavItem{{Label: "Model", Active: true}, {Label: "Tuples", Badge: "9999"}},
		"● connected")
	s.SetMain("Model", "type document\n  define viewer: [user]")
	s.SetStatus(Status{
		Left: "model: openfga: no authorization models found in this store right now",
		Keys: []string{"↑↓", "/", "↵", "n", "r", "q"},
	})

	v := s.View()
	lines := strings.Split(v, "\n")
	if len(lines) != 24 {
		t.Fatalf("view = %d lines, want exactly 24 (wrap must be truncated, not overflow height)", len(lines))
	}
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w > 100 {
			t.Errorf("line %d width %d > 100", i, w)
		}
	}
}
