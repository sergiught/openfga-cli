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

// resHeader's reported toggle ranges must line up with where the labels
// actually render, so a change to the header format can't silently break the
// click hit-testing that maps a column to a toggle.
func TestResHeaderTogglesMatchRenderedColumns(t *testing.T) {
	m := Model{}
	m.result.vals = [3]string{"user:anne", "viewer", "document:roadmap"}

	head, fullRange, pathRange := m.resHeader(0)
	vis := ansi.Strip(head)

	if got := colOf(vis, resToggleFull); got != fullRange[0] {
		t.Errorf("full tree renders at col %d, resHeader reports start %d", got, fullRange[0])
	}
	if got := fullRange[1] - fullRange[0]; got != lipgloss.Width(resToggleFull) {
		t.Errorf("full tree range width = %d, want %d", got, lipgloss.Width(resToggleFull))
	}
	if got := colOf(vis, resTogglePath); got != pathRange[0] {
		t.Errorf("ACL path renders at col %d, resHeader reports start %d", got, pathRange[0])
	}
	if got := pathRange[1] - pathRange[0]; got != lipgloss.Width(resTogglePath) {
		t.Errorf("ACL path range width = %d, want %d", got, lipgloss.Width(resTogglePath))
	}
}

// Clicking the "full tree" / "ACL path" labels in the resolution header switches
// the view, like the `p` key does — the enhancement in issue #41.
func TestResolutionHeaderClickTogglesView(t *testing.T) {
	sh := shell.New()
	sh.SetSize(120, 30)
	m := Model{sh: sh, section: secQuery, showRes: true, resPathOnly: true}
	m.resTree = &fga.ResNode{Name: "document:roadmap#viewer", Granted: true}
	m.result.vals = [3]string{"user:anne", "viewer", "document:roadmap"}

	bx, by := m.sh.MainBodyOrigin()
	_, fullRange, pathRange := m.resHeader(bx)

	click := func(m Model, x, y int) Model {
		nm, _ := m.handleClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		return nm.(Model)
	}

	// Starting from ACL-path view, click "full tree" -> full tree.
	m = click(m, fullRange[0], by)
	if m.resPathOnly {
		t.Fatal("clicking 'full tree' should set resPathOnly=false")
	}
	// Click "ACL path" -> collapsed path.
	m = click(m, pathRange[0], by)
	if !m.resPathOnly {
		t.Fatal("clicking 'ACL path' should set resPathOnly=true")
	}
	// A click on the header line but left of both labels changes nothing.
	m = click(m, bx, by)
	if !m.resPathOnly {
		t.Fatal("a click left of the toggles must not change the view")
	}
	// A click off the header row changes nothing.
	m.resPathOnly = false
	m = click(m, fullRange[0], by+5)
	if m.resPathOnly {
		t.Fatal("a click off the header row must not change the view")
	}
}
