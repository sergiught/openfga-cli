package list

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// TUI-14: a bare quit key (q/esc) from a list-backed section must not hard-quit
// the whole TUI. The list's built-in quit keybindings must be disabled so the
// app's own key router owns quitting.
func TestQuitKeybindingsDisabled(t *testing.T) {
	l := New()
	if l.Model.KeyMap.Quit.Enabled() {
		t.Fatal("list quit keybinding should be disabled (app owns quitting)")
	}
	if l.Model.KeyMap.ForceQuit.Enabled() {
		t.Fatal("list force-quit keybinding should be disabled")
	}
	// A 'q' keypress must not produce a tea.Quit command.
	l.SetItems([]Item{{TitleText: "alpha"}, {TitleText: "beta"}})
	l.SetSize(40, 10)
	cmd := l.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatal("pressing q in a list must not quit the program")
		}
	}
}

// TUI-13: filtering must actually narrow the visible rows and move the selection
// onto the matching row, so that a delete keyed off SelectedItem hits the
// filtered match rather than the first (unfiltered) row. The bubbles list filter
// is asynchronous — SetFilterText applies it synchronously for the test.
func TestFilterNarrowsAndSelectsMatch(t *testing.T) {
	l := New()
	l.SetItems([]Item{
		{TitleText: "alpha", ID: "a", Index: 0},
		{TitleText: "beta", ID: "b", Index: 1},
		{TitleText: "gamma", ID: "g", Index: 2},
	})
	l.SetSize(40, 10)

	l.Model.SetFilterText("beta")

	sel, ok := l.Selected()
	if !ok {
		t.Fatal("expected a selected item after filtering")
	}
	if sel.ID != "b" || sel.Index != 1 {
		t.Fatalf("filter should select the matching row (beta, index 1), got ID=%q Index=%d", sel.ID, sel.Index)
	}
	view := ansi.Strip(l.View())
	if !strings.Contains(view, "beta") {
		t.Fatalf("filtered view should show the match, got:\n%s", view)
	}
	if strings.Contains(view, "alpha") || strings.Contains(view, "gamma") {
		t.Fatalf("filtered view should hide non-matching rows, got:\n%s", view)
	}
}

func TestSelectIDUsesFilteredIndex(t *testing.T) {
	l := New()
	l.SetSize(50, 12)
	l.SetItems([]Item{
		{TitleText: "dev", Filter: "dev", ID: "dev"},
		{TitleText: "prod", Filter: "prod", ID: "prod"},
	})
	l.Model.SetFilterText("prod")
	if !l.SelectID("prod") {
		t.Fatal("visible prod row was not found")
	}
	selected, ok := l.Selected()
	if !ok || selected.ID != "prod" {
		t.Fatalf("selected = %+v, %t; want prod", selected, ok)
	}
}

// The "/" filter is gated behind a keypress and easy to miss, so a populated
// list advertises it with a faint hint that the filter input replaces once the
// user starts filtering. An empty list shows no hint (nothing to filter).
func TestFilterHintAndPlaceholder(t *testing.T) {
	l := New()
	l.SetSize(40, 10)
	l.SetFilterHint("press / to filter")
	l.SetFilterPlaceholder("match any field")

	// Empty list: no hint.
	if got := ansi.Strip(l.View()); strings.Contains(got, "press / to filter") {
		t.Fatalf("empty list should not show the filter hint, got:\n%s", got)
	}

	// Populated, not filtering: hint is shown.
	l.SetItems([]Item{{TitleText: "alpha"}, {TitleText: "beta"}})
	if got := ansi.Strip(l.View()); !strings.Contains(got, "press / to filter") {
		t.Fatalf("populated list should advertise the filter, got:\n%s", got)
	}

	// While filtering with no input, the placeholder replaces the hint.
	l.Model.KeyMap.Filter.SetEnabled(true)
	l.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !l.SettingFilter() {
		t.Fatal("'/' should start filtering")
	}
	got := ansi.Strip(l.View())
	if strings.Contains(got, "press / to filter") {
		t.Fatalf("hint should be replaced by the filter input while filtering, got:\n%s", got)
	}
	if !strings.Contains(got, "match any field") {
		t.Fatalf("filter input should show the placeholder before any input, got:\n%s", got)
	}
}

func TestSetCompactHidesDescriptionsButKeepsTitles(t *testing.T) {
	l := New()
	l.SetItems([]Item{
		{TitleText: "alpha", DescText: "first"},
		{TitleText: "beta", DescText: "second"},
	})
	l.SetSize(40, 10)

	normal := l.View()
	if !strings.Contains(normal, "first") || !strings.Contains(normal, "second") {
		t.Fatalf("normal view should show descriptions, got:\n%s", normal)
	}

	l.SetCompact(true)
	compact := l.View()
	if strings.Contains(compact, "first") || strings.Contains(compact, "second") {
		t.Fatalf("compact view should hide descriptions, got:\n%s", compact)
	}
	if !strings.Contains(compact, "alpha") || !strings.Contains(compact, "beta") {
		t.Fatalf("compact view should still show titles, got:\n%s", compact)
	}

	// Rows must actually collapse to one line each, not just have their
	// description text hidden while still occupying a blank second line.
	lines := strings.Split(compact, "\n")
	alphaLine := -1
	for i, ln := range lines {
		if strings.Contains(ln, "alpha") {
			alphaLine = i
			break
		}
	}

	if alphaLine == -1 || alphaLine+1 >= len(lines) || !strings.Contains(lines[alphaLine+1], "beta") {
		t.Fatalf("compact view should render beta on the line immediately after alpha with no blank line between rows, got:\n%s", compact)
	}

	l.SetCompact(false)
	restored := l.View()
	if !strings.Contains(restored, "first") {
		t.Fatalf("toggling compact off should restore descriptions, got:\n%s", restored)
	}
}

func TestIndexAtAccountsForPersistentTitleRow(t *testing.T) {
	l := New()
	l.SetCompact(true)
	l.SetSize(40, 10)
	l.SetItems([]Item{{TitleText: "alpha"}, {TitleText: "beta"}})
	if got := l.IndexAt(0); got != -1 {
		t.Fatalf("title/filter row mapped to item %d", got)
	}
	if got := l.IndexAt(1); got != 0 {
		t.Fatalf("first compact item row mapped to %d, want 0", got)
	}
	if got := l.IndexAt(2); got != 1 {
		t.Fatalf("second compact item row mapped to %d, want 1", got)
	}
}
