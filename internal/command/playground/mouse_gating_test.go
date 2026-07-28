package playground

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/go-openfga/openfga"
)

// --- TUI-F4: mouse wheel must be gated under the takeover-form dialogs and
// routed to the model picker's own list instead of the graph behind it.

func threeTuples() []openfga.Tuple {
	return []openfga.Tuple{
		{Key: openfga.TupleKey{User: "user:anne", Relation: "owner", Object: "document:roadmap"}},
		{Key: openfga.TupleKey{User: "user:bob", Relation: "viewer", Object: "document:roadmap"}},
		{Key: openfga.TupleKey{User: "user:carl", Relation: "viewer", Object: "document:other"}},
	}
}

func TestWheelGatedUnderTakeoverForm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	var tm tea.Model = mod
	tm, _ = tm.Update(tuplesLoadedMsg{tuples: threeTuples()})
	m := tm.(Model)
	m.section = secTuples
	m.formKind = formWriteTuple

	before, ok := m.tuplesList.Selected()
	if !ok {
		t.Fatal("expected a selected tuple before the wheel event")
	}

	got, _ := m.handleWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	gm := got.(Model)
	after, ok := gm.tuplesList.Selected()
	if !ok {
		t.Fatal("expected a selected tuple after the wheel event")
	}
	if after.ID != before.ID {
		t.Fatalf("wheel while the Write Tuple dialog is open must not move the underlying list selection; before=%q after=%q", before.ID, after.ID)
	}
}

func TestWheelRoutedToModelPickerList(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	var tm tea.Model = mod
	tm, _ = tm.Update(modelsListedMsg{models: []openfga.AuthorizationModel{
		{ID: "model-1", SchemaVersion: "1.1"},
		{ID: "model-2", SchemaVersion: "1.1"},
	}})
	m := tm.(Model)
	m.section = secModel
	m.modelPicking = true

	before, ok := m.modelsList.Selected()
	if !ok {
		t.Fatal("expected a selected model before the wheel event")
	}

	got, _ := m.handleWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	gm := got.(Model)
	if gm.graphAnimating || gm.graphTarget != 0 {
		t.Fatal("wheel while the model picker is open must not scroll the graph behind it")
	}
	after, ok := gm.modelsList.Selected()
	if !ok {
		t.Fatal("expected a selected model after the wheel event")
	}
	if after.ID == before.ID {
		t.Fatal("wheel while the model picker is open should move its own list's selection")
	}
}
