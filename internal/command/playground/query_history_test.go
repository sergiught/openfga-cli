package playground

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/ui/shell"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// queryModelWithHistory returns a model sitting in the Tuple Queries panel
// with three rerunnable entries.
func queryModelWithHistory() tea.Model {
	m := newTestModel().(Model)
	m.section = secQuery
	m.focus = shell.FocusPanel
	m.history = []histEntry{
		{mode: "check", vals: [3]string{"user:anne", "reader", "doc:budget"}, ok: true},
		{mode: "check", vals: [3]string{"user:bob", "owner", "doc:plan"}, ok: false},
		{mode: "list-objects", vals: [3]string{"user:anne", "reader", "doc"}},
	}
	return m
}

func TestHistoryPickerOpensOnH(t *testing.T) {
	m := queryModelWithHistory()

	m, _ = m.Update(key("h"))
	mm := m.(Model)
	if !mm.historyPicking {
		t.Fatal("h should open the history picker")
	}
	title, body := mm.dialogContent()
	if title != "Recent queries" {
		t.Fatalf("dialog title = %q, want %q", title, "Recent queries")
	}
	if !strings.Contains(body, "doc:budget") {
		t.Fatalf("picker should list history entries:\n%s", body)
	}
}

func TestHistoryPickerEmptyDoesNotOpen(t *testing.T) {
	mm := newTestModel().(Model)
	mm.section = secQuery
	mm.focus = shell.FocusPanel
	var m tea.Model = mm

	m, cmd := m.Update(key("h"))
	if m.(Model).historyPicking {
		t.Fatal("an empty history must not open an empty modal")
	}
	if cmd == nil {
		t.Fatal("empty history should raise an informational toast")
	}
	if levels := m.(Model).toasts.Levels(); len(levels) == 0 || levels[len(levels)-1] != toast.Info {
		t.Fatalf("empty history should push an Info toast, got %v", levels)
	}
}

func TestHistoryPickerEscCloses(t *testing.T) {
	m := queryModelWithHistory()

	m, _ = m.Update(key("h"))
	m, _ = m.Update(key("esc"))
	mm := m.(Model)
	if mm.historyPicking {
		t.Fatal("esc should close the picker")
	}
	if mm.focus != shell.FocusPanel {
		t.Fatal("esc in the picker closes the overlay, not the panel")
	}
}

func TestHistoryPickerEnterReruns(t *testing.T) {
	m := queryModelWithHistory()

	m, _ = m.Update(key("h"))
	mm := m.(Model)
	// Move off the default cursor (index 0) so this test can't pass against a
	// bug that hardcodes rerunHistory(0) regardless of what's selected.
	mm.historyList.SelectIndex(1)
	var tm tea.Model = mm
	tm, cmd := tm.Update(key("enter"))
	mm = tm.(Model)
	if mm.historyPicking {
		t.Fatal("enter should close the picker")
	}
	if cmd == nil {
		t.Fatal("enter should dispatch the rerun command")
	}
	// rerunHistory refills the query form from the selected entry — assert it
	// used the second fixture entry (user:bob/owner/doc:plan), not the first.
	if mm.qform == nil {
		t.Fatal("rerun should have rebuilt the query form")
	}
	if mm.qmode != queryModeIndex("check") {
		t.Fatalf("qmode = %d, want the check mode of the selected entry", mm.qmode)
	}
	want := []string{"user:bob", "owner", "doc:plan"}
	got := mm.qform.Values()
	if len(got) < 3 || strings.Join(got[:3], ",") != strings.Join(want, ",") {
		t.Fatalf("qform values = %v, want the first 3 to be %v (the selected, not the default, entry)", got, want)
	}
}

// TestHistoryPickerFilterNarrows reproduces the async list-filter wiring bug:
// activeList() had no secQuery case, so the FilterMatchesMsg the "/" filter
// depends on was never fed back to historyList and every row stayed visible.
func TestHistoryPickerFilterNarrows(t *testing.T) {
	m := queryModelWithHistory()

	m, _ = m.Update(key("h"))
	mm := m.(Model)
	if got := len(mm.historyList.Model.VisibleItems()); got != 3 {
		t.Fatalf("precondition: expected 3 visible rows before filtering, got %d", got)
	}

	var tm tea.Model = mm
	tm = pump(t, tm, key("/"))
	for _, r := range "bob" {
		tm = pump(t, tm, key(string(r)))
	}
	mm = tm.(Model)
	if !mm.historyList.SettingFilter() {
		t.Fatal("expected to be mid-typing a filter")
	}
	// Only the user:bob entry's filter text ("check doc:plan#owner@user:bob")
	// contains "bob".
	if got := len(mm.historyList.Model.VisibleItems()); got != 1 {
		t.Fatalf("filtering \"bob\" should narrow to 1 visible row, got %d", got)
	}
}

// TestHistoryPickerFooterAndBreadcrumb pins the picker to the same footer and
// header contract as the model switcher it was modeled on: with a modal open,
// the footer must advertise the modal's keys (the query panel's i/tab/h/r are
// all inert inside it) and the header must say which sub-mode you are in.
func TestHistoryPickerFooterAndBreadcrumb(t *testing.T) {
	m := queryModelWithHistory()

	m, _ = m.Update(key("h"))
	mm := m.(Model)

	keys := strings.Join(mm.statusKeys(), " ")
	for _, inert := range []string{"i/↵ edit", "tab mode", "h rerun", "r resolve"} {
		if strings.Contains(keys, inert) {
			t.Fatalf("footer advertises %q, which does nothing inside the picker: %q", inert, keys)
		}
	}
	// "↵ rerun" rather than the model picker's "↵ select": the footer has to
	// agree with the modal's own "enter rerun" hint.
	for _, want := range []string{"↑↓ browse", "↵ rerun", "esc"} {
		if !strings.Contains(keys, want) {
			t.Fatalf("footer missing the picker's own key %q: %q", want, keys)
		}
	}

	if got, want := mm.mainTitle(), sectionNames[secQuery]+" ▸ Recent queries"; got != want {
		t.Fatalf("mainTitle = %q, want the sub-mode breadcrumb %q", got, want)
	}
}

// TestHistoryPickerRerunsTheRowShown reproduces the index shift: pushHistory
// prepends, so a query result landing while the picker is open renumbers every
// entry under the rows the user is looking at. Selecting "user:bob" must rerun
// user:bob, not whatever slid into its old slot.
func TestHistoryPickerRerunsTheRowShown(t *testing.T) {
	m := queryModelWithHistory()

	m, _ = m.Update(key("h"))
	mm := m.(Model)
	mm.historyList.SelectIndex(1) // the user:bob row, as displayed

	// An in-flight query lands while the picker is open (reachable because
	// enter on the form leaves editing on: run → esc → h → result arrives).
	var tm tea.Model = mm
	tm, _ = tm.Update(queryResultMsg{
		storeID: "store-1", modelID: "model-1",
		mode: "check", vals: [3]string{"user:zoe", "viewer", "doc:late"}, ok: true, badge: true,
	})
	mm = tm.(Model)
	if !mm.historyPicking {
		t.Fatal("precondition: a landing result must not close the picker")
	}
	if len(mm.history) != 4 {
		t.Fatalf("precondition: the result should have been recorded, got %d entries", len(mm.history))
	}

	tm = mm
	tm, _ = tm.Update(key("enter"))
	mm = tm.(Model)
	want := []string{"user:bob", "owner", "doc:plan"}
	got := mm.qform.Values()
	if len(got) < 3 || strings.Join(got[:3], ",") != strings.Join(want, ",") {
		t.Fatalf("qform values = %v, want %v — the picker reran a different entry than the row it displayed", got, want)
	}
}

// TestHistoryPickerReopensUnfiltered pins that a filter does not outlive the
// picker: handleHistoryPicker takes esc before the list sees it, so the list
// never cancels its own filter and the next open would show a narrowed subset
// with no visible filter prompt to explain it.
func TestHistoryPickerReopensUnfiltered(t *testing.T) {
	tm := queryModelWithHistory()

	tm = pump(t, tm, key("h"))
	tm = pump(t, tm, key("/"))
	for _, r := range "bob" {
		tm = pump(t, tm, key(string(r)))
	}
	if got := len(tm.(Model).historyList.Model.VisibleItems()); got != 1 {
		t.Fatalf("precondition: filtering \"bob\" should narrow to 1 row, got %d", got)
	}
	tm = pump(t, tm, key("esc"))
	if tm.(Model).historyPicking {
		t.Fatal("precondition: esc should have closed the picker")
	}
	tm = pump(t, tm, key("h"))

	mm := tm.(Model)
	if got := len(mm.historyList.Model.VisibleItems()); got != 3 {
		t.Fatalf("a reopened picker must show all %d rows, got %d — the previous filter survived the close", 3, got)
	}
}

func TestDigitsJumpFromQueryPanel(t *testing.T) {
	m := queryModelWithHistory()

	// [2] on the tab must mean Stores everywhere, including here, where it
	// used to rerun history slot 2 instead.
	m, _ = m.Update(key("2"))
	if got := m.(Model).section; got != secStores {
		t.Fatalf("section = %v, want secStores", got)
	}
}

func TestHistoryStripHasNoDigitPrefix(t *testing.T) {
	m := queryModelWithHistory().(Model)

	out := ansi.Strip(m.historyStrip(120))
	if out == "" {
		t.Fatal("historyStrip returned nothing for seeded history")
	}
	// The leading digit advertised the old binding; left in place it would tell
	// users to press a key that now jumps sections. Assert on any digit rather
	// than the one removed formatting ("1 "), so reintroducing the accelerator
	// as "1.", "1)" or "[1]" fails too. The fixture's tuple values are
	// deliberately digit-free so nothing incidental can trip this — keep them
	// that way.
	if i := strings.IndexAny(out, "0123456789"); i >= 0 {
		t.Fatalf("history chips must not advertise digit shortcuts (found %q at %d):\n%s", out[i], i, out)
	}
}
