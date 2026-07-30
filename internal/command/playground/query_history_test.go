package playground

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/ui/shell"
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
	m, cmd := m.Update(key("enter"))
	mm := m.(Model)
	if mm.historyPicking {
		t.Fatal("enter should close the picker")
	}
	if cmd == nil {
		t.Fatal("enter should dispatch the rerun command")
	}
	// rerunHistory refills the query form from the selected entry.
	if mm.qform == nil {
		t.Fatal("rerun should have rebuilt the query form")
	}
}
