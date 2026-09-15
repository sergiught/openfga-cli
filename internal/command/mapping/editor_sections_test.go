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

// atIterForm walks the rule hub into the iterator hub and opens its Source/As
// form, which is where the two scalar fields live now that the iterator's
// tuples have a row of their own.
func atIterForm(t *testing.T, m *wizardModel) {
	t.Helper()
	openSection(t, m, 3, screenIterator)
	send(m, key("enter")) // Source, the first row
	if m.top() != screenIterForm {
		t.Fatalf("top = %v, want the iterator form", m.top())
	}
}

// selectIterRow puts the iterator hub's cursor on the named row.
func selectIterRow(t *testing.T, m *wizardModel, value string) {
	t.Helper()
	if m.top() != screenIterator {
		t.Fatalf("top = %v, want the iterator hub", m.top())
	}
	m.iterHub.SetCursor(0)
	for i := 0; i < m.iterHub.Len(); i++ {
		if m.iterHub.Selected().Value == value {
			return
		}
		m.iterHub.Move(1)
	}
	t.Fatalf("no %q row on the iterator hub", value)
}

// openIterTuples opens the iterator's own tuple list. The caller is already on
// the iterator hub.
func openIterTuples(t *testing.T, m *wizardModel) {
	t.Helper()
	selectIterRow(t, m, "tuples")
	send(m, key("enter"))
	if m.top() != screenTuples || !m.inIter {
		t.Fatalf("top = %v, inIter = %v, want the iterator's tuple list", m.top(), m.inIter)
	}
}

func TestIteratorFormAndNestedTuples(t *testing.T) {
	m := atRuleHub(t)
	atIterForm(t, m)

	m.iterForm.SetValues([]string{"input.data.object.identities", "identity"})
	send(m, key("esc")) // commit the form, back to the iterator hub

	openIterTuples(t, m)
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

// The iterator's tuples are a row you arrow onto, like every other list in this
// wizard. They used to be reachable only by a ^t chord from inside the form,
// which is what "in the iterator I can't select tuples" was about: two text
// fields, no list, and nothing on screen to select.
func TestTheIteratorHubOffersItsTuplesAsARow(t *testing.T) {
	m := atRuleHub(t)
	atIterForm(t, m)
	m.iterForm.SetValues([]string{"input.data.object.identities", "identity"})
	send(m, key("esc"))

	var titles []string
	for _, it := range m.iteratorSections() {
		titles = append(titles, it.Title)
	}
	want := []string{"Source", "As", "Tuples"}
	if len(titles) != len(want) {
		t.Fatalf("iterator hub rows = %v, want %v", titles, want)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("iterator hub rows = %v, want %v", titles, want)
		}
	}
	openIterTuples(t, m)
}

// Choosing Tuples before there is an iterator to hang them on says so rather
// than opening a list whose edits would be discarded.
func TestTheIteratorHubRefusesTuplesWithoutASource(t *testing.T) {
	m := atRuleHub(t)
	openSection(t, m, 3, screenIterator)
	selectIterRow(t, m, "tuples")
	send(m, key("enter"))

	if m.top() == screenTuples {
		t.Fatal("the tuple list opened with no iterator to hold the tuples")
	}
	if m.errMsg == "" {
		t.Fatal("no iterator, and no word about why the row did nothing")
	}
}

// TestTypingIntoIteratorSourceStaysOnTheForm guards against binding "edit
// tuples" to a bare "t": the iterator form is text entry, and a path like
// "input.data.object.accounts" contains the letter t several times. Every one
// of those keystrokes must land in the Source field.
func TestTypingIntoIteratorSourceStaysOnTheForm(t *testing.T) {
	m := atRuleHub(t)
	atIterForm(t, m)

	typeText(m, "input.data.object.accounts")
	if got := m.iterForm.Values()[0]; got != "input.data.object.accounts" {
		t.Fatalf("source = %q", got)
	}
	if m.top() != screenIterForm {
		t.Fatalf("top = %v, want the iterator form", m.top())
	}
}

func TestLeavingTheIteratorRebindsTheTupleEditor(t *testing.T) {
	m := atRuleHub(t)
	atIterForm(t, m)
	m.iterForm.SetValues([]string{"input.data.object.identities", "identity"})
	send(m, key("esc"))

	openIterTuples(t, m)
	send(m, key("esc"), key("esc")) // tuples -> iterator hub -> rule hub

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
	atIterForm(t, m)
	m.iterForm.SetValues([]string{"input.data.object.identities", "identity"})
	send(m, key("esc"), key("esc")) // form -> iterator hub -> rule hub
	if m.rule().Iterator == nil {
		t.Fatal("iterator should exist")
	}

	atIterForm(t, m)
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

// A new filter starts as whichever action the rule can actually support. The
// wizard used to hard-code delete, so patch — mapper's own default, and the
// shape that keeps a list in step — was reachable only by typing a word the
// screen never mentioned.
func TestANewFilterTakesTheActionTheRuleCanSupport(t *testing.T) {
	m := atRuleHub(t)
	m.rule().Tuples = []mapping.Tuple{{User: "user:a", Relation: "member", Object: "organization:o"}}
	openSection(t, m, 4, screenFilters)
	send(m, key("a"), key("esc"))

	if got := m.rule().Filters[0].Action; got != "" {
		t.Fatalf("action = %q on a rule that writes tuples, want a patch", got)
	}
}

func TestANewFilterOnARuleThatWritesNothingIsADelete(t *testing.T) {
	m := atRuleHub(t) // a fresh rule writes nothing yet
	openSection(t, m, 4, screenFilters)
	send(m, key("a"), key("esc"))

	if got := m.rule().Filters[0].Action; got != "delete" {
		t.Fatalf("action = %q on a rule with no tuples, want delete: a patch there "+
			"has an empty desired state and fails every event", got)
	}
}

// The list spells the default out. A blank cell reads like a field the user
// forgot rather than the action the filter has.
func TestTheFilterListNamesThePatchDefault(t *testing.T) {
	m := atRuleHub(t)
	m.rule().Tuples = []mapping.Tuple{{User: "user:a", Relation: "member", Object: "organization:o"}}
	openSection(t, m, 4, screenFilters)
	send(m, key("a"), key("esc"))

	if out := plain(m.viewString()); !strings.Contains(out, "patch") {
		t.Fatalf("the filter list does not say what an unset action does:\n%s", out)
	}
}

func TestTheFilterListSaysHowMuchOfTheCapIsSpent(t *testing.T) {
	m := atRuleHub(t)
	openSection(t, m, 4, screenFilters)
	// Nothing spent yet, and the empty state already has a sentence to say.
	if got := plain(m.viewString()); strings.Contains(got, "of 3 used") {
		t.Fatalf("an empty filter list should not carry a budget:\n%s", got)
	}

	send(m, key("a"), key("esc"))
	if got := plain(m.viewString()); !strings.Contains(got, "1 of 3 used") {
		t.Fatalf("want a filter count, got:\n%s", got)
	}

	send(m, key("a"), key("esc"))
	send(m, key("a"), key("esc"))
	if got := plain(m.viewString()); !strings.Contains(got, "3 of 3 used") {
		t.Fatalf("want a full filter count, got:\n%s", got)
	}
	// The count is there so the cap is visible before it refuses a fourth.
	send(m, key("a"))
	if m.errMsg == "" {
		t.Fatal("a fourth filter should be refused")
	}
}

func TestTheTupleBudgetCountsOnlyWhatActuallyRan(t *testing.T) {
	m := atRuleHub(t)
	m.width, m.height = 100, 30
	m.applySize()

	r := m.rule()
	r.Sample = &mapping.Sample{Label: "sample", Event: map[string]any{
		"type": "organization.member.added",
		"data": map[string]any{"object": map[string]any{
			"organization": map[string]any{"id": "org_1"},
			"user":         map[string]any{"user_id": "auth0|1"},
		}},
	}}
	r.Tuples = []mapping.Tuple{{User: "user:alice", Relation: "member", Object: "organization:acme"}}

	// A nameless rule does not compile, so the sample never runs. Reporting
	// "0 of 40" over the errors saying why would read as a measurement.
	r.Name = ""
	m.refresh()
	if got := plain(m.viewString()); strings.Contains(got, "of 40 tuples") {
		t.Fatalf("a document that never compiled has no budget to report:\n%s", got)
	}

	r.Name = "member_added"
	r.When = `input.type == "organization.member.added"`
	m.refresh()
	if got := plain(m.viewString()); !strings.Contains(got, "1 of 40 tuples") {
		t.Fatalf("want the sample's tuple count against the cap, got:\n%s", got)
	}
}
