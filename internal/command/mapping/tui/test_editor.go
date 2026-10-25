package tui

import (
	"errors"
	"reflect"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// testRow is what an editor row stands for: a part of the test, or the
// expected tuple or filter at index i.
type testRow struct {
	kind string // "name", "event", "tuple", "filter", "coverage", or "" for a note
	i    int
}

// testEditor edits one of the user's own tests in place: a row per part, each
// edit committed as it is made and the test re-run against the mapping.
type testEditor struct {
	s   *Session
	at  Focus
	idx int
	hub *picker.Picker
	// items and refs are the rows the picker draws and what each stands for.
	items []picker.Item
	refs  []testRow
	res   TestResults
}

func newTestEditor(s *Session, at Focus, i int) *testEditor {
	c := &testEditor{s: s, at: at, idx: i, hub: picker.New(nil)}
	c.sync()
	return c
}

// test is the test being edited, or nil when idx is out of range.
func (c *testEditor) test() *mapping.TestCase {
	if c.idx < 0 || c.idx >= len(c.s.Doc.Tests) {
		return nil
	}
	return &c.s.Doc.Tests[c.idx]
}

// sync rebuilds the rows and re-runs the tests, keeping the cursor.
func (c *testEditor) sync() {
	cursor := c.hub.Cursor()
	c.res = RunSavedTests(c.s.Ctx, c.s.Doc)
	c.items, c.refs = c.rows()
	c.hub = picker.New(c.items)
	c.hub.SetCursor(cursor)
}

func (c *testEditor) rows() ([]picker.Item, []testRow) {
	tc := c.test()
	if tc == nil {
		return nil, nil
	}
	var items []picker.Item
	var refs []testRow
	add := func(it picker.Item, ref testRow) {
		// Every value here can come from the user's own event payload.
		it.Title, it.Desc = style.SanitizeTerminal(it.Title), style.SanitizeTerminal(it.Desc)
		items, refs = append(items, it), append(refs, ref)
	}
	add(picker.Item{Title: "Name", Desc: tc.Name}, testRow{kind: "name"})
	add(picker.Item{Title: "Event", Desc: eventLabel(tc.Input)}, testRow{kind: "event"})
	for i, t := range tc.ExpectTuples {
		add(picker.Item{Title: mapping.TupleText(t, nil)}, testRow{kind: "tuple", i: i})
	}
	for i, f := range tc.ExpectTupleFilters {
		add(picker.Item{Title: expectedFilterText(f)}, testRow{kind: "filter", i: i})
	}
	if len(tc.ExpectTuples) == 0 && len(tc.ExpectTupleFilters) == 0 {
		// Whether filters go unchecked is the row below's to say, and only when
		// the event produces some: said here, it reads as though it always does.
		add(picker.Item{Title: "Expects nothing", Desc: "the event must produce no tuples"}, testRow{})
	}
	if n := c.uncheckedFilters(); n > 0 {
		add(picker.Item{Title: "Filters: not checked — the event produces " + mapping.Plural(n, "filter")}, testRow{})
	}
	// mapper checks coverage against the filters the event produces, not the
	// ones the test expects.
	coverage := picker.Item{Title: "Filter coverage: off", Desc: "a write may fall outside every filter the event produces"}
	if tc.AssertWritesCoveredByFilter {
		coverage = picker.Item{Title: "Filter coverage: on", Desc: "every write the event produces must fall under a filter it produces"}
	}
	add(coverage, testRow{kind: "coverage"})
	return items, refs
}

// uncheckedFilters counts the filters the test's event produces now when the
// test lists none. mapper compares filters only for a test that lists at least
// one, so a negative test passes on an event that deletes through a filter;
// the editor shows what goes unchecked instead of letting the verdict hide it.
func (c *testEditor) uncheckedFilters() int {
	tc := c.test()
	if tc == nil || len(tc.ExpectTupleFilters) > 0 {
		return 0
	}
	n := 0
	for _, op := range mapping.Evaluate(c.s.Ctx, c.s.Doc, tc.Input).Filters {
		n += len(op.Filters)
	}
	return n
}

// row is what the cursor's row stands for: a note when there is none.
func (c *testEditor) row() testRow {
	if i := c.hub.Cursor(); i < len(c.refs) {
		return c.refs[i]
	}
	return testRow{}
}

func (c *testEditor) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case closedMsg:
		if p, ok := msg.result.(Picked); ok {
			c.setInput(p.Event)
		}
		c.sync()
	case tea.KeyPressMsg:
		c.key(msg)
	}
	return c, nil
}

func (c *testEditor) key(k tea.KeyPressMsg) {
	tc := c.test()
	if tc == nil {
		if k.String() == "esc" {
			c.s.post(doneMsg{})
		}
		return
	}
	row := c.row()
	switch k.String() {
	case "up", "k":
		c.hub.Move(-1)
	case "down", "j":
		c.hub.Move(1)
	case "esc":
		c.s.post(doneMsg{})
	case "enter", " ":
		switch row.kind {
		case "name":
			c.s.post(openMsg{c: newTestNameForm(c.s, c.at, c.idx)})
		case "event":
			c.s.post(openMsg{c: newEventPick(c.s, c.at, false)})
		case "coverage":
			tc.AssertWritesCoveredByFilter = !tc.AssertWritesCoveredByFilter
			c.s.post(docChangedMsg{})
			c.sync()
		}
	case "a":
		c.s.post(openMsg{c: newExpectForm(c.s, c.at, c.idx)})
	case "d":
		switch row.kind {
		case "tuple":
			tc.ExpectTuples = slices.Delete(tc.ExpectTuples, row.i, row.i+1)
		case "filter":
			tc.ExpectTupleFilters = slices.Delete(tc.ExpectTupleFilters, row.i, row.i+1)
			// Removing the last filter loosens the test rather than failing it,
			// unlike removing any other row, so it is said.
			if len(tc.ExpectTupleFilters) == 0 {
				c.s.post(toastMsg{text: "filters are no longer checked for this test: mapper checks them only when a test lists at least one"})
			}
		default:
			return
		}
		c.s.post(docChangedMsg{})
		c.sync()
	case "r":
		next, err := mapping.NewTest(c.s.Ctx, c.s.Doc, tc.Name, tc.Input)
		if err != nil {
			// The expectations stay: they may be all that says what the event
			// should do, and a mapping halfway through an edit is no reason to
			// lose them.
			c.s.post(toastMsg{text: style.SanitizeTerminal("can't reset: " + err.Error()), err: true})
			return
		}
		c.setExpectations(next)
	case "n":
		// A test expecting nothing is a negative test: this event must not
		// produce anything.
		c.setExpectations(mapping.TestCase{})
	}
}

// setInput replaces the test's event and leaves its expectations alone: the
// verdict then says whether they still hold.
func (c *testEditor) setInput(event map[string]any) {
	tc := c.test()
	if tc == nil || reflect.DeepEqual(tc.Input, event) {
		return
	}
	tc.Input = event
	c.s.post(docChangedMsg{})
}

// setExpectations takes next's expectations, reporting a change only when
// they differ from the test's own.
func (c *testEditor) setExpectations(next mapping.TestCase) {
	tc := c.test()
	if tc == nil || sameExpectations(*tc, next) {
		return
	}
	tc.ExpectTuples, tc.ExpectTupleFilters = next.ExpectTuples, next.ExpectTupleFilters
	tc.AssertWritesCoveredByFilter = next.AssertWritesCoveredByFilter
	c.s.post(docChangedMsg{})
	c.sync()
}

// sameExpectations compares what two tests expect. A nil list and an empty
// one are the same expectation.
func sameExpectations(a, b mapping.TestCase) bool {
	return a.AssertWritesCoveredByFilter == b.AssertWritesCoveredByFilter &&
		slices.EqualFunc(a.ExpectTuples, b.ExpectTuples, func(x, y language.Tuple) bool { return x.Key() == y.Key() }) &&
		slices.Equal(a.ExpectTupleFilters, b.ExpectTupleFilters)
}

// View keeps the cursor's row on screen: a test can expect more tuples than
// the pane has lines, and the picker draws every row.
func (c *testEditor) View(width, height int) string {
	// The picker clamps a row's description, not its title; an expected tuple
	// is as long as the ids in it.
	return lipgloss.NewStyle().MaxWidth(width).Render(pickerWindow(c.hub, width, height))
}

// Keys offers ↵ and d only where they act: ↵ edits the name, the event and
// coverage, d removes an expected tuple or filter.
func (c *testEditor) Keys() []string {
	var keys []string
	switch c.row().kind {
	case "name", "event", "coverage":
		keys = append(keys, "↵ edit")
	case "tuple", "filter":
		keys = append(keys, "d remove")
	}
	return append(keys, "a add tuple", "r reset", "n expect nothing", "esc back", "? help")
}

func (c *testEditor) heading() (string, string) {
	tc := c.test()
	if tc == nil {
		return "Test", ""
	}
	return "Test", c.res.verdict(tc.Name)
}

func (c *testEditor) help() (string, string) {
	return "Test", "The rows under the event are the tuples it must produce, all of them and " +
		"nothing else. Its filters are checked the same way, but only when the test lists at " +
		"least one. a adds an expected tuple and d removes the row under the cursor. r resets the " +
		"expectations to what the mapping produces now; n expects nothing, which makes a " +
		"negative test: an event that must produce no tuples.\n\n" +
		"Filter coverage also fails the test when a write the event produces falls outside every " +
		"filter it produces, where no later patch can clean it up."
}

func (c *testEditor) focus() Focus { return c.at }

// testNameForm renames a test. It commits on enter, not as the user types: a
// name halfway through being typed can be another test's, and two tests of
// one name report as one.
type testNameForm struct {
	s    *Session
	at   Focus
	idx  int
	form *field.Form
}

func newTestNameForm(s *Session, at Focus, i int) *testNameForm {
	taken := map[string]bool{}
	// The user's own tests come first in what a save writes, so entry i is
	// this test.
	for j, tc := range mapping.SavedTests(s.Ctx, s.Doc) {
		if j != i {
			taken[tc.Name] = true
		}
	}
	validate := func(v string) error {
		v = strings.TrimSpace(v)
		switch {
		case v == "":
			return errors.New("a test needs a name")
		case taken[v]:
			return errors.New("another test already has this name")
		}
		return nil
	}
	c := &testNameForm{s: s, at: at, idx: i,
		form: field.NewForm(field.New("Test name", "deleted users map to nothing").WithValidate(validate))}
	c.form.Reset()
	if i >= 0 && i < len(s.Doc.Tests) {
		c.form.SetValues([]string{s.Doc.Tests[i].Name})
	}
	return c
}

func (c *testNameForm) Init() tea.Cmd { return c.form.Init() }

func (c *testNameForm) Update(msg tea.Msg) (component, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		c.s.post(doneMsg{})
		return c, nil
	}
	cmd := c.form.Update(msg)
	if c.form.Completed() {
		name := strings.TrimSpace(c.form.Values()[0])
		if c.idx < len(c.s.Doc.Tests) && c.s.Doc.Tests[c.idx].Name != name {
			c.s.Doc.Tests[c.idx].Name = name
			c.s.post(docChangedMsg{})
		}
		c.s.post(doneMsg{})
		return c, nil
	}
	return c, cmd
}

func (c *testNameForm) View(width, height int) string {
	c.form.SetWidth(width)
	return c.form.View()
}

func (c *testNameForm) Keys() []string { return []string{"↵ save", "esc cancel"} }

func (c *testNameForm) heading() (string, string) {
	return "Test name", "What `ofga mappings test` calls this test."
}

func (c *testNameForm) typing() bool { return true }

func (c *testNameForm) focus() Focus { return c.at }

// expectField indexes the expected-tuple form's fields, in the order Values
// returns them.
type expectField int

const (
	expectObject expectField = iota
	expectRelation
	expectUser
	expectAction
	expectCondition
	expectContext
)

// expectForm adds one expected tuple to a test. It adds on submit, not as the
// user types: a tuple halfway through being typed is not one the test expects.
type expectForm struct {
	s    *Session
	at   Focus
	idx  int
	form *field.Form
}

func newExpectForm(s *Session, at Focus, i int) *expectForm {
	c := &expectForm{s: s, at: at, idx: i, form: field.NewForm(
		field.New("Object", "organization:acme").WithValidate(vRequiredTuplePart(vLiteral(vObjectRef))),
		field.New("Relation", "member").WithValidate(vRequiredTuplePart(vLiteral(nil))),
		field.New("User", "user:alice").WithValidate(vRequiredTuplePart(vLiteral(vUserRef))),
		field.NewSelect("Action", tupleActions, 0),
		field.New("Condition (optional)", "in_region").WithValidate(vLiteral(vIdent)),
		field.New("Context (key=value, comma separated)", "region=eu").WithValidate(vExpectedContext),
	)}
	c.syncVisibility()
	return c
}

func (c *expectForm) Init() tea.Cmd { return c.form.Init() }

func (c *expectForm) Update(msg tea.Msg) (component, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		c.s.post(doneMsg{})
		return c, nil
	}
	cmd := c.form.Update(msg)
	c.syncVisibility()
	if c.form.Completed() {
		c.commit()
		c.s.post(doneMsg{})
		return c, nil
	}
	return c, cmd
}

// syncVisibility hides what cannot apply, as the rule's tuple form does: a
// delete carries no condition, and a context needs a condition.
func (c *expectForm) syncVisibility() {
	vals := c.form.Values()
	writes := strings.TrimSpace(vals[expectAction]) != "delete"
	c.form.SetVisible(int(expectCondition), writes)
	c.form.SetVisible(int(expectContext), writes && strings.TrimSpace(vals[expectCondition]) != "")
}

// commit appends the tuple. Its context holds the plain strings FGA stores,
// the way mapper renders a template's.
func (c *expectForm) commit() {
	if c.idx < 0 || c.idx >= len(c.s.Doc.Tests) {
		return
	}
	vals := c.form.Values()
	v := func(f expectField) string {
		if !c.form.Visible(int(f)) {
			return ""
		}
		return strings.TrimSpace(vals[f])
	}
	t := language.Tuple{
		Object:    v(expectObject),
		Relation:  v(expectRelation),
		User:      v(expectUser),
		Action:    language.TupleAction(keepDefault(v(expectAction), "", tupleActions[0])),
		Condition: v(expectCondition),
	}
	for _, e := range splitContext(v(expectContext)) {
		if t.Context == nil {
			t.Context = map[string]any{}
		}
		t.Context[e.Key] = e.Template
	}
	tc := &c.s.Doc.Tests[c.idx]
	tc.ExpectTuples = append(tc.ExpectTuples, t)
	c.s.post(docChangedMsg{})
}

func (c *expectForm) View(width, height int) string {
	c.form.SetWidth(width)
	c.form.SetHeight(height)
	return c.form.View()
}

func (c *expectForm) Keys() []string { return []string{"tab next", "↵ add", "esc cancel"} }

func (c *expectForm) heading() (string, string) {
	return "Expected tuple", "A tuple the event must produce, written out as FGA stores it."
}

func (c *expectForm) typing() bool { return true }

func (c *expectForm) focus() Focus { return c.at }
