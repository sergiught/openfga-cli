package playground

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/configtest"
)

// editingQuery is a playground on the query section with its form focused.
func editingQuery(t *testing.T) tea.Model {
	t.Helper()
	configtest.Isolate(t)
	m := newTestModel().(Model)
	m.section = secQuery
	m.editing = true
	m.qform.FocusIndex(0)
	return m
}

func typeInto(tm tea.Model, s string) tea.Model {
	for _, r := range s {
		tm, _ = tm.Update(key(string(r)))
	}
	return tm
}

func TestTypingContinuesInPlaceAcrossAResize(t *testing.T) {
	tm := typeInto(editingQuery(t), "user:an")
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 130, Height: 32})
	tm = typeInto(tm, "ne")
	if got := tm.(Model).qform.Values()[0]; got != "user:anne" {
		t.Fatalf("user = %q, want %q", got, "user:anne")
	}
}

func TestAMidStringCaretSurvivesAResize(t *testing.T) {
	tm := typeInto(editingQuery(t), "user:ae")
	tm, _ = tm.Update(key("left"))
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 130, Height: 32})
	tm = typeInto(tm, "n")
	if got := tm.(Model).qform.Values()[0]; got != "user:ane" {
		t.Fatalf("user = %q, want %q", got, "user:ane")
	}
}

func TestTypingContinuesInPlaceAcrossTheContextToggle(t *testing.T) {
	tm := typeInto(editingQuery(t), "user:an")
	for range 3 {
		tm, _ = tm.Update(key("down"))
	}
	tm, _ = tm.Update(key(" ")) // flip the context toggle (index 3 in check mode)
	m := tm.(Model)
	if !m.qShowContext {
		t.Fatalf("context toggle did not flip; focus = %d", m.qform.FocusedIndex())
	}
	m.qform.FocusIndex(0)
	tm = typeInto(m, "ne")
	if got := tm.(Model).qform.Values()[0]; got != "user:anne" {
		t.Fatalf("user = %q, want %q", got, "user:anne")
	}
}

func TestTypingContinuesInPlaceAfterARun(t *testing.T) {
	tm := typeInto(editingQuery(t), "user:anne")
	tm, _ = tm.Update(key("down"))
	tm = typeInto(tm, "viewer")
	tm, _ = tm.Update(key("down"))
	tm = typeInto(tm, "document:roadmap")
	tm, _ = tm.Update(key("enter")) // on to the context toggle
	tm, _ = tm.Update(key("enter")) // run: the form is rebuilt and refilled
	tm = typeInto(tm, "2")
	if got := tm.(Model).qform.Values()[0]; got != "user:anne2" {
		t.Fatalf("user = %q, want %q", got, "user:anne2")
	}
}
