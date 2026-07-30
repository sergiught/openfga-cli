package playground

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// emitsClearScreen reports whether running cmd (recursing into batches) yields
// bubbletea's clear-screen message, i.e. a forced full repaint.
func emitsClearScreen(cmd tea.Cmd) bool {
	var queue []tea.Msg
	collectCmd(cmd, &queue)
	want := tea.ClearScreen()
	for _, msg := range queue {
		if reflect.DeepEqual(msg, want) {
			return true
		}
	}
	return false
}

// A terminal-side screen wipe (macOS ⌘K in Terminal.app / iTerm2 clears the
// buffer without telling the app) leaves bubbletea's cell buffer describing a
// screen that no longer exists. The renderer only ever writes diffs against
// that stale buffer, so the UI stays blank apart from stray updated cells.
// Ctrl+L is the conventional "redraw" escape hatch out of that state.
func TestCtrlLForcesRepaint(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if !emitsClearScreen(cmd) {
		t.Fatal("ctrl+l must force a full repaint (tea.ClearScreen)")
	}
}

// The redraw key is worthless if an overlay can swallow it: the screen is
// already unreadable when the user reaches for it, so they cannot know what
// state the UI is in. Like Ctrl+C, it must be handled before every overlay,
// form, editor and list-filter branch.
func TestCtrlLForcesRepaintUnderOverlays(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(Model) Model
	}{
		{"help overlay", func(m Model) Model { m.helpOpen = true; return m }},
		{"command palette", func(m Model) Model { m.paletteOpen = true; return m }},
		{"error dialog", func(m Model) Model { m.formErr = "boom"; return m }},
		{"form takeover", func(m Model) Model {
			nm, _ := m.enterForm(formWriteTuple)
			return nm.(Model)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup(newTestModel().(Model))
			_, cmd := m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
			if !emitsClearScreen(cmd) {
				t.Fatalf("ctrl+l must repaint even with the %s open", tc.name)
			}
		})
	}
}

// Regaining terminal focus auto-heals the common sequence: ⌘K wipes the
// screen, the user switches to another app to figure out what happened, then
// switches back. Requires View.ReportFocus so the terminal sends the event.
func TestFocusRegainForcesRepaint(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(tea.FocusMsg{})
	if !emitsClearScreen(cmd) {
		t.Fatal("regaining focus must force a full repaint")
	}
}

func TestReportFocusEnabled(t *testing.T) {
	if !newTestModel().(Model).View().ReportFocus {
		t.Fatal("View.ReportFocus must be set or the terminal never sends tea.FocusMsg")
	}
}

// A recovery key the user cannot discover does not help them.
func TestHelpDocumentsRedrawKey(t *testing.T) {
	m := newTestModel().(Model)
	if !strings.Contains(m.helpBody(), "ctrl+l") {
		t.Fatal("the help overlay must document ctrl+l")
	}
}
