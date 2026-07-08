package shell

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/ui/icons"
)

func TestSizingWideHasSidebar(t *testing.T) {
	s := New()
	s.SetSize(110, 32)
	if s.Collapsed() {
		t.Fatal("110 cols should not collapse the sidebar")
	}
	w, h := s.MainSize()
	if w <= 0 || h <= 0 {
		t.Fatalf("MainSize = (%d,%d), want positive", w, h)
	}
	if w >= 110 {
		t.Errorf("main width %d should be less than total when sidebar is shown", w)
	}
}

func TestSizingNarrowCollapses(t *testing.T) {
	s := New()
	s.SetSize(60, 20)
	if !s.Collapsed() {
		t.Fatal("60 cols should collapse the sidebar")
	}
	w, _ := s.MainSize()
	if w < 50 {
		t.Errorf("collapsed main width %d should use most of the 60 cols", w)
	}
}

func TestViewFitsWidth(t *testing.T) {
	s := New()
	s.SetSize(100, 24)
	s.SetSidebar([]string{"store: demo"}, []NavItem{{Label: "Model", Active: true}, {Label: "Tuples", Badge: "42"}}, "online")
	s.SetMain("Authorization Model", "type document")
	s.SetStatus(Status{Left: "ready", Keys: []string{"q"}})
	view := s.View()
	if strings.TrimSpace(view) == "" {
		t.Fatal("empty view")
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Fatalf("line %d width %d exceeds 100", i, w)
		}
	}
	if !strings.Contains(stripANSI(view), "Model") || !strings.Contains(stripANSI(view), "ready") {
		t.Error("view should contain nav label and status text")
	}
}

func TestRegionsFillFullWidth(t *testing.T) {
	for _, total := range []int{100, 120, 90} { // wide (sidebar shown)
		s := New()
		s.SetSize(total, 24)
		s.SetSidebar([]string{"store: demo"}, []NavItem{{Label: "Model", Active: true}}, "online")
		s.SetMain("Title", "body")
		sb := s.renderSidebar(s.bodyHeight())
		main := s.renderMain(s.bodyHeight())
		if got := lipgloss.Width(sb) + lipgloss.Width(main); got != total {
			t.Errorf("total=%d: sidebar(%d)+main(%d)=%d, want %d (no gap)",
				total, lipgloss.Width(sb), lipgloss.Width(main), got, total)
		}
	}
	// collapsed: main fills the whole width
	s := New()
	s.SetSize(60, 20)
	s.SetMain("T", "b")
	if got := lipgloss.Width(s.renderMain(s.bodyHeight())); got != 60 {
		t.Errorf("collapsed main width = %d, want 60", got)
	}
}

func TestMainSizeIsInterior(t *testing.T) {
	s := New()
	s.SetSize(100, 24)
	w, _ := s.MainSize()
	// interior = (total - sidebarOccupied) - border(2) - padding(2)
	want := 100 - s.sidebarOccupied() - 4
	if w != want {
		t.Errorf("MainSize().w = %d, want interior %d", w, want)
	}
}

func TestActiveNavShowsBadge(t *testing.T) {
	s := New()
	s.SetSize(100, 24)
	s.SetSidebar(nil, []NavItem{{Label: "Tuples", Badge: "42", Active: true}}, "")
	if !strings.Contains(stripANSI(s.View()), "42") {
		t.Error("active nav item should still show its badge count")
	}
}

func TestSetDialogKeepsBaseDimensions(t *testing.T) {
	s := New()
	s.SetSize(80, 24)
	s.SetSidebar([]string{"store: demo"}, []NavItem{{Label: "Model", Active: true}}, "online")
	s.SetMain("Title", "body")
	s.SetStatus(Status{Left: "ready", Keys: []string{"q"}})
	s.SetDialog("Pick", "one\ntwo")

	view := s.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 24 {
		t.Fatalf("dialog view line count = %d, want 24 (must match shell height)", len(lines))
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > 80 {
			t.Fatalf("dialog view line %d width %d exceeds shell width 80", i, w)
		}
	}
	if !strings.Contains(stripANSI(view), "Pick") {
		t.Error("dialog title should appear in the view")
	}

	s.SetDialog("", "")
	if strings.Contains(stripANSI(s.View()), "Pick") {
		t.Error("clearing the dialog (empty title+body) should remove it from the view")
	}
}

func TestStatusSegments(t *testing.T) {
	s := New()
	s.SetSize(100, 30)
	s.SetStatus(Status{Mode: "CHECK", Store: "demo", Keys: []string{"q"}})
	out := stripANSI(s.View())
	for _, want := range []string{"CHECK", "demo", "q"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status bar missing %q", want)
		}
	}
}

func TestFilledTitleBarAndCaps(t *testing.T) {
	icons.Apply(icons.ModeNerdFont)
	t.Cleanup(func() { icons.Apply(icons.ModeNerdFont) })
	s := New()
	s.SetSize(100, 30)
	s.SetMain("Query", "body")
	s.SetStatus(Status{Mode: "CHECK"})
	out := s.View()
	if !strings.Contains(out, "48;2;43;53;71") { // BgHighlight #2B3547 behind the title
		t.Fatal("main title must render on a filled BgHighlight bar")
	}
	if !strings.Contains(out, "") || !strings.Contains(out, "") {
		t.Fatal("status chips must carry powerline caps on the nerdfont rung")
	}
	icons.Apply(icons.ModeUnicode)
	if out2 := s.View(); strings.Contains(out2, "") {
		t.Fatal("caps must disappear on the unicode rung")
	}
}

func TestEntranceSlidesAndSettles(t *testing.T) {
	s := New()
	s.SetSize(100, 30)
	s.SetMain("Query", "body")
	settled := s.View()
	s.SetEntrance(0.5, true)
	moving := s.View()
	if moving == settled {
		t.Fatal("mid-entrance frame must differ from the settled frame")
	}
	s.SetEntrance(0, false)
	if s.View() != settled {
		t.Fatal("frac 0 + ghost false must render identically to steady state")
	}
}

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
