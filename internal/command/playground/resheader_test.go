package playground

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/ui/shell"
)

// colOf returns the display column (not byte offset) at which label starts in
// the visible text — the middot separator is multi-byte but one column wide.
func colOf(vis, label string) int {
	return lipgloss.Width(vis[:strings.Index(vis, label)])
}

// resHeader's reported zones must line up with where the labels actually
// render, so a change to the header format can't silently break the click
// hit-testing that maps a column to an action.
func TestResHeaderZonesMatchRenderedColumns(t *testing.T) {
	m := Model{}
	m.result.vals = [3]string{"user:anne", "viewer", "document:roadmap"}

	head, z := m.resHeader(0)
	vis := ansi.Strip(head)

	for _, tc := range []struct {
		name  string
		label string
		zone  [2]int
	}{
		{"full tree", resToggleFull, z.full},
		{"ACL path", resTogglePath, z.path},
		{"p toggle", resHintToggle, z.toggle},
		{"r/esc close", resHintClose, z.close},
	} {
		if got := colOf(vis, tc.label); got != tc.zone[0] {
			t.Errorf("%s renders at col %d, resHeader reports start %d", tc.name, got, tc.zone[0])
		}
		if got := tc.zone[1] - tc.zone[0]; got != lipgloss.Width(tc.label) {
			t.Errorf("%s zone width = %d, want %d", tc.name, got, lipgloss.Width(tc.label))
		}
	}
}

// Clicking a resolution-header label acts on it, like the keys do — the
// enhancement in issue #41.
func TestResolutionHeaderClickActions(t *testing.T) {
	sh := shell.New()
	sh.SetSize(120, 30)
	newModel := func() Model {
		m := Model{sh: sh, section: secQuery, showRes: true, resPathOnly: true}
		m.resTree = &fga.ResNode{Name: "document:roadmap#viewer", Granted: true}
		m.result.vals = [3]string{"user:anne", "viewer", "document:roadmap"}
		return m
	}
	bx, by := sh.MainBodyOrigin()
	_, z := newModel().resHeader(bx)

	click := func(m Model, x, y int) Model {
		nm, _ := m.handleClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		return nm.(Model)
	}

	// "full tree" -> full tree; "ACL path" -> collapsed path.
	if m := click(newModel(), z.full[0], by); m.resPathOnly {
		t.Error("clicking 'full tree' should set resPathOnly=false")
	}
	m := newModel()
	m.resPathOnly = false
	if m := click(m, z.path[0], by); !m.resPathOnly {
		t.Error("clicking 'ACL path' should set resPathOnly=true")
	}
	// "p toggle" flips the current view.
	if m := click(newModel(), z.toggle[0], by); m.resPathOnly {
		t.Error("clicking 'p toggle' should flip resPathOnly true->false")
	}
	// "r/esc close" closes the resolution view.
	if m := click(newModel(), z.close[0], by); m.showRes {
		t.Error("clicking 'r/esc close' should close the resolution view")
	}
	// The scroll hint and gaps between labels are inert; the view stays open and
	// unchanged.
	if m := click(newModel(), bx, by); !m.showRes || !m.resPathOnly {
		t.Error("a click left of the labels must not change anything")
	}
	// A click off the header row changes nothing.
	if m := click(newModel(), z.full[0], by+5); !m.showRes || !m.resPathOnly {
		t.Error("a click off the header row must not change anything")
	}
}
