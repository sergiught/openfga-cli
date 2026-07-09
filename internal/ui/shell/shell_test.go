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
	if !strings.Contains(stripAnsi(view), "Model") || !strings.Contains(stripAnsi(view), "ready") {
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
	// interior = (total - sidebarOccupied) - margins(2); the flat pane has no
	// border, just a 1-col margin on each side.
	want := 100 - s.sidebarOccupied() - 2
	if w != want {
		t.Errorf("MainSize().w = %d, want interior %d", w, want)
	}
}

func TestActiveNavShowsBadge(t *testing.T) {
	s := New()
	s.SetSize(100, 24)
	s.SetSidebar(nil, []NavItem{{Label: "Tuples", Badge: "42", Active: true}}, "")
	if !strings.Contains(stripAnsi(s.View()), "42") {
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
	if !strings.Contains(stripAnsi(view), "Pick") {
		t.Error("dialog title should appear in the view")
	}

	s.SetDialog("", "")
	if strings.Contains(stripAnsi(s.View()), "Pick") {
		t.Error("clearing the dialog (empty title+body) should remove it from the view")
	}
}

func TestStatusSegments(t *testing.T) {
	s := New()
	s.SetSize(100, 30)
	s.SetStatus(Status{Mode: "CHECK", Store: "demo", Keys: []string{"q"}})
	out := stripAnsi(s.View())
	for _, want := range []string{"CHECK", "demo", "q"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status bar missing %q", want)
		}
	}
}

func TestFlatMainPaneWithHeaderRule(t *testing.T) {
	icons.Apply(icons.ModeNerdFont)
	t.Cleanup(func() { icons.Apply(icons.ModeNerdFont) })
	s := New()
	s.SetSize(100, 30)
	s.SetMain("Query", "body")
	s.SetStatus(Status{Mode: "CHECK", Keys: []string{"q"}})
	out := s.View()
	for _, glyph := range []string{"╭", "╰", "│"} {
		if strings.Contains(out, glyph) {
			t.Fatalf("flat base frame must not contain border glyph %q", glyph)
		}
	}
	if strings.Contains(out, "48;2;6;8;12") {
		t.Fatal("base frame must not paint BgPanel (48;2;6;8;12)")
	}
	if !strings.Contains(stripAnsi(out), "Query ─") {
		t.Fatal("main pane must carry a section-header rule")
	}
	if !strings.Contains(out, "") {
		t.Fatal("status chips keep powerline caps")
	}
	s.SetDialog("Modal", "body")
	if dlg := s.View(); !strings.Contains(dlg, "╭") {
		t.Fatal("dialogs remain the boxed exception")
	}

	icons.Apply(icons.ModeUnicode)
	if out := s.View(); strings.Contains(out, "") {
		t.Fatal("unicode icon rung must drop powerline caps")
	}
}

func TestBrandLineInSidebar(t *testing.T) {
	s := New()
	s.SetSize(120, 30)
	s.SetBrand("", "v1")
	s.SetMain("Query", "body")
	plain := stripAnsi(s.View())
	if !strings.Contains(plain, "v1") {
		t.Fatal("sidebar must carry version")
	}
	if strings.Contains(plain, "authorization playground") {
		t.Fatal("sidebar must not carry the tagline")
	}
	if !strings.Contains(plain, "╱╱╱") {
		t.Fatal("sidebar must carry hatch bands")
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

func TestWordmarkRendersInWideSidebar(t *testing.T) {
	s := New()
	s.SetSize(120, 35)
	s.SetBrand("", "v1")
	s.SetMain("Query", "body")
	out := stripAnsi(s.View())
	if !strings.Contains(out, "▟███▙") {
		t.Fatal("wide sidebar must render the block wordmark")
	}
	if !strings.Contains(out, "v1") {
		t.Fatal("brand line must show version")
	}
	if strings.Contains(out, "authorization playground") {
		t.Fatal("brand line must not show tagline")
	}
}

func TestWordmarkFallsBackOnShortTerminal(t *testing.T) {
	s := New()
	s.SetSize(120, 20) // bodyHeight 19 < 26
	s.SetBrand("", "v1")
	s.SetMain("Query", "body")
	out := stripAnsi(s.View())
	if strings.Contains(out, "▟███▙") {
		t.Fatal("short terminal must fall back to the text wordmark")
	}
	if !strings.Contains(out, "OpenFGA") {
		t.Fatal("fallback wordmark must read OpenFGA")
	}
}

func stripAnsi(s string) string {
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
