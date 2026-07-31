package playground

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/ui/shell"
)

// --- TUI-F8: enterQueryEdit mutates the model through a pointer (editing, and
// status on the no-store path) while the enclosing value-receiver methods
// returned it in the same return statement. Go only orders function calls
// left-to-right; the plain read of m relative to that call is unspecified, so
// the returned model could be the pre-mutation copy.
//
// These are refactor guards, not bug reproductions — the old form happened to
// work under the current compiler. They assert the mutation is observable on
// the model bubbletea receives back, so a future re-inlining to
// `return m, m.enterQueryEdit()` cannot silently regress it.

// queryPanelModel puts the model in the Tuple Queries panel, not editing.
func queryPanelModel(t *testing.T) tea.Model {
	t.Helper()
	m := newTestModel().(Model)
	m.section = secQuery
	m.focus = shell.FocusPanel
	m.editing = false
	return m
}

func TestQueryEditKeyAppliesMutation(t *testing.T) {
	for _, k := range []string{"i", "enter", "tab", "shift+tab"} {
		t.Run(k, func(t *testing.T) {
			tm := queryPanelModel(t)
			tm, _ = tm.Update(key(k))
			if !tm.(Model).editing {
				t.Fatalf("%q should leave editing observable on the returned model", k)
			}
		})
	}
}

// tab and shift+tab cycle the mode before entering the form; both mutations
// have to survive together, since the form is rebuilt from the new mode.
func TestQueryEditTabCyclesModeAndEdits(t *testing.T) {
	tm := queryPanelModel(t)
	before := tm.(Model).qmode

	tm, _ = tm.Update(key("tab"))
	m := tm.(Model)
	if !m.editing {
		t.Fatal("tab should leave editing observable on the returned model")
	}
	if m.qmode == before {
		t.Fatal("tab should also advance the query mode")
	}
}

// The no-store path mutates status instead of editing, and returns a nil cmd —
// the mutation is the only observable, so it is the easier one to lose.
func TestQueryEditWithoutStoreAppliesStatus(t *testing.T) {
	m := newTestModel().(Model)
	m.section = secQuery
	m.focus = shell.FocusPanel
	m.editing = false
	m.storeID = ""
	var tm tea.Model = m

	tm, _ = tm.Update(key("i"))
	got := tm.(Model)
	if got.editing {
		t.Fatal("editing must not start without a store")
	}
	if got.status != "select a store first" {
		t.Fatalf("status = %q, want the no-store message observable on the returned model", got.status)
	}
}

// onEnterSection's secQuery case runs the same call when descending into the
// panel, so it needs its own guard.
func TestOnEnterSectionQueryAppliesMutation(t *testing.T) {
	m := newTestModel().(Model)
	m.section = secStores
	m.focus = shell.FocusPanel
	var tm tea.Model = m

	// A digit jump lands in the panel, which is what triggers onEnterSection's
	// secQuery branch.
	tm, _ = tm.Update(key("6"))
	got := tm.(Model)
	if got.section != secQuery {
		t.Fatalf("section = %v, want secQuery", got.section)
	}
	if !got.editing {
		t.Fatal("descending into the query panel should leave editing observable on the returned model")
	}
}
