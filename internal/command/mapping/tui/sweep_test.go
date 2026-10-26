package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// componentCase is one component in a state worth drawing, built on a fresh
// session over doc. Every component task adds its components here, so the
// sweeps below cover each one.
type componentCase struct {
	name string
	doc  func() *mapping.Document
	ix   *mapping.ModelIndex
	open func(s *Session) component
}

func componentCases(t *testing.T) []componentCase {
	cases := []componentCase{
		{name: "path picker", doc: func() *mapping.Document { return sampleDoc(memberAddedEvent()) }, open: openPicker},
		{name: "tuple list", doc: abcTuples, open: openTuples},
		{name: "empty tuple list", doc: func() *mapping.Document { return tuplesDoc() }, open: openTuples},
		{name: "tuple form", doc: func() *mapping.Document {
			return tuplesDoc(mapping.Tuple{Object: "organization:" + strings.Repeat("x", 300), Condition: "in_region"})
		}, ix: mapping.IndexModel(conditionModel()), open: openForm},
		{name: "variable list", doc: func() *mapping.Document {
			return varsDoc(mapping.Variable{Name: "org", Expr: "input.data.object." + strings.Repeat("x", 200)})
		}, open: openVars},
		{name: "empty variable list", doc: func() *mapping.Document { return varsDoc() }, open: openVars},
		{name: "variable form", doc: func() *mapping.Document { return varsDoc(mapping.Variable{Name: "org"}) },
			open: func(s *Session) component { return newVarForm(s, Focus{}, 0) }},
		{name: "filter list", doc: func() *mapping.Document {
			return filtersDoc(true, mapping.TupleFilter{Object: "organization:" + strings.Repeat("x", 200)})
		}, open: openFilters},
		{name: "empty filter list", doc: func() *mapping.Document { return filtersDoc(false) }, open: openFilters},
		{name: "filter form", doc: func() *mapping.Document { return filtersDoc(false, mapping.TupleFilter{Action: "delete"}) },
			open: func(s *Session) component { return newFilterForm(s, Focus{}, 0) }},
		{name: "iterator hub", doc: identitiesDoc, open: openIter},
		{name: "iterator hub without an iterator", doc: func() *mapping.Document { return sampleDoc(userCreatedEvent()) }, open: openIter},
		{name: "iterator hub with a long source", doc: func() *mapping.Document {
			d := identitiesDoc()
			d.Rules[0].Iterator.Source = "input.data.object." + strings.Repeat("x", 200)
			return d
		}, open: openIter},
		{name: "iterator form", doc: identitiesDoc,
			open: func(s *Session) component { return newIterForm(s, Focus{}, 0) }},
		{name: "trigger form", doc: func() *mapping.Document {
			d := freshRule()
			d.Rules[0].Name = strings.Repeat("n", 300)
			return d
		}, open: openTrigger},
		{name: "event pick", doc: freshRule, open: func(s *Session) component { return newEventPick(s, Focus{}, false) }},
		{name: "fork event pick", doc: func() *mapping.Document { return &mapping.Document{} }, open: forkPick},
		{name: "event paste", doc: freshRule, open: func(s *Session) component { return newEventPaste(s) }},
		{name: "event file", doc: freshRule, open: func(s *Session) component { return newEventFile(s) }},
		{name: "rule spoke", doc: func() *mapping.Document { return recipeDoc(t, "user.updated") }, open: openSpoke},
		{name: "action pick", doc: freshRule, open: func(s *Session) component { return newActionPick(s, Focus{}) }},
		{name: "rules hub", doc: func() *mapping.Document {
			d := threeRules()
			d.Rules[0].Name = strings.Repeat("r", 300)
			return d
		}, open: openHub},
		{name: "empty rules hub", doc: func() *mapping.Document { return &mapping.Document{} }, open: openHub},
		{name: "delete confirmation", doc: threeRules, open: func(s *Session) component { return newConfirmDelete(s, 0) }},
		{name: "test list", doc: func() *mapping.Document { return longTestDoc(t) }, open: openTests},
		{name: "empty test list", doc: freshRule, open: openTests},
		{name: "test editor", doc: func() *mapping.Document { return longTestDoc(t) }, open: openTestEditor},
		{name: "test name form", doc: func() *mapping.Document { return longTestDoc(t) }, open: func(s *Session) component { return newTestNameForm(s, Focus{}, 0) }},
		{name: "expected tuple form", doc: func() *mapping.Document { return longTestDoc(t) }, open: func(s *Session) component { return newExpectForm(s, Focus{}, 0) }},
		{name: "test delete confirmation", doc: func() *mapping.Document { return longTestDoc(t) }, open: func(s *Session) component { return newConfirmTestDelete(s, 0) }},
	}
	// A recipe's content is the catalog entry, so every entry is a case.
	for _, e := range auth0.Catalog() {
		cases = append(cases, componentCase{name: "recipe " + e.Type, doc: func() *mapping.Document { return &mapping.Document{} },
			open: func(s *Session) component { return newRecipeView(s, e) }})
	}
	return cases
}

// The widths a host gives a component: the narrowest content column
// (34, at a 44-column terminal), 80×24's, and wide.
var sweepSizes = []struct{ w, h int }{{34, 5}, {50, 12}, {72, 18}, {120, 40}}

func TestNoComponentDrawsWiderThanItIsGiven(t *testing.T) {
	for _, c := range componentCases(t) {
		for _, sz := range sweepSizes {
			s := NewSession(context.Background(), c.doc(), c.ix)
			for i, line := range strings.Split(c.open(s).View(sz.w, sz.h), "\n") {
				if w := lipgloss.Width(line); w > sz.w {
					t.Errorf("%s at %dx%d: line %d is %d cells wide", c.name, sz.w, sz.h, i, w)
				}
			}
		}
	}
}

// ? is advertised exactly where a component has help to show, so a user is
// never told about a key that does nothing.
func TestHelpIsAdvertisedExactlyWhereItExists(t *testing.T) {
	for _, c := range componentCases(t) {
		comp := c.open(NewSession(context.Background(), c.doc(), c.ix))
		_, hasHelp := comp.(helped)
		if advertised := slices.Contains(comp.Keys(), "? help"); hasHelp != advertised {
			t.Errorf("%s: help = %v but ? advertised = %v", c.name, hasHelp, advertised)
		}
	}
}

// focusedRow is the text of the row a component has the cursor on: the
// selected item's title for lists and pickers, the focused field's label for
// forms. Components with no such row (dialogs, a text area) return "".
func focusedRow(c component) string {
	title := func(l *uilist.List) string {
		it, _ := l.Selected()
		return it.TitleText
	}
	switch c := c.(type) {
	case *ruleSpoke:
		return c.hub.Selected().Title
	case *actionPick:
		return c.pick.Selected().Title
	case *iterHub:
		return c.hub.Selected().Title
	case *testEditor:
		return c.hub.Selected().Title
	case *tupleList:
		return title(c.list)
	case *varList:
		return title(c.list)
	case *filterList:
		return title(c.list)
	case *rulesHub:
		return title(c.list)
	case *testList:
		return title(c.list)
	case *eventPick:
		return title(c.list)
	case *pathPicker:
		return title(c.list)
	case *tupleForm:
		return c.form.FocusedLabel()
	case *varForm:
		return c.form.FocusedLabel()
	case *filterForm:
		return c.form.FocusedLabel()
	case *iterForm:
		return c.form.FocusedLabel()
	case *triggerForm:
		return c.form.FocusedLabel()
	case *eventFile:
		return c.form.FocusedLabel()
	case *testNameForm:
		return c.form.FocusedLabel()
	case *expectForm:
		return c.form.FocusedLabel()
	}
	return ""
}

// The host pane neither scrolls nor clips a component, so each must fit the
// height it is given and still show the row the user is on.
func TestEveryComponentHonoursItsHeight(t *testing.T) {
	for _, c := range componentCases(t) {
		for _, sz := range []struct{ w, h int }{{34, 5}, {50, 10}, {80, 21}} {
			comp := c.open(NewSession(context.Background(), c.doc(), c.ix))
			view := comp.View(sz.w, sz.h)
			if got := lipgloss.Height(view); got > sz.h {
				t.Errorf("%s at %dx%d: %d lines", c.name, sz.w, sz.h, got)
			}
			for i, line := range strings.Split(view, "\n") {
				if w := ansi.StringWidth(line); w > sz.w {
					t.Errorf("%s at %dx%d: line %d is %d cells wide", c.name, sz.w, sz.h, i, w)
				}
			}
			if row := focusedRow(comp); row != "" {
				// A narrow pane truncates a long row, so look for its start.
				if want := []rune(row); len(want) > 6 {
					row = string(want[:6])
				}
				if !strings.Contains(plain(view), row) {
					t.Errorf("%s at %dx%d: focused row %q missing:\n%s", c.name, sz.w, sz.h, row, plain(view))
				}
			}
		}
	}
}

// A paste reaches every text field, and on a rule's forms the document, as
// typing does: a field built but never focused, or a form that commits only
// what is typed, drops it.
func TestAPasteReachesEveryForm(t *testing.T) {
	rule := func(h *harness) *mapping.Rule { return &h.s.Doc.Rules[0] }
	for _, c := range []struct {
		name  string
		doc   *mapping.Document
		open  func(s *Session) component
		keys  []string // pressed before the paste
		value func(h *harness) string
	}{
		{"trigger", freshRule(), openTrigger, nil, func(h *harness) string { return rule(h).Name }},
		{"tuple", tuplesDoc(mapping.Tuple{}), func(s *Session) component { return newTupleForm(s, Focus{}, 0) }, nil,
			func(h *harness) string { return rule(h).Tuples[0].User }},
		{"variable", varsDoc(mapping.Variable{}), func(s *Session) component { return newVarForm(s, Focus{}, 0) }, nil,
			func(h *harness) string { return rule(h).Variables[0].Name }},
		{"iterator", sampleDoc(userCreatedEvent()), func(s *Session) component { return newIterForm(s, Focus{}, 0) }, nil,
			func(h *harness) string {
				if it := rule(h).Iterator; it != nil {
					return it.Source
				}
				return ""
			}},
		{"filter", filtersDoc(true, mapping.TupleFilter{}), func(s *Session) component { return newFilterForm(s, Focus{}, 0) }, nil,
			func(h *harness) string { return rule(h).Filters[0].User }},
		{"event file", &mapping.Document{}, func(s *Session) component { return newEventFile(s) }, nil,
			func(h *harness) string { return topAs[*eventFile](h.t, h).form.Values()[0] }},
		{"typed path", &mapping.Document{}, openBrowse(t.TempDir()), []string{"ctrl+p"},
			func(h *harness) string { return topAs[*fileBrowse](h.t, h).path.Values()[0] }},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.doc, nil, c.open)
			h.press(c.keys...)
			h.send(tea.PasteMsg{Content: "pasted"})
			if got := c.value(h); !strings.Contains(got, "pasted") {
				t.Fatalf("the paste did not arrive: value = %q", got)
			}
		})
	}
}
