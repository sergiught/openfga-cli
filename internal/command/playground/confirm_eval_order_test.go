package playground

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// --- TUI-F8: the confirm modal's "enter"/"y" paths mutate the model through a
// pointer while returning the (value-receiver) model in the same return
// statement. Go only orders function calls left-to-right; the plain read of m
// relative to that call is unspecified. These are refactor guards: they assert
// the confirmed action's mutation is observable on the model bubbletea
// receives back, so a hoist to `cmd := run(&m); return m, cmd` can't silently
// regress it.

func TestConfirmModalRequirePathAppliesMutation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	applied := false
	mod.confirm = &confirmAction{
		action:  "Delete store",
		subject: "demo",
		require: "store-1",
		run: func(m *Model) tea.Cmd {
			applied = true
			m.storeDeleting = true
			m.beginLoad()
			return nil
		},
	}
	mod.confirm.input = "store-1"

	var tm tea.Model = mod
	tm, _ = tm.Update(key("enter"))
	m := tm.(Model)

	if !applied {
		t.Fatal("matching the require text and pressing enter should run the confirmed action")
	}
	if !m.storeDeleting {
		t.Fatal("the confirmed action's mutation (storeDeleting) must be observable on the returned model")
	}
	if !m.loading {
		t.Fatal("the confirmed action's beginLoad() must be observable on the returned model")
	}
	if m.confirm != nil {
		t.Fatal("the confirm modal should close once the action runs")
	}
}

func TestConfirmModalYesPathAppliesMutation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	applied := false
	mod.confirm = &confirmAction{
		action:  "Delete tuple",
		subject: "user:anne owner document:roadmap",
		run: func(m *Model) tea.Cmd {
			applied = true
			m.tupleMutating = true
			m.beginLoad()
			return nil
		},
	}

	var tm tea.Model = mod
	tm, _ = tm.Update(key("y"))
	m := tm.(Model)

	if !applied {
		t.Fatal("pressing y on a plain confirm should run the confirmed action")
	}
	if !m.tupleMutating {
		t.Fatal("the confirmed action's mutation (tupleMutating) must be observable on the returned model")
	}
	if !m.loading {
		t.Fatal("the confirmed action's beginLoad() must be observable on the returned model")
	}
	if m.confirm != nil {
		t.Fatal("the confirm modal should close once the action runs")
	}
}
