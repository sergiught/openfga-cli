package playground

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// Ctrl+R jumps from the query editor straight to the resolution tree for the
// last check, without the esc-then-r two-step.
func TestQueryEditCtrlROpensResolution(t *testing.T) {
	m := newTestModel().(Model)
	m.section = secQuery
	m.editing = true
	m.hasResult = true
	m.result = queryResultMsg{badge: true, mode: "check", vals: [3]string{"user:vhs", "reader", "repo:acme/vhs-demo"}}

	got, cmd := m.handleQueryForm(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	mm := got.(Model)
	if mm.editing {
		t.Fatal("ctrl+r should drop out of edit mode so the resolution view takes over")
	}
	if cmd == nil {
		t.Fatal("ctrl+r with a completed check should dispatch the resolution load")
	}
}

// Without a check result, ctrl+r surfaces a "run a check first" hint toast
// rather than dispatching a resolution load (TUI-36: the guidance used to be set
// on m.status but never shown).
func TestQueryEditCtrlRNoResultIsHint(t *testing.T) {
	m := newTestModel().(Model)
	m.section = secQuery
	m.editing = true
	m.hasResult = false

	got, _ := m.handleQueryForm(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	mm := got.(Model)
	if !mm.editing {
		t.Fatal("ctrl+r without a result should stay in edit mode (no resolution load)")
	}
	if levels := mm.toasts.Levels(); len(levels) == 0 || levels[len(levels)-1] != toast.Info {
		t.Fatalf("ctrl+r without a result should push an Info hint toast, got %v", levels)
	}
}
