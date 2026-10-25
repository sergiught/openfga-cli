package tui

import (
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// filtersDoc is one rule with the member-added sample and fs as its filters.
// writes gives the rule a tuple of its own, which decides a new filter's action.
func filtersDoc(writes bool, fs ...mapping.TupleFilter) *mapping.Document {
	d := sampleDoc(memberAddedEvent())
	if writes {
		d.Rules[0].Tuples = []mapping.Tuple{{User: "user:a", Relation: "member", Object: "organization:o"}}
	}
	d.Rules[0].Filters = fs
	return d
}

func openFilters(s *Session) component { return newFilterList(s, Focus{}) }

func TestFiltersAddAndCapAtThree(t *testing.T) {
	h := newHarness(t, filtersDoc(false), nil, openFilters)
	for range 3 {
		h.press("a")
		topAs[*filterForm](t, h).form.SetValues([]string{"", "member", "organization:{{ input.data.object.organization.id }}", "delete"})
		h.press("esc")
	}
	if got := len(h.s.Doc.Rules[0].Filters); got != 3 {
		t.Fatalf("filters = %d", got)
	}
	h.press("a")
	if got := len(h.s.Doc.Rules[0].Filters); got != 3 {
		t.Fatalf("the cap should hold at 3, got %d", got)
	}
	if toast := h.lastToast(); !toast.Err || toast.Text != "a rule takes at most 3 tuple filters" {
		t.Fatalf("toast = %+v", toast)
	}
	topAs[*filterList](t, h)
}

func TestFilterRoundTripsThroughTheDocument(t *testing.T) {
	h := newHarness(t, filtersDoc(false), nil, openFilters)
	h.press("a")
	topAs[*filterForm](t, h).form.SetValues([]string{"user:{{ fga_escape(input.data.object.user.user_id) }}", "", "", "delete"})
	h.press("esc")

	f := h.s.Doc.Rules[0].Filters[0]
	if f.User == "" || f.Action != "delete" {
		t.Fatalf("filter = %+v", f)
	}
	items := topAs[*filterList](t, h).list.Model.Items()
	if len(items) != 1 {
		t.Fatalf("filter list items = %d, want 1", len(items))
	}
	if desc := items[0].(uilist.Item).DescText; !strings.Contains(desc, "fga_escape") {
		t.Fatalf("the list does not show the filter: %s", desc)
	}
}

// A new filter starts as whichever action the rule can support: a patch on a
// rule that writes tuples, a delete on one that writes none.
func TestANewFilterTakesTheActionTheRuleCanSupport(t *testing.T) {
	h := newHarness(t, filtersDoc(true), nil, openFilters)
	h.press("a", "esc")
	if got := h.s.Doc.Rules[0].Filters[0].Action; got != "" {
		t.Fatalf("action = %q on a rule that writes tuples, want a patch", got)
	}
}

func TestANewFilterOnARuleThatWritesNothingIsADelete(t *testing.T) {
	h := newHarness(t, filtersDoc(false), nil, openFilters)
	h.press("a", "esc")
	if got := h.s.Doc.Rules[0].Filters[0].Action; got != "delete" {
		t.Fatalf("action = %q on a rule with no tuples, want delete: a patch there "+
			"has an empty desired state and fails every event", got)
	}
}

// The list spells the default out. A blank cell reads like a field the user
// forgot rather than the action the filter has.
func TestTheFilterListNamesThePatchDefault(t *testing.T) {
	h := newHarness(t, filtersDoc(true), nil, openFilters)
	h.press("a", "esc")
	if out := plain(h.view(72, 12)); !strings.Contains(out, "patch") {
		t.Fatalf("the filter list does not say what an unset action does:\n%s", out)
	}
}

func TestTheFilterListSaysHowMuchOfTheCapIsSpent(t *testing.T) {
	h := newHarness(t, filtersDoc(false), nil, openFilters)
	sub := func() string { _, s := topAs[*filterList](t, h).heading(); return s }
	// Nothing spent yet, and the empty state already has a sentence to say.
	if strings.Contains(sub(), "of 3 used") {
		t.Fatalf("an empty filter list carries a budget: %q", sub())
	}
	h.press("a", "esc")
	if !strings.Contains(sub(), "1 of 3 used") {
		t.Fatalf("subtitle = %q, want a filter count", sub())
	}
	h.press("a", "esc", "a", "esc")
	if !strings.Contains(sub(), "3 of 3 used") {
		t.Fatalf("subtitle = %q, want a full count", sub())
	}
}

// A2: the selection stayed on the first row while the filter showed another.
func TestAFilteredFilterDeleteRemovesTheRowShown(t *testing.T) {
	h := newHarness(t, filtersDoc(false,
		mapping.TupleFilter{Object: "organization:a", Action: "delete"},
		mapping.TupleFilter{Object: "team:b", Action: "delete"},
		mapping.TupleFilter{Object: "connection:c", Action: "delete"},
	), nil, openFilters)
	h.press("/")
	h.typeText("connection")
	h.press("enter", "d")
	var objects []string
	for _, f := range h.s.Doc.Rules[0].Filters {
		objects = append(objects, f.Object)
	}
	if got := strings.Join(objects, ","); got != "organization:a,team:b" {
		t.Fatalf("filters = %s, want connection:c deleted", got)
	}
}

// User, relation and object are templates, so a picked path is wrapped; the
// insert commits.
func TestAPathInAFilterFieldIsATemplate(t *testing.T) {
	h := newHarness(t, filtersDoc(false, mapping.TupleFilter{Action: "delete"}), nil,
		func(s *Session) component { return newFilterForm(s, Focus{}, 0) })
	h.typeText("user:")
	h.press("ctrl+p", "/")
	h.typeText("user_id")
	h.press("enter", "enter")
	if got := h.s.Doc.Rules[0].Filters[0].User; got != "user:{{ input.data.object.user.user_id }}" {
		t.Fatalf("user = %q", got)
	}
}

func TestAFilterObjectIsValidatedInline(t *testing.T) {
	h := newHarness(t, filtersDoc(false, mapping.TupleFilter{Action: "delete"}), nil,
		func(s *Session) component { return newFilterForm(s, Focus{}, 0) })
	h.press("tab", "tab")
	h.typeText("organization")
	h.press("tab")
	if !strings.Contains(plain(h.view(72, 20)), "needs a type prefix like organization: before the id") {
		t.Fatalf("no inline error for an object without a type:\n%s", plain(h.view(72, 20)))
	}
}

func TestTabbingThroughAFilterChangesNothing(t *testing.T) {
	assertOnlyEditsChangeTheDocument(t, func() *mapping.Document {
		return filtersDoc(false, mapping.TupleFilter{User: "user:1", Object: "organization:1", Action: "delete"})
	}, func(s *Session) component { return newFilterForm(s, Focus{}, 0) })
}

func TestTheFilterActionIsPicked(t *testing.T) {
	h := newHarness(t, filtersDoc(true, mapping.TupleFilter{Object: "organization:"}), nil,
		func(s *Session) component { return newFilterForm(s, Focus{}, 0) })
	h.press("tab", "tab", "tab")
	h.press("right")
	if got := h.s.Doc.Rules[0].Filters[0].Action; got != "delete" {
		t.Fatalf("action = %q after ←→, want delete", got)
	}
	h.press("right")
	if got := h.s.Doc.Rules[0].Filters[0].Action; got != "" {
		t.Fatalf("back on patch, action = %q, want it unset again", got)
	}
}

// B9: two filters that differ only in object must not read the same.
func TestTwoFiltersAreToldApartByTheirObject(t *testing.T) {
	user := "user:{{ fga_escape(input.data.object.user.user_id) }}"
	h := newHarness(t, filtersDoc(true,
		mapping.TupleFilter{User: user, Relation: "member", Object: "organization:alpha"},
		mapping.TupleFilter{User: user, Relation: "member", Object: "organization:bravo"},
	), nil, openFilters)
	items := topAs[*filterList](t, h).list.Model.Items()
	a, b := items[0].(uilist.Item), items[1].(uilist.Item)
	if a.TitleText != "organization:alpha" || b.TitleText != "organization:bravo" {
		t.Fatalf("titles = %q, %q; want the objects", a.TitleText, b.TitleText)
	}
	if want := user + " member organization:alpha"; !strings.Contains(a.DescText, want) {
		t.Fatalf("description %q lacks the full user relation object line", a.DescText)
	}
	out := plain(h.view(60, 12))
	if !strings.Contains(out, "organization:alpha") || !strings.Contains(out, "organization:bravo") {
		t.Fatalf("at 60 columns the rows do not show their objects:\n%s", out)
	}
}
