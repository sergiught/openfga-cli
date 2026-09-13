package mapping

import (
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// atRuleHub returns a wizard with a model and one rule, on the rule hub.
func atRuleHub(t *testing.T) *wizardModel {
	t.Helper()
	m := atTuples(t)
	send(m, key("esc"))
	if m.top() != screenRule {
		t.Fatalf("top = %v", m.top())
	}
	return m
}

func openSection(t *testing.T, m *wizardModel, cursor int, want screen) {
	t.Helper()
	m.sections.SetCursor(cursor)
	send(m, key("enter"))
	if m.top() != want {
		t.Fatalf("top = %v, want %v", m.top(), want)
	}
}

func TestActionEditorSetsAndClearsTheRuleAction(t *testing.T) {
	m := atRuleHub(t)
	openSection(t, m, 1, screenAction)

	if !selectByTitle(m.actionPick, "delete") {
		t.Fatalf("delete not offered: %v", titles(m.actionPick))
	}
	send(m, key("enter"))
	if got := m.rule().Action; got != "delete" {
		t.Fatalf("action = %q", got)
	}
	if m.top() != screenRule {
		t.Fatalf("top = %v", m.top())
	}

	openSection(t, m, 1, screenAction)
	selectByTitle(m.actionPick, "Per tuple")
	send(m, key("enter"))
	if got := m.rule().Action; got != "" {
		t.Fatalf("action = %q, want cleared", got)
	}
}

func TestVariablesAddEditDelete(t *testing.T) {
	m := atRuleHub(t)
	openSection(t, m, 2, screenVariables)

	send(m, key("a"))
	if m.top() != screenVariable {
		t.Fatalf("top = %v", m.top())
	}
	m.varForm.SetValues([]string{"org", "input.data.object.organization.id"})
	send(m, key("esc"))

	vs := m.rule().Variables
	if len(vs) != 1 || vs[0].Name != "org" || vs[0].Expr != "input.data.object.organization.id" {
		t.Fatalf("variables = %+v", vs)
	}
	if !strings.Contains(m.viewString(), "org") {
		t.Fatalf("list should show the variable:\n%s", m.viewString())
	}

	send(m, key("d"))
	if len(m.rule().Variables) != 0 {
		t.Fatalf("variables = %+v", m.rule().Variables)
	}
}

func TestVariablesAreUsableInTuples(t *testing.T) {
	m := atRuleHub(t)
	// A rule needs a name to compile at all; atRuleHub leaves it unset (it
	// never visits the event picker, which is what normally fills it in), so
	// this test sets one directly rather than exercising that unrelated flow.
	m.rule().Name = "organization.member.added"
	m.rule().Sample = &mapping.Sample{Label: "organization.member.added", Event: memberAddedEvent()}
	m.rule().Variables = []mapping.Variable{{Name: "org", Expr: "input.data.object.organization.id"}}
	m.rule().Tuples = []mapping.Tuple{{
		User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
		Relation: "member",
		Object:   "organization:{{ variables.org }}",
	}}
	m.refresh()

	if len(m.preview.Tuples) != 1 {
		t.Fatalf("preview = %+v (diagnostics %v)", m.preview.Tuples, m.preview.Diagnostics)
	}
	if got := m.preview.Tuples[0].Object; got != "organization:org_1234567890abcdef" {
		t.Fatalf("object = %q", got)
	}
}

func TestIteratorFormAndNestedTuples(t *testing.T) {
	m := atRuleHub(t)
	openSection(t, m, 3, screenIterator)

	m.iterForm.SetValues([]string{"input.data.object.identities", "identity"})
	send(m, key("ctrl+t")) // ctrl+t opens the iterator's tuples

	if m.top() != screenTuples {
		t.Fatalf("top = %v", m.top())
	}
	if !m.inIter {
		t.Fatal("the tuple editor should be bound to the iterator")
	}
	send(m, key("a"))
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{
		fieldObject:   "connection:{{ identity.connection }}",
		fieldRelation: "member",
		fieldUser:     "user:{{ fga_escape(input.data.object.user_id) }}",
	}))
	send(m, key("esc"))

	it := m.rule().Iterator
	if it == nil {
		t.Fatal("no iterator")
	}
	if it.Source != "input.data.object.identities" || it.As != "identity" {
		t.Fatalf("iterator = %+v", it)
	}
	if len(it.Tuples) != 1 {
		t.Fatalf("iterator tuples = %+v", it.Tuples)
	}
	if len(m.rule().Tuples) != 0 {
		t.Fatalf("the rule's own tuples were touched: %+v", m.rule().Tuples)
	}
}

// TestTypingIntoIteratorSourceStaysOnTheForm guards against binding "edit
// tuples" to a bare "t": the iterator screen is a text-entry form, and a path
// like "input.data.object.accounts" contains the letter t several times. Every
// one of those keystrokes must land in the Source field, not open the tuple
// list.
func TestTypingIntoIteratorSourceStaysOnTheForm(t *testing.T) {
	m := atRuleHub(t)
	openSection(t, m, 3, screenIterator)

	typeText(m, "input.data.object.accounts")
	if got := m.iterForm.Values()[0]; got != "input.data.object.accounts" {
		t.Fatalf("source = %q", got)
	}
	if m.top() != screenIterator {
		t.Fatalf("top = %v, want screenIterator", m.top())
	}
}

func TestLeavingTheIteratorRebindsTheTupleEditor(t *testing.T) {
	m := atRuleHub(t)
	openSection(t, m, 3, screenIterator)
	m.iterForm.SetValues([]string{"input.data.object.identities", "identity"})
	send(m, key("ctrl+t"), key("esc"), key("esc")) // tuples -> iterator -> rule hub

	if m.inIter {
		t.Fatal("inIter must be cleared on the way out")
	}
	openSection(t, m, 5, screenTuples)
	send(m, key("a"), key("esc"))
	if len(m.rule().Tuples) != 1 {
		t.Fatalf("rule tuples = %+v", m.rule().Tuples)
	}
	if len(m.rule().Iterator.Tuples) != 0 {
		t.Fatalf("iterator tuples = %+v", m.rule().Iterator.Tuples)
	}
}

func TestClearingTheIteratorSourceRemovesIt(t *testing.T) {
	m := atRuleHub(t)
	openSection(t, m, 3, screenIterator)
	m.iterForm.SetValues([]string{"input.data.object.identities", "identity"})
	send(m, key("esc"))
	if m.rule().Iterator == nil {
		t.Fatal("iterator should exist")
	}

	openSection(t, m, 3, screenIterator)
	m.iterForm.SetValues([]string{"", ""})
	send(m, key("esc"))
	if m.rule().Iterator != nil {
		t.Fatalf("an empty source should drop the iterator: %+v", m.rule().Iterator)
	}
}

func TestFiltersAddAndCapAtThree(t *testing.T) {
	m := atRuleHub(t)
	openSection(t, m, 4, screenFilters)

	for i := 0; i < 3; i++ {
		send(m, key("a"))
		m.filterForm.SetValues([]string{"", "member", "organization:{{ input.data.object.organization.id }}", "delete"})
		send(m, key("esc"))
	}
	if got := len(m.rule().Filters); got != 3 {
		t.Fatalf("filters = %d", got)
	}

	send(m, key("a"))
	if got := len(m.rule().Filters); got != 3 {
		t.Fatalf("the cap should hold at 3, got %d", got)
	}
	if m.errMsg == "" {
		t.Fatal("the cap should explain itself")
	}
	if m.top() != screenFilters {
		t.Fatalf("top = %v", m.top())
	}
}

func TestFilterRoundTripsThroughTheDocument(t *testing.T) {
	m := atRuleHub(t)
	openSection(t, m, 4, screenFilters)
	send(m, key("a"))
	m.filterForm.SetValues([]string{
		"user:{{ fga_escape(input.data.object.user.user_id) }}", "", "", "delete",
	})
	send(m, key("esc"))

	f := m.rule().Filters[0]
	if f.User == "" || f.Action != "delete" {
		t.Fatalf("filter = %+v", f)
	}
	items := m.filterList.Model.Items()
	if len(items) != 1 {
		t.Fatalf("filter list items = %d, want 1", len(items))
	}
	if title := items[0].(uilist.Item).TitleText; !strings.Contains(title, "fga_escape") {
		t.Fatalf("the list should show the filter:\n%s", title)
	}
}

func TestRuleHubSummariesReflectEverySection(t *testing.T) {
	m := atRuleHub(t)
	m.rule().Action = "delete"
	m.rule().Variables = []mapping.Variable{{Name: "org", Expr: "input.x"}}
	m.rule().Iterator = &mapping.Iterator{Source: "input.data.object.identities", As: "identity"}
	m.rule().Filters = []mapping.TupleFilter{{Object: "organization:1", Action: "delete"}}
	m.refresh()

	v := m.viewString()
	for _, want := range []string{"delete", "1 variable", "identity", "1 filter"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
}
