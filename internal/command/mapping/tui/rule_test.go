package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func openSpoke(s *Session) component { return newRuleSpoke(s, Focus{}) }

// rowDesc is the summary under the spoke's row titled title.
func rowDesc(t *testing.T, c *ruleSpoke, title string) string {
	t.Helper()
	cursor := c.hub.Cursor()
	defer c.hub.SetCursor(cursor)
	for i := 0; i < c.hub.Len(); i++ {
		c.hub.SetCursor(i)
		if it := c.hub.Selected(); it.Title == title {
			return it.Desc
		}
	}
	t.Fatalf("no %q row on the rule hub", title)
	return ""
}

func TestTheSpokeListsSixSections(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openSpoke)
	want := []string{"Trigger", "Action", "Variables", "Iterator", "Tuple filters", "Tuples"}
	if got := pickerTitles(topAs[*ruleSpoke](t, h).hub); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

// The rows are built when the spoke opens, so the first key lands on a real
// row without a render in between.
func TestEnterOpensASectionWithoutRenderingFirst(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openSpoke)
	h.press("down", "enter")
	topAs[*actionPick](t, h)
}

func TestEachRowOpensItsSection(t *testing.T) {
	d := freshRule()
	d.Rules[0].Iterator = &mapping.Iterator{Source: "input.x", As: "x"}
	h := newHarness(t, d, nil, openSpoke)
	h.press("enter")
	topAs[*triggerForm](t, h)
	h.press("esc", "down", "down", "enter")
	topAs[*varList](t, h)
	h.press("esc", "down", "enter")
	topAs[*iterHub](t, h)
	h.press("esc", "down", "enter")
	topAs[*filterList](t, h)
	h.press("esc", "down", "enter")
	if l := topAs[*tupleList](t, h); l.at.InIter {
		t.Fatal("the Tuples row opened the iterator's tuples")
	}
}

func TestActionEditorSetsAndClearsTheRuleAction(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openSpoke)
	h.press("down", "enter")
	p := topAs[*actionPick](t, h)
	if !selectTitle(p.pick, "delete") {
		t.Fatalf("delete not offered: %v", pickerTitles(p.pick))
	}
	h.press("enter")
	if got := h.s.Doc.Rules[0].Action; got != "delete" {
		t.Fatalf("action = %q", got)
	}
	topAs[*ruleSpoke](t, h)

	h.press("enter")
	selectTitle(topAs[*actionPick](t, h).pick, "Per tuple")
	h.press("enter")
	if got := h.s.Doc.Rules[0].Action; got != "" {
		t.Fatalf("action = %q, want cleared", got)
	}
}

func TestTheActionPickStartsOnTheRulesAction(t *testing.T) {
	d := freshRule()
	d.Rules[0].Action = "write"
	h := newHarness(t, d, nil, func(s *Session) component { return newActionPick(s, Focus{}) })
	if got := topAs[*actionPick](t, h).pick.Selected().Value; got != "write" {
		t.Fatalf("selected = %q", got)
	}
}

// An action the picker does not offer, hand-written in a loaded file, opens on
// the first item. The search for it used to stop on the last item it tried, so
// enter turned a typo into "delete".
func TestAnUnknownActionOpensThePickOnTheFirstItem(t *testing.T) {
	d := freshRule()
	d.Rules[0].Action = "bogus"
	h := newHarness(t, d, nil, openSpoke)
	h.press("down", "enter")
	topAs[*actionPick](t, h)
	h.press("enter")
	if got := h.s.Doc.Rules[0].Action; got != "" {
		t.Fatalf("action = %q, want the first item's, per tuple", got)
	}
}

func TestRuleHubSummariesReflectEverySection(t *testing.T) {
	d := freshRule()
	r := &d.Rules[0]
	r.Action = "delete"
	r.Variables = []mapping.Variable{{Name: "org", Expr: "input.x"}}
	r.Iterator = &mapping.Iterator{Source: "input.data.object.identities", As: "identity"}
	r.Filters = []mapping.TupleFilter{{Object: "organization:1", Action: "delete"}}
	h := newHarness(t, d, nil, openSpoke)
	v := plain(h.view(72, 30))
	for _, want := range []string{"delete", "1 variable", "identity", "1 filter"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
}

// A child's edit shows on the spoke when the child closes.
func TestTheRowsFollowAChildsEdit(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openSpoke)
	h.press("enter")
	h.press("tab")
	h.typeText("true")
	h.press("esc")
	if got := rowDesc(t, topAs[*ruleSpoke](t, h), "Trigger"); got != "true" {
		t.Fatalf("trigger row = %q", got)
	}
}

func TestANewRulesMissingTuplesShowOnItsRow(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openSpoke)
	if got := rowDesc(t, topAs[*ruleSpoke](t, h), "Tuples"); !strings.Contains(got, "no tuples") {
		t.Fatalf("tuples row = %q, want the lint problem", got)
	}
}

// user.updated writes from its iterator alone. A bare "0 tuples" beside a
// preview showing a tuple reads as the recipe having failed.
func TestTheTuplesRowSaysWhenTheTuplesAreOnTheIterator(t *testing.T) {
	h := newHarness(t, recipeDoc(t, "user.updated"), nil, openSpoke)
	desc := rowDesc(t, topAs[*ruleSpoke](t, h), "Tuples")
	if strings.Contains(desc, "0 tuple") || !strings.Contains(desc, "iterator") {
		t.Fatalf("the row does not say where the tuples are: %q", desc)
	}
}

func TestTheTuplesRowCountsBothListsSeparately(t *testing.T) {
	h := newHarness(t, recipeDoc(t, "connection.created"), nil, openSpoke)
	if desc := rowDesc(t, topAs[*ruleSpoke](t, h), "Tuples"); !strings.Contains(desc, "1 tuple") || !strings.Contains(desc, "iterator") {
		t.Fatalf("the row does not account for both lists: %q", desc)
	}
}

func TestTheTuplesRowStaysPlainWithoutAnIterator(t *testing.T) {
	h := newHarness(t, recipeDoc(t, "organization.member.role.assigned"), nil, openSpoke)
	if desc := rowDesc(t, topAs[*ruleSpoke](t, h), "Tuples"); desc != "1 tuple" {
		t.Fatalf("desc = %q, want a plain count", desc)
	}
}

// A model warning is advisory: the row says so without the blocking mark.
func TestAModelWarningOnTheTuplesRowIsNotBlocking(t *testing.T) {
	d := freshRule()
	d.Rules[0] = mapping.Rule{
		Name: "organization.member.added", When: `input.type == "organization.member.added"`,
		Tuples: []mapping.Tuple{{User: "user:1", Relation: "owner", Object: "organization:1"}},
	}
	h := newHarness(t, d, mapping.IndexModel(testModel()), openSpoke)
	tuples := rowDesc(t, topAs[*ruleSpoke](t, h), "Tuples")
	if strings.HasPrefix(tuples, "✗") {
		t.Fatalf("the rule hub marks an advisory warning as blocking: %q", tuples)
	}
	if !strings.Contains(tuples, "owner") {
		t.Fatalf("the rule hub does not report the warning at all: %q", tuples)
	}
}

// mapper rejects these at compile time; the hub marks the row the user fixes
// them on, rather than leaving them to a raw diagnostic in the preview.
func TestTheHubMarksWhatMapperWouldReject(t *testing.T) {
	tup := mapping.Tuple{User: "user:{{ input.id }}", Relation: "member", Object: "group:g"}
	tests := []struct {
		name, row, want string
		rule            mapping.Rule
	}{
		{"a trigger reading a variable", "Trigger", "runs before its variables", mapping.Rule{
			When: "variables.uid != \"\"", Variables: []mapping.Variable{{Name: "uid", Expr: "input.id"}},
			Tuples: []mapping.Tuple{tup},
		}},
		{"a delete rule with a patch filter", "Action", "cannot delete them", mapping.Rule{
			Action: "delete", Filters: []mapping.TupleFilter{{Object: "group:g", Relation: "member"}},
			Tuples: []mapping.Tuple{tup},
		}},
		{"a filter naming one tuple", "Tuple filters", "names a single tuple", mapping.Rule{
			Filters: []mapping.TupleFilter{{User: "user:a", Relation: "member", Object: "group:g", Action: "delete"}},
		}},
		{"an iterator alias of input", "Iterator", "would hide the event's own input", mapping.Rule{
			Iterator: &mapping.Iterator{Source: "input.ids", As: "input", Tuples: []mapping.Tuple{tup}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.rule.Name = "r"
			h := newHarness(t, &mapping.Document{Rules: []mapping.Rule{tc.rule}}, nil, openSpoke)
			if got := rowDesc(t, topAs[*ruleSpoke](t, h), tc.row); !strings.Contains(got, tc.want) {
				t.Fatalf("%s row = %q, want it to say %q", tc.row, got, tc.want)
			}
		})
	}
}

func TestActionPickOnTheSameValuePostsNothing(t *testing.T) {
	d := freshRule()
	d.Rules[0].Action = "write"
	h := newHarness(t, d, nil, func(s *Session) component { return newActionPick(s, Focus{}) })
	h.press("enter")
	if h.changes != 0 || !h.closed {
		t.Fatalf("changes = %d, closed = %v, want the pick to close without a change", h.changes, h.closed)
	}

	h = newHarness(t, d, nil, func(s *Session) component { return newActionPick(s, Focus{}) })
	h.press("down", "enter")
	if h.changes != 1 || d.Rules[0].Action != "delete" {
		t.Fatalf("changes = %d, action = %q, want one change to delete", h.changes, d.Rules[0].Action)
	}
}
