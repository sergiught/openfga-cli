package shell

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
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
	s.SetSidebar("ofga", []string{"store: demo"}, []NavItem{{Label: "Model", Active: true}, {Label: "Tuples", Badge: "42"}}, "online")
	s.SetMain("Authorization Model", "type document")
	s.SetStatus("ready", "q quit")
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
