package list

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
		{TitleText: "prod2", Filter: "prod2", ID: "prod2"},
	})
	// Two rows survive the filter, so landing on the right one takes an actual
	// move — with a single match the cursor is already there either way.
	l.Model.SetFilterText("prod")
	if got := len(l.Model.VisibleItems()); got != 2 {
		t.Fatalf("the filter should leave two rows, got %d", got)
	}
	if !l.SelectID("prod2") {
		t.Fatal("visible prod2 row was not found")
	}
	selected, ok := l.Selected()
	if !ok || selected.ID != "prod2" {
		t.Fatalf("selected = %+v, %t; want prod2", selected, ok)
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

// A click on empty space below the last match must not select anything. The
// index IndexAt returns is fed to SelectIndex, which addresses the filtered
// rows, so bounding it by the unfiltered item count let a click past the end
// move the cursor to a row that is not rendered — leaving the pane with
// nothing highlighted.
func TestIndexAtIgnoresRowsBelowTheLastVisibleMatch(t *testing.T) {
	l := New()
	l.SetCompact(true)
	l.SetSize(40, 12)
	l.SetItems([]Item{
		{TitleText: "alpha"}, {TitleText: "beta"}, {TitleText: "gamma"},
		{TitleText: "delta"}, {TitleText: "epsilon"},
	})
	l.Model.SetFilterText("alpha")

	visible := len(l.Model.VisibleItems())
	if visible != 1 {
		t.Fatalf("filter matched %d items, want 1", visible)
	}
	// Row 0 is the title/filter bar, so row 1 is the only match.
	if got := l.IndexAt(1); got != 0 {
		t.Fatalf("the matching row mapped to %d, want 0", got)
	}
	for _, row := range []int{2, 3, 4} {
		if got := l.IndexAt(row); got != -1 {
			t.Errorf("row %d is below the last match but mapped to item %d", row, got)
		}
	}
}

// SetFilterPrompt renames the "/" input's label, for a section that also offers
// a filter of its own and needs the two named apart.
func TestSetFilterPrompt(t *testing.T) {
	l := New()
	l.SetSize(40, 10)
	l.SetFilterPrompt("find: ")
	l.SetItems([]Item{{TitleText: "alpha"}})
	l.Model.KeyMap.Filter.SetEnabled(true)
	l.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	got := ansi.Strip(l.View())
	if !strings.Contains(got, "find:") {
		t.Fatalf("the input should carry the overridden prompt, got:\n%s", got)
	}
	if strings.Contains(got, "filter:") {
		t.Fatalf("the default prompt should be gone, got:\n%s", got)
	}
}

// SetItems re-runs an applied filter over the new items itself. bubbles hands
// that back as a command, and a caller that routes messages by "the list on
// screen" would deliver it to the wrong list or drop it — leaving a filtered
// list rendering nothing.
func TestSetItemsReappliesFilter(t *testing.T) {
	l := New()
	l.SetSize(40, 10)
	l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}, {TitleText: "beta", Filter: "beta"}})
	l.Model.SetFilterText("alpha") // applies the filter synchronously
	if got := len(l.Model.VisibleItems()); got != 1 {
		t.Fatalf("the filter should narrow two items to one, got %d", got)
	}

	l.SetItems([]Item{
		{TitleText: "alpha", Filter: "alpha"},
		{TitleText: "beta", Filter: "beta"},
		{TitleText: "gamma", Filter: "gamma"},
	})
	if got := len(l.Model.VisibleItems()); got != 1 {
		t.Fatalf("replacing the items must re-run the applied filter, got %d visible", got)
	}
}

// The matches have to be in before the paginator is sized, or everything past
// the first page of a filtered list is unreachable.
func TestSetItemsRepaginatesAfterReapplyingFilter(t *testing.T) {
	l := New()
	l.SetSize(40, 20)
	items := func(n int) []Item {
		out := make([]Item, n)
		for i := range out {
			out[i] = Item{TitleText: "match", Filter: "match"}
		}
		return out
	}
	l.SetItems(items(60))
	l.Model.SetFilterText("match")
	want := l.Model.Paginator.TotalPages
	if want < 2 {
		t.Fatalf("test needs a multi-page list, got %d pages", want)
	}

	l.SetItems(items(60))
	if got := len(l.Model.VisibleItems()); got != 60 {
		t.Fatalf("every item still matches, got %d visible", got)
	}
	if got := l.Model.Paginator.TotalPages; got != want {
		t.Fatalf("pages = %d after replacing the items, want %d — rows past the first page are unreachable", got, want)
	}
}

// Replacing the items while the user is still typing must keep the matches in
// step too; the "/" input is drawn from the same model.
func TestSetItemsReappliesFilterWhileTyping(t *testing.T) {
	l := New()
	l.SetSize(40, 10)
	l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}, {TitleText: "beta", Filter: "beta"}})
	l.Model.KeyMap.Filter.SetEnabled(true)
	l.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	l.Model.FilterInput.SetValue("alpha")
	if !l.SettingFilter() {
		t.Fatal("expected to be mid-typing a filter")
	}

	l.SetItems([]Item{
		{TitleText: "alpha", Filter: "alpha"},
		{TitleText: "beta", Filter: "beta"},
		{TitleText: "gamma", Filter: "gamma"},
	})
	if got := len(l.Model.VisibleItems()); got != 1 {
		t.Fatalf("mid-typing, the matches must be recomputed too, got %d visible", got)
	}
}

// Re-running the pagination must land on a fixed point. In compact view a
// multi-page pager is a line taller than a single-page one, so sizing the rows
// while the page count still reads 1 leaves the list a line too tall — enough
// to push the app's footer off screen.
func TestSetItemsKeepsFilteredListWithinItsHeight(t *testing.T) {
	for _, compact := range []bool{false, true} {
		l := New()
		l.SetCompact(compact)
		l.SetSize(40, 20)
		items := make([]Item, 60)
		for i := range items {
			items[i] = Item{TitleText: "match", DescText: "d", Filter: "match"}
		}
		l.SetItems(items)
		l.Model.SetFilterText("match")
		want := l.Model.Paginator.PerPage

		l.SetItems(items)
		if got := l.Model.Paginator.PerPage; got != want {
			t.Errorf("compact=%v: rows per page = %d after replacing the items, want %d", compact, got, want)
		}
		if got := lipgloss.Height(l.View()); got > 20 {
			t.Errorf("compact=%v: list renders %d lines, more than the %d it was given", compact, got, 20)
		}
	}
}

// An applied filter hides its own input, so the title bar has to name the term
// or the narrowing is invisible — and it has to stay inside the pane, since an
// overflowing title wraps and pushes the app's footer off screen.
func TestApplyFilterHintNamesTheAppliedTerm(t *testing.T) {
	l := New()
	l.SetSize(24, 10)
	l.SetFilterHint("/ find")
	l.SetFilterPrompt("find: ")
	l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}, {TitleText: "beta", Filter: "beta"}})
	if got := ansi.Strip(l.View()); !strings.Contains(got, "/ find") {
		t.Fatalf("an unfiltered list should advertise the key, got:\n%s", got)
	}

	// The title has to track the filter state, not the last SetItems: applying a
	// filter runs no SetItems, so driving it from there alone left the title a
	// reload behind in both directions.
	l.Model.SetFilterText("alpha")
	l.Model.KeyMap.Filter.SetEnabled(true)
	l.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // any key through the app's path
	if got := ansi.Strip(l.View()); !strings.Contains(got, "find: alpha") {
		t.Fatalf("an applied filter should name its term, got:\n%s", got)
	}

	// And stop naming it the moment it is cleared.
	l.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	l.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if l.Model.FilterState() != list.Unfiltered {
		t.Fatalf("expected the filter cleared, got %v", l.Model.FilterState())
	}
	if got := ansi.Strip(l.View()); strings.Contains(got, "find: alpha") {
		t.Fatalf("a cleared filter must stop being named, got:\n%s", got)
	}

	l.Model.SetFilterText(strings.Repeat("x", 80))
	l.applyFilterHint()
	for _, line := range strings.Split(ansi.Strip(l.View()), "\n") {
		if got := lipgloss.Width(line); got > 24 {
			t.Fatalf("a long term must be truncated, got a %d-column line: %q", got, line)
		}
	}
}

// hintWidth's budget is measured rather than derived, so pin the measurement:
// bubbles truncates the title against the pane width but renders it in a padded
// bar, and an overflowing title wraps — which costs the app a row and pushes its
// footer off screen.
func TestTitleBarBudget(t *testing.T) {
	for _, w := range []int{21, 24, 40, 80} {
		l := New()
		l.SetSize(w, 10)
		l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}})
		l.Model.Title = strings.Repeat("t", l.hintWidth())
		if got := lipgloss.Width(ansi.Strip(l.View())); got > w {
			t.Errorf("w=%d: a title of hintWidth()=%d renders %d columns", w, l.hintWidth(), got)
		}
	}
}

func TestTruncateHint(t *testing.T) {
	for _, tc := range []struct {
		in   string
		w    int
		want string
	}{
		{"abcdefghij", 10, "abcdefghij"}, // exact fit
		{"abcdefghij", 20, "abcdefghij"},
		{"abcdefghij", 9, "abcdefgh…"},
		{"abcdefghij", 3, "ab…"},
		{"abcdefghij", 1, "…"},
	} {
		if got := truncateHint(tc.in, tc.w); got != tc.want {
			t.Errorf("truncateHint(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
	}
}

// hintWidth is a measured budget, so pin it from below as well: one column more
// must actually overflow, or the number has drifted and nothing would notice.
func TestTitleBarBudgetIsTight(t *testing.T) {
	for _, w := range []int{21, 24, 40, 80} {
		l := New()
		l.SetSize(w, 10)
		l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}})
		l.Model.Title = strings.Repeat("t", l.hintWidth()+1)
		if got := lipgloss.Width(ansi.Strip(l.View())); got <= w {
			t.Errorf("w=%d: hintWidth()=%d leaves room to spare — the budget has drifted", w, l.hintWidth())
		}
	}
}

// An applied filter names itself even when it matches nothing: that is the
// state in which the user most needs to know what is hiding the rows.
func TestApplyFilterHintNamesTheTermOverAnEmptyResult(t *testing.T) {
	l := New()
	l.SetSize(40, 10)
	l.SetFilterHint("/ find")
	l.SetFilterPrompt("find: ")
	l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}})
	l.Model.SetFilterText("zzzz")
	l.applyFilterHint()
	if got := ansi.Strip(l.View()); !strings.Contains(got, "find: zzzz") {
		t.Fatalf("an applied filter matching nothing should still name itself, got:\n%s", got)
	}
}

// An applied term outranks the empty-list case: a filter that matches nothing
// is exactly when the user most needs to see what is hiding the rows.
func TestAppliedTermSurvivesAnEmptiedList(t *testing.T) {
	l := New()
	l.SetSize(40, 10)
	l.SetFilterHint("/ find")
	l.SetFilterPrompt("find: ")
	l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}})
	l.Model.SetFilterText("alpha")
	l.SetItems(nil)
	if got := ansi.Strip(l.View()); !strings.Contains(got, "find: alpha") {
		t.Fatalf("the applied term should outlive an emptied list, got:\n%s", got)
	}
}

// The key hint shares the title bar with the applied term, so it needs the same
// budget: a hint longer than the pane wraps, and the row it costs is the app's
// footer. This is how the footer disappeared once already.
func TestFilterHintIsTruncatedToo(t *testing.T) {
	l := New()
	l.SetSize(20, 10)
	l.SetFilterHint("a hint far longer than this narrow pane can hold")
	l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}})
	for _, line := range strings.Split(ansi.Strip(l.View()), "\n") {
		if got := lipgloss.Width(line); got > 20 {
			t.Fatalf("an over-long hint must be truncated: %d columns, %q", got, line)
		}
	}
}

// A narrower pane makes an already-rendered title too long. Nothing else
// recomputes it, so a resize has to — an overflowing title wraps, and the row
// it costs is the one the app's footer sits on.
func TestSetSizeRetruncatesTheTitle(t *testing.T) {
	l := New()
	l.SetSize(120, 20)
	l.SetFilterPrompt("find: ")
	l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}})
	l.Model.SetFilterText(strings.Repeat("a", 60))
	l.applyFilterHint()

	l.SetSize(30, 20)
	if got := lipgloss.Width(l.Model.Title); got > l.hintWidth() {
		t.Fatalf("title is %d columns after shrinking to a %d-column budget", got, l.hintWidth())
	}
	for _, line := range strings.Split(ansi.Strip(l.View()), "\n") {
		if got := lipgloss.Width(line); got > 30 {
			t.Fatalf("line overflows the pane after a resize: %d columns, %q", got, line)
		}
	}
}

// ResetFilter has to put the title bar back too: reaching through to
// Model.ResetFilter leaves it naming a filter that is no longer applied.
func TestResetFilterClearsTheTitle(t *testing.T) {
	l := New()
	l.SetSize(40, 10)
	l.SetFilterHint("/ find")
	l.SetFilterPrompt("find: ")
	l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}, {TitleText: "beta", Filter: "beta"}})
	l.Model.SetFilterText("alpha")
	l.applyFilterHint()
	if got := ansi.Strip(l.View()); !strings.Contains(got, "find: alpha") {
		t.Fatalf("expected an applied filter to start with, got:\n%s", got)
	}

	l.ResetFilter()
	if got := ansi.Strip(l.View()); strings.Contains(got, "find: alpha") {
		t.Fatalf("a cleared filter must stop being named, got:\n%s", got)
	}
	if got := len(l.Model.VisibleItems()); got != 2 {
		t.Fatalf("every row should be back, got %d", got)
	}
}

// A reload can return fewer rows than the cursor was sitting on. bubbles clamps
// against the unfiltered set, so under an applied filter the cursor can end up
// past the last visible row and the pane renders with nothing highlighted.
func TestSetItemsClampsTheCursorToTheVisibleRows(t *testing.T) {
	l := New()
	l.SetSize(40, 20)
	rows := func(n int) []Item {
		out := make([]Item, n)
		for i := range out {
			out[i] = Item{TitleText: "match", Filter: "match", ID: strconv.Itoa(i)}
		}
		return out
	}
	l.SetItems(rows(120))
	l.Model.SetFilterText("match")
	for i := 0; i < 40; i++ {
		l.Model.CursorDown()
	}
	if l.Model.Index() < 5 {
		t.Fatalf("expected the cursor well down the list, got %d", l.Model.Index())
	}

	l.SetItems(rows(2))
	if got, n := l.Model.Index(), len(l.Model.VisibleItems()); got >= n {
		t.Fatalf("cursor at %d with %d visible rows — nothing would be highlighted", got, n)
	}
	// It lands on the last visible row, not back at the top: the user was near
	// the end of the list and that is the nearest valid row.
	if got := l.Model.Index(); got != 1 {
		t.Fatalf("cursor = %d, want the last visible row (1)", got)
	}
	if _, ok := l.Selected(); !ok {
		t.Fatal("a non-empty list must have a selected row")
	}
}

// The clamp measures the rows on screen, not the rows held: under a filter
// those differ, which is the whole reason it exists.
func TestSetItemsClampMeasuresTheVisibleRows(t *testing.T) {
	l := New()
	l.SetSize(40, 20)
	items := make([]Item, 120)
	for i := range items {
		items[i] = Item{TitleText: "row", Filter: "keep", ID: strconv.Itoa(i)}
	}
	l.SetItems(items)
	l.Model.SetFilterText("keep")
	for i := 0; i < 40; i++ {
		l.Model.CursorDown()
	}

	// Same 120 items, but only two still match.
	for i := range items {
		items[i].Filter = "gone"
	}
	items[0].Filter, items[1].Filter = "keep", "keep"
	l.SetItems(items)
	if n := len(l.Model.VisibleItems()); n != 2 {
		t.Fatalf("expected two matches, got %d", n)
	}
	if got := l.Model.Index(); got >= 2 {
		t.Fatalf("cursor at %d with 2 visible rows — measured the held rows, not the shown ones", got)
	}
}

// The boundary is inclusive: an index equal to the visible count is already one
// past the last row, so ">" instead of ">=" leaves it orphaned.
func TestSetItemsClampBoundaryIsInclusive(t *testing.T) {
	l := New()
	l.SetSize(40, 20)
	items := make([]Item, 12)
	for i := range items {
		items[i] = Item{TitleText: "row", Filter: "keep", ID: strconv.Itoa(i)}
	}
	l.SetItems(items)
	l.Model.SetFilterText("keep")
	l.Model.Select(3)

	l.SetItems(items[:3]) // exactly one fewer row than the cursor's index
	if got := len(l.Model.VisibleItems()); got != 3 {
		t.Fatalf("expected three rows, got %d", got)
	}
	if got := l.Model.Index(); got != 2 {
		t.Fatalf("cursor = %d with 3 rows, want the last one (2)", got)
	}
	if _, ok := l.Selected(); !ok {
		t.Fatal("a non-empty list must have a selected row")
	}
}

// An empty visible set has no row to land on; the cursor must not go negative,
// which nothing later would repair.
func TestSetItemsClampLeavesAnEmptyListAlone(t *testing.T) {
	l := New()
	l.SetSize(40, 20)
	l.SetItems([]Item{{TitleText: "alpha", Filter: "alpha"}})
	l.Model.SetFilterText("alpha")
	l.SetItems(nil)
	if got := l.Model.Index(); got < 0 {
		t.Fatalf("cursor = %d, want a valid index", got)
	}
}
