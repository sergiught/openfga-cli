package playground

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/configtest"
)

// --- TUI-F3 (+F14): resize() must not yank focus off a field the user is
// mid-typing in. A background load (assertions, etc.) or a same-width
// WindowSizeMsg both call resize(), which used to rebuild the query form
// unconditionally and refocus field 0 via qform.Init().

func TestQueryFormFocusSurvivesBackgroundAssertionsLoad(t *testing.T) {
	configtest.Isolate(t)
	mod := newTestModel().(Model)
	mod.section = secQuery
	mod.editing = true
	mod.qform.FocusIndex(2) // "Object" field in check mode

	var tm tea.Model = mod
	tm, _ = tm.Update(key("x"))
	m := tm.(Model)
	if got := m.qform.FocusedIndex(); got != 2 {
		t.Fatalf("typing must not move focus; focus = %d, want 2", got)
	}
	if got := m.qform.Values()[2]; got != "x" {
		t.Fatalf("typed value = %q, want %q", got, "x")
	}

	// A slow assertions load lands while the user is still on field 2.
	tm, _ = tm.Update(assertionsLoadedMsg{modelID: "model-1", assertions: []openfga.Assertion{
		{TupleKey: openfga.CheckRequestTupleKey{User: "user:anne", Relation: "viewer", Object: "document:roadmap"}, Expectation: true},
	}})
	m = tm.(Model)
	if got := m.qform.FocusedIndex(); got != 2 {
		t.Fatalf("background assertions load must not yank focus off field 2; focus = %d", got)
	}
	if got := m.qform.Values()[2]; got != "x" {
		t.Fatalf("in-progress input must survive the background load; value = %q, want %q", got, "x")
	}
}

func TestQueryFormFocusSurvivesSameWidthWindowResize(t *testing.T) {
	configtest.Isolate(t)
	mod := newTestModel().(Model) // already sized to 110x32
	mod.section = secQuery
	mod.editing = true
	mod.qform.FocusIndex(2)

	var tm tea.Model = mod
	tm, _ = tm.Update(key("y"))
	m := tm.(Model)
	if got := m.qform.FocusedIndex(); got != 2 {
		t.Fatalf("typing must not move focus; focus = %d, want 2", got)
	}

	// Same dimensions as before: a spurious WindowSizeMsg (e.g. a terminal
	// reporting its size without an actual change) must not rebuild the form.
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m = tm.(Model)
	if got := m.qform.FocusedIndex(); got != 2 {
		t.Fatalf("a same-width WindowSizeMsg must not move focus off field 2; focus = %d", got)
	}
	if got := m.qform.Values()[2]; got != "y" {
		t.Fatalf("in-progress input must survive a same-width resize; value = %q, want %q", got, "y")
	}
}
