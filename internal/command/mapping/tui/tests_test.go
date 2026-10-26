package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// testsDoc is one member-added rule whose sample generates a test.
func testsDoc() *mapping.Document {
	d := tuplesDoc(mapping.Tuple{
		User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
		Relation: "member",
		Object:   "organization:{{ input.data.object.organization.id }}",
	})
	d.Rules[0].Name = "member-added"
	d.Rules[0].When = `input.type == "organization.member.added"`
	return d
}

// orgEvent is the member-added event for another organization: the same rule
// fires, on an input no sample shares.
func orgEvent(org string) map[string]any {
	ev := memberAddedEvent()
	ev["data"] = map[string]any{"object": map[string]any{
		"organization": map[string]any{"id": org},
		"user":         map[string]any{"user_id": "auth0|1"},
	}}
	return ev
}

// ownTest is what adding a test for event produces: expectations taken from
// the mapping now.
func ownTest(t *testing.T, d *mapping.Document, name string, event map[string]any) mapping.TestCase {
	t.Helper()
	tc, err := mapping.NewTest(t.Context(), d, name, event)
	if err != nil {
		t.Fatal(err)
	}
	return tc
}

func openTests(s *Session) component { return newTestList(s, Focus{}) }

func openTestEditor(s *Session) component { return newTestEditor(s, Focus{}, 0) }

// selectTest highlights the list row titled name, failing when there is none.
func selectTest(t *testing.T, h *harness, name string) {
	t.Helper()
	c := topAs[*testList](t, h)
	for i, it := range c.list.Model.Items() {
		// The row ends with how long the test took: "✓ name (12ms)".
		if title := it.(uilist.Item).TitleText; strings.HasSuffix(title, " "+name) || strings.Contains(title, " "+name+" (") {
			c.list.SelectIndex(i)
			return
		}
	}
	t.Fatalf("no test %q in:\n%s", name, plain(h.view(100, 30)))
}

// selectEditorRow highlights the editor row whose title starts with prefix.
func selectEditorRow(t *testing.T, h *harness, prefix string) {
	t.Helper()
	c := topAs[*testEditor](t, h)
	for i := 0; i < c.hub.Len(); i++ {
		c.hub.SetCursor(i)
		if strings.HasPrefix(c.hub.Selected().Title, prefix) {
			return
		}
	}
	t.Fatalf("no row %q in:\n%s", prefix, plain(h.view(100, 30)))
}

func subtitle(h *harness) string {
	_, sub := h.st.Heading()
	return sub
}

// verdictLine is the first line of the subtitle: the first reason a test
// failed, before the rest the verdict lists under it.
func verdictLine(h *harness) string {
	line, _, _ := strings.Cut(subtitle(h), "\n")
	return line
}

func TestTheListShowsGeneratedAndOwnTestsWithTheirVerdicts(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{
		ownTest(t, d, "other org", orgEvent("org_b")),
		{Name: "expects nothing", Input: orgEvent("org_c")},
	}
	h := newHarness(t, d, nil, openTests)
	v := plain(h.view(120, 30))
	for _, want := range []string{
		"✓ other org", "1 tuple",
		"✗ expects nothing", "expects nothing · unexpected: user:auth0|1 member organization:org_c",
		"✓ member-added", "from member-added's sample · follows the mapping",
	} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
	if sub := subtitle(h); !strings.Contains(sub, "2 of 3 pass") {
		t.Fatalf("subtitle = %q", sub)
	}
}

func TestTestsOfAMappingThatDoesNotCompileAreNotRun(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{ownTest(t, d, "other org", orgEvent("org_b"))}
	d.Rules[0].Tuples[0].User = "user:{{ input. }}"
	h := newHarness(t, d, nil, openTests)
	if v := plain(h.view(120, 30)); !strings.Contains(v, "– other org") {
		t.Fatalf("the test is not marked as not run:\n%s", v)
	}
	// The generated test is gone from the list, since a sample makes one only
	// once it evaluates; the subtitle says it comes back.
	if sub := subtitle(h); !strings.Contains(sub, "must compile") || !strings.Contains(sub, "Generated tests return once it compiles.") {
		t.Fatalf("subtitle = %q, want it to say the mapping must compile first, and the generated tests return", sub)
	}
	// Without a sample there is no generated test to promise.
	d.Rules[0].Sample = nil
	h = newHarness(t, d, nil, openTests)
	if sub := subtitle(h); strings.Contains(sub, "Generated") {
		t.Fatalf("subtitle = %q, with no sample to generate a test", sub)
	}
}

func TestAddingATestFromTheCatalogCreatesAPassingOwnTest(t *testing.T) {
	d := testsDoc()
	d.Rules[0].Sample = nil // nothing generated, so the new test stands alone
	h := newHarness(t, d, nil, openTests)
	h.press("a")
	if !topAs[*eventPick](t, h).list.SelectID("organization.member.added") {
		t.Fatal("could not select the event")
	}
	h.press("enter")
	topAs[*testEditor](t, h)
	if len(h.s.Doc.Tests) != 1 || h.s.Doc.Tests[0].Name != "organization.member.added" {
		t.Fatalf("tests = %+v", h.s.Doc.Tests)
	}
	if len(h.s.Doc.Tests[0].ExpectTuples) != 1 || h.changes == 0 {
		t.Fatalf("test = %+v, changes = %d", h.s.Doc.Tests[0], h.changes)
	}
	if sub := subtitle(h); sub != "Passes." {
		t.Fatalf("subtitle = %q", sub)
	}
	h.press("esc")
	if v := plain(h.view(120, 30)); !strings.Contains(v, "✓ organization.member.added") {
		t.Fatalf("the new test is not listed as passing:\n%s", v)
	}
}

// A test whose event the mapping fails on is still created, expecting
// nothing, and the toast says why.
func TestAddingATestTheMappingFailsOnStillCreatesIt(t *testing.T) {
	d := testsDoc()
	h := newHarness(t, d, nil, openTests)
	h.press("a")
	topAs[*eventPick](t, h).list.SelectID(pasteID)
	h.press("enter")
	h.send(tea.PasteMsg{Content: `{"type": "organization.member.added"}`})
	h.press("ctrl+d")
	topAs[*testEditor](t, h)
	if len(h.s.Doc.Tests) != 1 || len(h.s.Doc.Tests[0].ExpectTuples) != 0 {
		t.Fatalf("tests = %+v", h.s.Doc.Tests)
	}
	if toast := h.lastToast(); !toast.Err || !strings.Contains(toast.Text, "expects nothing") {
		t.Fatalf("toast = %+v", toast)
	}
}

// A new test never takes a name a save already writes.
func TestANewTestsNameIsUnique(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{{Name: "organization.member.added", Input: orgEvent("org_b")}}
	h := newHarness(t, d, nil, openTests)
	h.press("a")
	topAs[*eventPick](t, h).list.SelectID("organization.member.added")
	h.press("enter")
	if got := h.s.Doc.Tests[1].Name; got != "organization.member.added 2" {
		t.Fatalf("name = %q", got)
	}
}

func TestOpeningAGeneratedTestMakesItYourOwn(t *testing.T) {
	h := newHarness(t, testsDoc(), nil, openTests)
	selectTest(t, h, "member-added")
	h.press("enter")
	topAs[*testEditor](t, h)
	if len(h.s.Doc.Tests) != 1 || h.s.Doc.Tests[0].Name != "member-added" || len(h.s.Doc.Tests[0].ExpectTuples) != 1 {
		t.Fatalf("tests = %+v", h.s.Doc.Tests)
	}
	if toast := h.lastToast(); !strings.Contains(toast.Text, "now your own test") {
		t.Fatalf("toast = %+v", toast)
	}
	h.press("esc")
	c := topAs[*testList](t, h)
	if n := len(c.list.Model.Items()); n != 1 {
		t.Fatalf("rows = %d, want the own test alone", n)
	}
	if v := plain(h.view(120, 30)); strings.Contains(v, "follows the mapping") {
		t.Fatalf("the generated test is still listed:\n%s", v)
	}
}

func TestDeletingAnOwnTestAsksFirst(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{ownTest(t, d, "other org", orgEvent("org_b"))}
	h := newHarness(t, d, nil, openTests)
	selectTest(t, h, "other org")
	h.press("d")
	topAs[*confirmTestDelete](t, h)
	h.press("enter", "n")
	if len(h.s.Doc.Tests) != 1 || h.changes != 0 {
		t.Fatalf("cancelling changed the tests: %+v", h.s.Doc.Tests)
	}
	selectTest(t, h, "other org")
	h.press("d", "y")
	if len(h.s.Doc.Tests) != 0 || h.changes == 0 {
		t.Fatalf("tests = %+v, changes = %d", h.s.Doc.Tests, h.changes)
	}
	topAs[*testList](t, h)
}

func TestDeletingAGeneratedTestExplainsWhereItComesFrom(t *testing.T) {
	h := newHarness(t, testsDoc(), nil, openTests)
	selectTest(t, h, "member-added")
	h.press("d")
	topAs[*testList](t, h)
	if toast := h.lastToast(); !strings.Contains(toast.Text, "member-added's sample") {
		t.Fatalf("toast = %+v", toast)
	}
	if h.changes != 0 {
		t.Fatal("explaining a generated test changed the document")
	}
}

// editorDoc is testsDoc with the sample's test made the user's own.
func editorDoc(t *testing.T) *mapping.Document {
	d := testsDoc()
	d.Tests = []mapping.TestCase{ownTest(t, d, "member added", memberAddedEvent())}
	return d
}

func TestTheEditorShowsTheTestsParts(t *testing.T) {
	h := newHarness(t, editorDoc(t), nil, openTestEditor)
	v := plain(h.view(120, 30))
	for _, want := range []string{"member added", "organization.member.added",
		"user:auth0|507f1f77bcf86cd799439020 member organization:org_1234567890abcdef", "Filter coverage: off"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
}

// Removing the only expected tuple leaves the mapping producing a tuple the
// test no longer expects.
func TestRemovingAnExpectedTupleFailsTheTest(t *testing.T) {
	h := newHarness(t, editorDoc(t), nil, openTestEditor)
	selectEditorRow(t, h, "user:")
	h.press("d")
	if n := len(h.s.Doc.Tests[0].ExpectTuples); n != 0 || h.changes != 1 {
		t.Fatalf("tuples = %d, changes = %d", n, h.changes)
	}
	if sub := subtitle(h); !strings.HasPrefix(sub, "Fails: unexpected: user:auth0|507f") {
		t.Fatalf("subtitle = %q", sub)
	}
}

func TestAddingAnExpectedTuple(t *testing.T) {
	h := newHarness(t, editorDoc(t), nil, openTestEditor)
	h.press("a")
	topAs[*expectForm](t, h)
	h.typeText("organization:acme")
	h.press("tab")
	h.typeText("member")
	h.press("tab")
	h.typeText("user:bob")
	h.press("tab", "space") // action: delete
	h.press("enter")
	topAs[*testEditor](t, h)
	got := h.s.Doc.Tests[0].ExpectTuples
	want := language.Tuple{User: "user:bob", Relation: "member", Object: "organization:acme", Action: language.ActionDelete}
	if len(got) != 2 || !reflect.DeepEqual(got[1], want) {
		t.Fatalf("tuples = %+v", got)
	}
	if sub := subtitle(h); !strings.HasPrefix(sub, "Fails: missing: user:bob member organization:acme") {
		t.Fatalf("subtitle = %q", sub)
	}
	if v := plain(h.view(120, 30)); !strings.Contains(v, "user:bob member organization:acme (delete)") {
		t.Fatalf("the new tuple is not listed:\n%s", v)
	}
}

// A condition and its context are written out as the plain values FGA stores.
func TestAnExpectedTupleCarriesItsConditionAndContext(t *testing.T) {
	h := newHarness(t, editorDoc(t), nil, openTestEditor)
	h.press("a")
	h.typeText("organization:acme")
	h.press("tab")
	h.typeText("member")
	h.press("tab")
	h.typeText("user:bob")
	h.press("tab", "tab")
	h.typeText("in_region")
	h.press("tab")
	h.typeText("region=eu, tier=gold")
	h.press("enter")
	got := h.s.Doc.Tests[0].ExpectTuples
	if len(got) != 2 || got[1].Condition != "in_region" ||
		!reflect.DeepEqual(got[1].Context, map[string]any{"region": "eu", "tier": "gold"}) || got[1].Action != "" {
		t.Fatalf("tuples = %+v", got)
	}
	if v := plain(h.view(120, 30)); !strings.Contains(v, `user:bob member organization:acme (in_region region="eu", tier="gold")`) {
		t.Fatalf("the condition and its context are not shown:\n%s", v)
	}
}

// A tuple whose context differs is another tuple to mapper, so the reasons a
// test fails show the context: the missing tuple and the unexpected one read
// apart, and the way `ofga mappings test` prints them.
func TestAContextMismatchReadsAsTwoTuples(t *testing.T) {
	d := testsDoc()
	d.Rules[0].Tuples[0].Condition = "in_region"
	d.Rules[0].Tuples[0].Context = []mapping.ContextEntry{{Key: "region", Template: "us"}}
	d.Tests = []mapping.TestCase{ownTest(t, d, "own", orgEvent("org_q"))}
	d.Tests[0].ExpectTuples[0].Context = map[string]any{"region": "eu"}
	h := newHarness(t, d, nil, openTestEditor)
	if sub := verdictLine(h); sub != `Fails: missing: user:auth0|1 member organization:org_q (in_region region="eu")` {
		t.Fatalf("subtitle = %q", sub)
	}
	selectEditorRow(t, h, "user:")
	h.press("d")
	if sub := verdictLine(h); sub != `Fails: unexpected: user:auth0|1 member organization:org_q (in_region region="us")` {
		t.Fatalf("subtitle = %q", sub)
	}
}

// mapper refuses a whole file whose expected tuple misses a part, so the form
// will not add one.
func TestAnExpectedTupleNeedsAllThreeParts(t *testing.T) {
	h := newHarness(t, editorDoc(t), nil, openTestEditor)
	h.press("a")
	h.typeText("organization:acme")
	h.press("enter", "enter", "enter", "enter", "enter", "enter")
	topAs[*expectForm](t, h)
	if n := len(h.s.Doc.Tests[0].ExpectTuples); n != 1 || h.changes != 0 {
		t.Fatalf("tuples = %d, changes = %d", n, h.changes)
	}
}

func TestExpectingNothingMakesANegativeTest(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{ownTest(t, d, "sync", memberAddedEvent())}
	d.Tests[0].ExpectTupleFilters = []language.TupleFilter{{Object: "organization:x", Action: language.FilterActionPatch}}
	d.Tests[0].AssertWritesCoveredByFilter = true
	h := newHarness(t, d, nil, openTestEditor)
	h.press("n")
	tc := h.s.Doc.Tests[0]
	if len(tc.ExpectTuples) != 0 || len(tc.ExpectTupleFilters) != 0 || tc.AssertWritesCoveredByFilter {
		t.Fatalf("test = %+v, want it expecting nothing", tc)
	}
	if sub := subtitle(h); !strings.HasPrefix(sub, "Fails: unexpected") {
		t.Fatalf("while the mapping produces tuples, subtitle = %q", sub)
	}
	// An event no rule matches is what a negative test is for.
	selectEditorRow(t, h, "Event")
	h.press("enter")
	topAs[*eventPick](t, h).list.SelectID(pasteID)
	h.press("enter")
	h.send(tea.PasteMsg{Content: `{"type": "user.deleted"}`})
	h.press("ctrl+d")
	topAs[*testEditor](t, h)
	if got := h.s.Doc.Tests[0].Input["type"]; got != "user.deleted" {
		t.Fatalf("input type = %v", got)
	}
	if sub := subtitle(h); sub != "Passes." {
		t.Fatalf("on an event no rule matches, subtitle = %q", sub)
	}
	// Expecting nothing of a test that already expects nothing is no edit.
	before := h.changes
	h.press("n")
	if h.changes != before {
		t.Fatal("n on a negative test reported a change")
	}
}

func TestResetExpectsWhatTheMappingProducesNow(t *testing.T) {
	h := newHarness(t, editorDoc(t), nil, openTestEditor)
	h.press("n")
	h.press("r")
	tc := h.s.Doc.Tests[0]
	if tc.Name != "member added" || len(tc.ExpectTuples) != 1 {
		t.Fatalf("test = %+v", tc)
	}
	if sub := subtitle(h); sub != "Passes." {
		t.Fatalf("subtitle = %q", sub)
	}
	before := h.changes
	h.press("r")
	if h.changes != before {
		t.Fatal("resetting a test that already matches reported a change")
	}
}

// I1: a reset that cannot work out what the mapping produces keeps what the
// user wrote, which may be all that says what the event should do.
func TestAResetThatFailsLeavesTheTestAlone(t *testing.T) {
	d := editorDoc(t)
	d.Tests[0].ExpectTuples = append(d.Tests[0].ExpectTuples,
		language.Tuple{User: "user:bob", Relation: "member", Object: "organization:hand-written"})
	d.Rules[0].Tuples[0].User = "user:{{ input. }}" // no longer compiles
	want := slices.Clone(d.Tests[0].ExpectTuples)
	h := newHarness(t, d, nil, openTestEditor)
	h.press("r")
	if got := h.s.Doc.Tests[0].ExpectTuples; !reflect.DeepEqual(got, want) || h.changes != 0 {
		t.Fatalf("tuples = %+v, changes = %d; want the test as it was", got, h.changes)
	}
	if toast := h.lastToast(); !toast.Err || !strings.HasPrefix(toast.Text, "can't reset: the mapping does not compile") {
		t.Fatalf("toast = %+v", toast)
	}
}

// I2: mapper compares filters only when a test lists one, and checks coverage
// against the filters the event produces; the screens say so.
func TestTheEditorSaysWhatMapperChecks(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{{Name: "quiet", Input: orgEvent("org_c"), AssertWritesCoveredByFilter: true}}
	h := newHarness(t, d, nil, openTestEditor)
	v := plain(h.view(160, 30))
	for _, want := range []string{
		"the event must produce no tuples",
		"every write the event produces must fall under a filter it produces",
	} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
	if strings.Contains(v, "no filters") || strings.Contains(v, "expected filter") {
		t.Fatalf("the editor promises a filter check mapper does not make:\n%s", v)
	}
	// This event produces no filters, so nothing goes unchecked to warn about.
	if strings.Contains(v, "not checked") {
		t.Fatalf("the editor warns of unchecked filters the event does not produce:\n%s", v)
	}
	selectEditorRow(t, h, "Filter coverage")
	h.press("enter")
	if v := plain(h.view(160, 30)); !strings.Contains(v, "a write may fall outside every filter the event produces") {
		t.Fatalf("coverage off reads:\n%s", v)
	}
	_, editor := topAs[*testEditor](t, h).help()
	_, list := newTestList(h.s, Focus{}).help()
	for _, body := range []string{editor, list} {
		if !strings.Contains(body, "only when the test lists at least one") || strings.Contains(body, "tuples and filters the mapping must") {
			t.Fatalf("help = %q", body)
		}
	}
	if !strings.Contains(editor, "a write the event produces falls outside every filter it produces") {
		t.Fatalf("editor help = %q", editor)
	}
}

// deleteFilterDoc's rule only deletes, through a filter on the event's
// organization: no tuples, one filter.
func deleteFilterDoc() *mapping.Document {
	d := sampleDoc(memberAddedEvent())
	d.Rules[0].When = `input.type == "organization.member.added"`
	d.Rules[0].Filters = []mapping.TupleFilter{{Object: "organization:{{ input.data.object.organization.id }}", Action: "delete"}}
	return d
}

// A negative test passes on an event that deletes through a filter, since
// mapper checks no filter a test does not list; the editor shows what goes
// unchecked.
func TestTheEditorShowsTheFiltersANegativeTestLeavesUnchecked(t *testing.T) {
	d := deleteFilterDoc()
	d.Tests = []mapping.TestCase{{Name: "must do nothing", Input: orgEvent("org_z")}}
	h := newHarness(t, d, nil, openTestEditor)
	if v := plain(h.view(120, 30)); !strings.Contains(v, "Filters: not checked — the event produces 1 filter") {
		t.Fatalf("the unchecked filter is not shown:\n%s", v)
	}
	// The row is a note: d and enter on it do nothing.
	selectEditorRow(t, h, "Filters: not checked")
	h.press("d", "enter")
	if h.changes != 0 {
		t.Fatalf("the note row edited the test: changes = %d", h.changes)
	}

	// An event that produces no filter has nothing unchecked to show.
	h = newHarness(t, editorDoc(t), nil, openTestEditor)
	h.press("n")
	if v := plain(h.view(120, 30)); strings.Contains(v, "not checked —") {
		t.Fatalf("a note for filters the event does not produce:\n%s", v)
	}
}

// twoFilterDoc's rule patches its tuple under two filters, and its own test
// expects both.
func twoFilterDoc(t *testing.T) *mapping.Document {
	d := testsDoc()
	org := "organization:{{ input.data.object.organization.id }}"
	d.Rules[0].Filters = []mapping.TupleFilter{{Object: org, Relation: "member"}, {Object: org, Relation: "admin"}}
	d.Tests = []mapping.TestCase{ownTest(t, d, "own", orgEvent("org_k"))}
	if len(d.Tests[0].ExpectTupleFilters) != 2 {
		t.Fatalf("own test = %+v, want two expected filters", d.Tests[0])
	}
	return d
}

// Removing a filter row is an edit like removing a tuple row, until it is the
// last: then mapper stops checking filters, and the toast says so.
func TestRemovingExpectedFilters(t *testing.T) {
	h := newHarness(t, twoFilterDoc(t), nil, openTestEditor)
	selectEditorRow(t, h, "relation=member")
	h.press("d")
	if fs := h.s.Doc.Tests[0].ExpectTupleFilters; len(fs) != 1 || fs[0].Relation != "admin" || h.changes != 1 {
		t.Fatalf("filters = %+v, changes = %d", fs, h.changes)
	}
	if sub := subtitle(h); !strings.HasPrefix(sub, "Fails: unexpected filter: relation=member") {
		t.Fatalf("subtitle = %q", sub)
	}
	if len(h.toasts) != 0 {
		t.Fatalf("toasts = %+v, want none while a filter is still listed", h.toasts)
	}
	selectEditorRow(t, h, "relation=admin")
	h.press("d")
	if fs := h.s.Doc.Tests[0].ExpectTupleFilters; len(fs) != 0 || h.changes != 2 {
		t.Fatalf("filters = %+v, changes = %d", fs, h.changes)
	}
	if toast := h.lastToast(); toast.Err || !strings.Contains(toast.Text, "filters are no longer checked for this test") {
		t.Fatalf("toast = %+v", toast)
	}
	if v := plain(h.view(120, 30)); !strings.Contains(v, "Filters: not checked — the event produces 2 filters") {
		t.Fatalf("the unchecked filters are not shown:\n%s", v)
	}
}

func TestFilterCoverageToggles(t *testing.T) {
	h := newHarness(t, editorDoc(t), nil, openTestEditor)
	selectEditorRow(t, h, "Filter coverage")
	h.press("enter")
	if !h.s.Doc.Tests[0].AssertWritesCoveredByFilter {
		t.Fatal("enter did not turn coverage on")
	}
	// The rule has no filters, so its write is not covered.
	if sub := subtitle(h); !strings.HasPrefix(sub, "Fails: not covered by a filter: user:auth0|507f") {
		t.Fatalf("subtitle = %q", sub)
	}
	if v := plain(h.view(120, 30)); !strings.Contains(v, "Filter coverage: on") {
		t.Fatalf("the row does not say coverage is on:\n%s", v)
	}
	h.press("enter")
	if h.s.Doc.Tests[0].AssertWritesCoveredByFilter || h.changes != 2 {
		t.Fatalf("coverage = %v, changes = %d", h.s.Doc.Tests[0].AssertWritesCoveredByFilter, h.changes)
	}
}

func TestATestsNameMustBeUniqueAndNotEmpty(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{
		ownTest(t, d, "first", orgEvent("org_b")),
		ownTest(t, d, "second", orgEvent("org_c")),
	}
	h := newHarness(t, d, nil, func(s *Session) component { return newTestEditor(s, Focus{}, 1) })
	selectEditorRow(t, h, "Name")
	h.press("enter")
	topAs[*testNameForm](t, h)
	for _, taken := range []string{"first", "member-added", ""} {
		backspace(h, 20)
		h.typeText(taken)
		h.press("enter")
		topAs[*testNameForm](t, h)
		if got := h.s.Doc.Tests[1].Name; got != "second" || h.changes != 0 {
			t.Fatalf("%q was accepted: name = %q, changes = %d", taken, got, h.changes)
		}
	}
	h.typeText("third")
	h.press("enter")
	topAs[*testEditor](t, h)
	if got := h.s.Doc.Tests[1].Name; got != "third" || h.changes != 1 {
		t.Fatalf("name = %q, changes = %d", got, h.changes)
	}
}

// F2: moving through the screens and leaving edits nothing, so it reports
// nothing and leaves the document as it was.
func TestLookingWithoutEditingChangesNothing(t *testing.T) {
	for name, keys := range map[string][]string{
		"list":        {"down", "up", "esc"},
		"open own":    {"enter", "down", "down", "esc", "esc"},
		"editor":      {"enter", "down", "down", "down", "up", "esc"},
		"name form":   {"enter", "enter", "tab", "tab", "esc", "esc"},
		"same name":   {"enter", "enter", "enter", "esc"},
		"tuple form":  {"enter", "a", "tab", "tab", "tab", "esc", "esc"},
		"event pick":  {"enter", "down", "enter", "esc", "esc"},
		"delete then": {"d", "n", "esc"},
	} {
		h := newHarness(t, editorDoc(t), nil, openTests)
		h.press(keys...)
		if h.changes != 0 {
			t.Fatalf("%s: %v reported %d changes", name, keys, h.changes)
		}
		if !reflect.DeepEqual(h.s.Doc, editorDoc(t)) {
			t.Fatalf("%s: %v rewrote the document:\n%+v", name, keys, h.s.Doc.Tests)
		}
	}
}

func TestTestNamesCannotDriveTheTerminal(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{{Name: "\x1b[2J\x1b]0;pwned\aown\u009b31m evil\u202egnp.exe", Input: map[string]any{"type": "\x1b[31mx\u202ey"}}}
	for name, open := range map[string]func(*Session) component{
		"list": openTests, "editor": openTestEditor,
		"name form": func(s *Session) component { return newTestNameForm(s, Focus{}, 0) },
		"confirm":   func(s *Session) component { return newConfirmTestDelete(s, 0) },
	} {
		h := newHarness(t, d, nil, open)
		v := h.view(72, 20)
		_, sub := h.st.Heading()
		if hasTerminalControls(plain(v)) || hasTerminalControls(sub) {
			t.Fatalf("%s carries terminal controls: %q / %q", name, v, sub)
		}
	}
}

// A test can expect more tuples than the pane has lines; the cursor's row stays
// on screen.
func TestTheEditorScrollsToTheCursor(t *testing.T) {
	d := editorDoc(t)
	for i := range 10 {
		d.Tests[0].ExpectTuples = append(d.Tests[0].ExpectTuples,
			language.Tuple{User: "user:" + string(rune('a'+i)), Relation: "member", Object: "organization:acme"})
	}
	h := newHarness(t, d, nil, openTestEditor)
	selectEditorRow(t, h, "Filter coverage")
	if v := plain(h.view(72, 6)); !strings.Contains(v, "Filter coverage") || strings.Count(v, "\n") > 5 {
		t.Fatalf("the cursor's row is off screen, or the view is too tall:\n%s", v)
	}
}

func TestHubTPostsRequestTests(t *testing.T) {
	h := newHarness(t, threeRules(), nil, openHub)
	if keys := strings.Join(h.st.Keys(), "  "); !strings.Contains(keys, "t tests") {
		t.Fatalf("keys = %s", keys)
	}
	h.press("down", "t")
	if !h.closed || h.result != RequestTests {
		t.Fatalf("closed = %v, result = %v, want RequestTests", h.closed, h.result)
	}

	// OpenTests is what a host opens on that request, on the highlighted rule.
	st := OpenTests(h.s, topAs[*rulesHub](t, h).focus())
	if f, ok := st.Focus(); !ok || f.Rule != 1 {
		t.Fatalf("focus = %+v, want the rule the hub had highlighted", f)
	}

	h = newHarness(t, &mapping.Document{}, nil, openHub)
	if keys := strings.Join(h.st.Keys(), "  "); strings.Contains(keys, "t tests") {
		t.Fatalf("the empty hub offers tests: %s", keys)
	}
	h.press("t")
	if h.closed {
		t.Fatal("t on the empty hub asked for the tests")
	}
}

// longTestDoc is editorDoc with a failing own test whose name, tuple and
// filter are far wider than any pane, for the sweeps.
func longTestDoc(t *testing.T) *mapping.Document {
	d := editorDoc(t)
	tc := &d.Tests[0]
	tc.Name = strings.Repeat("n", 300)
	tc.ExpectTuples = append(tc.ExpectTuples, language.Tuple{
		User: "user:" + strings.Repeat("u", 300), Relation: "member", Object: "organization:acme", Condition: "in_region",
	})
	tc.ExpectTupleFilters = []language.TupleFilter{{Object: "organization:" + strings.Repeat("o", 300), Action: language.FilterActionPatch}}
	return d
}

// M8: the editor offers ↵ only on a row it edits and d only on a row it
// removes.
func TestTheEditorsKeysFollowTheRow(t *testing.T) {
	always := []string{"a add tuple", "r reset", "n expect nothing", "esc back"}
	h := newHarness(t, twoFilterDoc(t), nil, openTestEditor)
	for row, want := range map[string]struct{ edit, remove bool }{
		"Name":            {edit: true},
		"Event":           {edit: true},
		"user:":           {remove: true},
		"relation=member": {remove: true},
		"Filter coverage": {edit: true},
	} {
		selectEditorRow(t, h, row)
		keys := strings.Join(h.st.Keys(), "  ")
		if strings.Contains(keys, "↵ edit") != want.edit || strings.Contains(keys, "d remove") != want.remove {
			t.Fatalf("on %s, keys = %s", row, keys)
		}
		for _, k := range always {
			if !strings.Contains(keys, k) {
				t.Fatalf("on %s, keys = %s, missing %q", row, keys, k)
			}
		}
	}
	h = newHarness(t, editorDoc(t), nil, openTestEditor)
	h.press("n")
	selectEditorRow(t, h, "Expects nothing")
	if keys := strings.Join(h.st.Keys(), "  "); strings.Contains(keys, "↵ edit") || strings.Contains(keys, "d remove") {
		t.Fatalf("on a note, keys = %s", keys)
	}
}

// Picking the event a test already has is no edit.
func TestRepickingTheSameEventChangesNothing(t *testing.T) {
	h := newHarness(t, editorDoc(t), nil, openTestEditor)
	pick := func() {
		selectEditorRow(t, h, "Event")
		h.press("enter")
		if !topAs[*eventPick](t, h).list.SelectID("organization.member.added") {
			t.Fatal("could not select the event")
		}
		h.press("enter")
		topAs[*testEditor](t, h)
	}
	pick() // the catalog's copy, whatever it holds
	before := h.changes
	pick()
	if h.changes != before {
		t.Fatalf("picking the same event again reported %d changes", h.changes-before)
	}
}

// A2: with the list filtered, ↵ and d act on the row the user sees, not on
// the first row.
func TestAFilteredTestListActsOnTheRowShown(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{
		ownTest(t, d, "alpha", orgEvent("org_a")),
		ownTest(t, d, "bravo", orgEvent("org_b")),
	}
	h := newHarness(t, d, nil, openTests)
	h.press("/")
	h.typeText("bravo")
	h.press("enter", "enter")
	if c := topAs[*testEditor](t, h); c.idx != 1 {
		t.Fatalf("↵ opened test %d, want bravo's", c.idx)
	}
	h = newHarness(t, d, nil, openTests)
	h.press("/")
	h.typeText("bravo")
	h.press("enter", "d", "y")
	if got := h.s.Doc.Tests; len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("tests = %+v, want bravo deleted", got)
	}
}

// M7: an expectation is the value FGA stores, so a template in it is literal
// text no evaluated tuple carries, and the test could never pass.
func TestAnExpectedTupleTakesNoTemplates(t *testing.T) {
	h := newHarness(t, editorDoc(t), nil, openTestEditor)
	h.press("a")
	h.typeText("organization:{{ input.id }}")
	h.press("tab")
	h.typeText("member")
	h.press("tab")
	h.typeText("user:bob")
	h.press("ctrl+s")
	topAs[*expectForm](t, h)
	if v := plain(h.view(120, 30)); !strings.Contains(v, "an expectation is a literal value, not a template") {
		t.Fatalf("the form does not say why:\n%s", v)
	}

	h = newHarness(t, editorDoc(t), nil, openTestEditor)
	h.press("a")
	h.typeText("organization:acme")
	h.press("tab")
	h.typeText("member")
	h.press("tab")
	h.typeText("user:bob")
	h.press("tab", "tab")
	h.typeText("in_region")
	h.press("tab")
	h.typeText("region={{ input.region }}")
	h.press("ctrl+s")
	topAs[*expectForm](t, h)
	if n := len(h.s.Doc.Tests[0].ExpectTuples); n != 1 || h.changes != 0 {
		t.Fatalf("tuples = %d, changes = %d", n, h.changes)
	}
	if v := plain(h.view(120, 30)); !strings.Contains(v, "an expectation is a literal value, not a template") {
		t.Fatalf("the form does not say why:\n%s", v)
	}
}

func TestReadOnlyRefusesEdits(t *testing.T) {
	// The keys read-only drops, per component.
	for _, tc := range []struct {
		name string
		doc  func() *mapping.Document
		open func(s *Session) component
		keys []string // pressed
		gone string   // hint that must not be offered
	}{
		{"hub add", threeRules, openHub, []string{"a"}, "a add"},
		{"hub delete", threeRules, openHub, []string{"d"}, "d delete"},
		{"hub save", threeRules, openHub, []string{"s"}, ""},
		{"spoke open", func() *mapping.Document { return recipeDoc(t, "user.updated") }, openSpoke, []string{"enter", " "}, "↵ open"},
		{"tests add", func() *mapping.Document { return longTestDoc(t) }, openTests, []string{"a"}, "a add"},
		{"tests delete", func() *mapping.Document { return longTestDoc(t) }, openTests, []string{"d"}, "d delete"},
		{"tests open", func() *mapping.Document { return longTestDoc(t) }, openTests, []string{"enter"}, "↵ open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := tc.doc()
			before, err := mapping.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			h := newHarness(t, doc, nil, tc.open)
			h.s.ReadOnly = true
			depth := h.st.Depth()
			if tc.gone != "" && slices.Contains(h.st.Keys(), tc.gone) {
				t.Fatalf("keys offer %q: %v", tc.gone, h.st.Keys())
			}
			h.press(tc.keys...)
			after, _ := mapping.Marshal(doc)
			if string(after) != string(before) || len(doc.Tests) != len(tc.doc().Tests) {
				t.Fatal("the document changed")
			}
			if h.changes != 0 || len(h.toasts) != 0 || h.closed || h.st.Depth() != depth {
				t.Fatalf("changes = %d, toasts = %v, closed = %v, depth %d -> %d",
					h.changes, h.toasts, h.closed, depth, h.st.Depth())
			}
		})
	}
}
