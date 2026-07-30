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
	// The leading digit advertised the old binding; left in place it would
	// tell users to press a key that now jumps sections. The fixture's tuple
	// values are deliberately digit-free so this substring check can't trip on
	// incidental content — keep them that way.
	if strings.Contains(out, "1 ") || strings.Contains(out, "2 ") {
		t.Fatalf("history chips must not advertise digit shortcuts:\n%s", out)
	}
}
