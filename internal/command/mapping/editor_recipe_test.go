package mapping

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

// Adding a rule offers the catalog, every time — not only on first run, and not
// buried behind ctrl+e once the user is already lost in a blank form.
func TestAddingARuleOffersThePayloadKind(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	if m.top() != screenPayloadKind {
		t.Fatalf("top = %v, want the payload-kind screen", m.top())
	}
	out := m.viewString()
	for _, want := range []string{"Auth0", "JSON"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from the payload-kind screen:\n%s", want, out)
		}
	}
}

func TestChoosingAuth0OpensTheEventCatalog(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	if m.top() != screenEventPick {
		t.Fatalf("top = %v, want the event picker", m.top())
	}
}

func TestChoosingOwnPayloadOpensThePasteScreen(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("down"), key("enter"))
	if m.top() != screenEventPaste {
		t.Fatalf("top = %v, want the paste screen", m.top())
	}
}

// The screen earns its place by showing the resolved value next to the path it
// came from. Templates only click when you see both.
func TestTheRecipeScreenShowsResolvedValuesAndTheirPaths(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter")) // payload kind -> Auth0 catalog
	selectEvent(t, m, "organization.member.added")

	out := m.viewString()
	for _, want := range []string{
		"user:auth0|",                 // the resolved user, escaped
		"data.object.user.user_id",    // where it came from
		"member",                      // the fixed relation
		"data.object.organization.id", // the object's path
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from the recipe screen:\n%s", want, out)
		}
	}
}

// With no model loaded there is nothing to check against, so requirements are
// stated as expectations rather than marked as failures.
func TestTheRecipeScreenStatesExpectationsWithoutAModel(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, "organization.member.added")

	out := m.viewString()
	if !strings.Contains(out, "expects") {
		t.Fatalf("no expectation wording with no model loaded:\n%s", out)
	}
	if strings.Contains(out, "not in your model") {
		t.Fatalf("a missing model must not be reported as a missing type:\n%s", out)
	}
}

// Accepting the recipe opens the rule it wrote, with the add-rule fork unwound
// behind it — not a stack five screens deep, and not a bare hub either.
//
// The stack depth is the point. Replacing the stack with just the hub made the
// next esc open the save dialog, so a user reaching for "back" a moment after
// accepting a mapping was asked whether to write the file instead.
func TestUsingARecipeOpensTheRuleAndKeepsEscMeaningBack(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, "organization.member.added")
	send(m, key("enter"))

	if m.top() != screenRule {
		t.Fatalf("top = %v, want the new rule open", m.top())
	}
	if len(m.stack) != 2 {
		t.Fatalf("stack is %d deep, want 2 (hub, rule) — the fork must be unwound", len(m.stack))
	}
	// The half that actually bit the user: esc is "back", not "save".
	send(m, key("esc"))
	if m.top() != screenRules {
		t.Fatalf("esc after accepting a recipe went to %v, want the hub", m.top())
	}
	if len(m.doc.Rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(m.doc.Rules))
	}
	r := m.doc.Rules[0]
	if r.Name != "organization.member.added" {
		t.Fatalf("rule name = %q", r.Name)
	}
	if len(r.Tuples) != 1 {
		t.Fatalf("got %d tuples, want 1", len(r.Tuples))
	}
	if r.Sample == nil {
		t.Fatal("the rule kept no sample, so the preview has nothing to evaluate")
	}
}

// A recipe arrives with Name and When already filled in, so the rule has to
// record them as auto-filled: applyEventType only replaces a field that is
// empty or still holds exactly what the last pick wrote, and a recipe rule that
// forgot to say so answers "the user typed this" to both guards. The user then
// switches the event with ctrl+e and gets the new sample beside the old
// trigger, whose `when` never fires — the preview says "skipped" with no reason.
func TestSwitchingTheEventOnARecipeRuleMovesItsTrigger(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter")) // add rule: kind screen -> Auth0 -> catalog
	if !m.events.SelectID("organization.member.added") {
		t.Fatal("could not select the event")
	}
	send(m, key("enter")) // open the recipe
	send(m, key("enter")) // use it -> the recipe's rule, open

	// The rule the recipe wrote is already open; take its trigger form.
	if m.top() != screenRule {
		t.Fatalf("top = %v, want the rule hub", m.top())
	}
	send(m, key("enter"))
	if m.top() != screenTrigger {
		t.Fatalf("top = %v, want the trigger form", m.top())
	}

	// Change the event. Both events carry a mapping recipe, so the switch is a
	// real one rather than a move onto an explain-only entry.
	send(m, key("ctrl+e"))
	if !m.events.SelectID("organization.connection.added") {
		t.Fatal("could not select the second event")
	}
	send(m, key("enter"))

	r := m.rule()
	if r == nil {
		t.Fatal("no rule")
	}
	if r.Name != "organization.connection.added" {
		t.Fatalf("name = %q, want the new event — the old trigger survived the switch", r.Name)
	}
	if want := `input.type == "organization.connection.added"`; r.When != want {
		t.Fatalf("when = %q, want %q — the new sample landed beside the old condition", r.When, want)
	}
}

// The one event that maps to nothing explains itself rather than dead-ending.
func TestAnEventWithNoMappingExplainsWhy(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, firstUnmappedEvent(t).Type)

	out := m.viewString()
	if !strings.Contains(out, "relationship") {
		t.Fatalf("no explanation for an event that maps to nothing:\n%s", out)
	}
}

// firstUnmappedEvent returns a catalog entry whose recipe maps to nothing. It
// is found by asking Maps() rather than by naming an event, so the tests below
// keep testing the right thing when the catalog gains another explain-only
// entry — or when one of today's grows a mapping.
func firstUnmappedEvent(t *testing.T) auth0.Event {
	t.Helper()
	for _, e := range auth0.Catalog() {
		if !e.Recipe.Maps() {
			return e
		}
	}
	t.Fatal("no explain-only event in the catalog")
	return auth0.Event{}
}

// An explain-only recipe has nothing to append. Before this, enter took the
// same path as a real recipe and dropped a zero-valued Rule on the hub: no
// name, no trigger, no tuples, and a blocking problem the user never asked for.
// What it appends instead is the rule a pasted payload would have produced:
// named and triggered off the event, carrying its sample, and empty of tuples
// for the user to fill. Reading why an event maps to nothing is an argument for
// writing a different rule, not for being sent back the way you came.
func TestAnEventWithNoMappingStillStartsARule(t *testing.T) {
	e := firstUnmappedEvent(t)

	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, e.Type)

	send(m, key("enter"))

	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d, want 1:\n%+v", len(m.doc.Rules), m.doc.Rules)
	}
	r := m.doc.Rules[0]
	if r.Sample == nil || r.Sample.Label != e.Type {
		t.Fatalf("the event's payload did not come with it: sample = %+v", r.Sample)
	}
	if r.Name == "" || r.When == "" {
		t.Fatalf("the trigger was left blank: name = %q, when = %q", r.Name, r.When)
	}
	// The zero Rule this screen must never append is the one with no tuples AND
	// nothing identifying it; tuples are the half the user is here to write.
	if len(r.Tuples) != 0 {
		t.Fatalf("%s maps to nothing but %d tuples appeared:\n%+v", e.Type, len(r.Tuples), r.Tuples)
	}
	if m.top() != screenRule {
		t.Fatalf("top = %v, want the new rule's editor", m.top())
	}
}

// One of the twenty-one events maps to nothing. Learning that only after
// picking it reads as a fault — in the wizard, or in a setup the user has not
// finished — so the row says it up front, in the words the rule editor uses.
func TestTheEventListSaysWhatEachEventMaps(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))

	// The top rows are always on screen, and between them they cover the two
	// kinds a user has to tell apart on sight: one that maps by iterating an
	// array, one that maps by deleting rather than writing.
	v := m.events.View()
	for _, want := range []string{"user.created · 1 tuple", "user.deleted · 2 tuple filters"} {
		if !strings.Contains(v, want) {
			t.Fatalf("the list does not say what the event maps: want %q in:\n%s", want, v)
		}
	}

	// And the two further down the catalog, reached by scrolling: the writing
	// half, and the one event that maps nothing. Selected rather than filtered:
	// filtering styles each matched rune individually, which puts escape
	// sequences between the letters of the very title this asserts on.
	for id, want := range map[string]string{
		"organization.member.added": "organization.member.added · 1 tuple",
		"organization.updated":      "organization.updated · attributes",
	} {
		m.events.SelectID(id)
		if v := m.events.View(); !strings.Contains(v, want) {
			t.Fatalf("the list does not say what %s maps: want %q in:\n%s", id, want, v)
		}
	}
}

// The complaint this answers: "it doesn't let me continue". A screen whose only
// exits are backwards is a dead end however good its explanation.
func TestAnEventWithNoMappingOffersAWayForward(t *testing.T) {
	e := firstUnmappedEvent(t)

	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, e.Type)

	var forward bool
	for _, k := range m.chromeFor().keys {
		if k.key == "↵" {
			forward = true
		}
		// These recipes require nothing of the model, so offering to change it
		// here invites the reading that the missing model is why the event was
		// given no mapping. It is reachable from the hub and from the rule.
		if k.key == "m" {
			t.Errorf("%s requires no model but the footer offers %q %q", e.Type, k.key, k.label)
		}
	}
	if !forward {
		t.Fatalf("%s is a dead end: the footer offers no ↵, only %+v", e.Type, m.chromeFor().keys)
	}
}

// The footer is only half of what the screen promises. The subtitle sits
// directly above the paragraph explaining why there is no mapping, so leaving
// it on the mapping copy has the screen contradict itself in two lines.
func TestAnEventWithNoMappingSaysSoInTheSubtitle(t *testing.T) {
	e := firstUnmappedEvent(t)

	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, e.Type)

	if sub := m.chromeFor().subtitle; strings.Contains(sub, "ready-made mapping for this event") {
		t.Fatalf("%s maps to nothing but the subtitle still promises a mapping: %q", e.Type, sub)
	}
}

// The mapping recipes keep offering it, so the guard above is a branch rather
// than a blanket removal.
func TestAMappingEventStillOffersTheUseKey(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, "organization.member.added")

	var found bool
	for _, k := range m.chromeFor().keys {
		if k.key == "↵" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a mapping recipe stopped offering ↵: %+v", m.chromeFor().keys)
	}
}

// selectEvent filters the catalog to one event and opens it.
func selectEvent(t *testing.T, m *wizardModel, typ string) {
	t.Helper()
	send(m, key("/"))
	typeText(m, typ)
	send(m, key("enter")) // accept the filter
	send(m, key("enter")) // open the highlighted event
	if m.top() != screenRecipe {
		t.Fatalf("top = %v, want the recipe screen", m.top())
	}
}

func TestSourcePathNamesWhereAValueCameFrom(t *testing.T) {
	tests := []struct{ in, want string }{
		{"user:{{ fga_escape(input.data.object.user.user_id) }}", "data.object.user.user_id"},
		{"organization:{{ input.data.object.organization.id }}", "data.object.organization.id"},
		{"member", "(fixed)"},
		{"{{ input.data.object.role.name }}", "data.object.role.name"},
		{"{{ a }}-{{ b }}", "{{ a }}-{{ b }}"},
	}
	for _, tc := range tests {
		if got := sourcePath(tc.in); got != tc.want {
			t.Errorf("sourcePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The add-rule fork asks about a rule that does not exist yet, so neither of its
// screens gets a preview beside it. The payload-kind screen was fixed on its own
// once and the recipe screen left behind, which put a red "must contain at least
// one rule" next to the very mapping the user was being offered: the pane was
// answering a question about the file while the screen asked one about the rule.
//
// Both variants are covered, because they reach the screen by different routes
// and only one of them has a mapping to show.
func TestTheAddRuleForkShowsNoPreview(t *testing.T) {
	for _, event := range []string{
		"organization.member.added", // ships a mapping
		"user.created",              // explain-only
	} {
		t.Run(event, func(t *testing.T) {
			m := atRulesHub(t)
			send(m, key("a"))
			kind := m.viewString()
			send(m, key("enter"))
			selectEvent(t, m, event)
			if m.top() != screenRecipe {
				t.Fatalf("top = %v, want the recipe screen", m.top())
			}

			for name, out := range map[string]string{"payload kind": kind, "recipe": m.viewString()} {
				// The preview's own heading and the lint line it carries. An empty
				// document fails both, and the recipe is the screen where a red cross
				// is least likely to be read as being about the document.
				for _, unwanted := range []string{"preview", "at least one rule"} {
					if strings.Contains(out, unwanted) {
						t.Fatalf("the %s screen still shows the preview (%q):\n%s", name, unwanted, out)
					}
				}
			}
		})
	}
}

// The payload-kind screen and the event-pick help both quote how many catalog
// events ship a mapping. They were written by hand and drifted — one promised
// all twenty-one mapped, the other said twelve — and the optimistic one taught
// users that an event mapping to nothing meant their own setup was incomplete.
// Both now derive the number, so this pins them to the catalog and to each
// other rather than to a literal that can rot again.
func TestTheAdvertisedMappingCountMatchesTheCatalog(t *testing.T) {
	want := 0
	for _, e := range auth0.Catalog() {
		if e.Recipe.Maps() {
			want++
		}
	}
	if got := mappedCount(); got != want {
		t.Fatalf("mappedCount() = %d, want %d", got, want)
	}

	m := newTestWizard(t, nil)
	send(m, key("enter"))
	// The wizard opens on the model question, stacked over the fork. esc skips
	// it and reveals the payload-kind screen underneath, which is the one this
	// test is about.
	send(m, key("esc"))
	kind := m.viewString()
	if !strings.Contains(kind, fmt.Sprintf("%d event types, %d with a ready-made mapping", len(auth0.Catalog()), want)) {
		t.Fatalf("the payload-kind screen misstates the catalog:\n%s", kind)
	}

	_, body, ok := helpFor(screenEventPick)
	if !ok {
		t.Fatal("the event list lost its help entry")
	}
	if !strings.Contains(body, fmt.Sprintf("%d of the %d", want, len(auth0.Catalog()))) {
		t.Fatalf("the event help misstates the catalog: %q", body)
	}
}

// sectionDesc returns the rule hub's summary for one section row.
func sectionDesc(t *testing.T, m *wizardModel, title string) string {
	t.Helper()
	for _, it := range m.ruleSections() {
		if it.Title == title {
			return it.Desc
		}
	}
	t.Fatalf("no %q row on the rule hub", title)
	return ""
}

// atRuleFor accepts the ready-made recipe for typ and stops on its rule hub.
func atRuleFor(t *testing.T, typ string) *wizardModel {
	t.Helper()
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, typ)
	send(m, key("enter"))
	if m.top() != screenRule {
		t.Fatalf("top = %v, want the rule hub", m.top())
	}
	return m
}

// The complaint this answers: "the tuples in the rule aren't populated
// correctly, I see it in the preview but not in the rule picker". They were
// populated — user.created writes from its iterator, so the tuple sat on
// Iterator.Tuples while the Tuples row counted only the rule's own and said
// "0 tuples". Beside a preview plainly showing a tuple, that reads as the
// recipe having failed rather than as the list living one screen over.
func TestTheTuplesRowSaysWhenTheTuplesAreOnTheIterator(t *testing.T) {
	m := atRuleFor(t, "user.created")

	desc := sectionDesc(t, m, "Tuples")
	if strings.Contains(desc, "0 tuple") {
		t.Fatalf("the rule writes a tuple from its iterator but the row says %q", desc)
	}
	if !strings.Contains(desc, "iterator") {
		t.Fatalf("the row does not say where the tuples are: %q", desc)
	}
}

// A rule can write from both lists. connection.created does — one tuple of its
// own for the tenant, one per enabled client from the iterator — so the row
// has to count them separately rather than pick one.
func TestTheTuplesRowCountsBothListsSeparately(t *testing.T) {
	m := atRuleFor(t, "connection.created")

	if desc := sectionDesc(t, m, "Tuples"); !strings.Contains(desc, "1 tuple") || !strings.Contains(desc, "iterator") {
		t.Fatalf("the row does not account for both lists: %q", desc)
	}
}

// And a rule with no iterator says nothing about one.
func TestTheTuplesRowStaysPlainWithoutAnIterator(t *testing.T) {
	m := atRuleFor(t, "organization.member.added")

	if desc := sectionDesc(t, m, "Tuples"); desc != "1 tuple" {
		t.Fatalf("desc = %q, want a plain count", desc)
	}
}

// Both tuple lists render on the same screen, so the chrome is the only thing
// that says which one you are looking at. Sending a user to the iterator's
// tuples is no use if arriving there looks identical to where they started.
func TestTheIteratorsTupleListSaysItIsTheIterators(t *testing.T) {
	m := atRuleFor(t, "user.created")

	for i := 0; i < len(m.ruleSections()); i++ {
		if m.sections.Selected().Title == "Iterator" {
			break
		}
		m.sections.Move(1)
	}
	send(m, key("enter"), key("ctrl+t"))
	if m.top() != screenTuples || !m.inIter {
		t.Fatalf("top = %v, inIter = %v, want the iterator's tuple list", m.top(), m.inIter)
	}

	c := m.chromeFor()
	if c.title == screenChrome[screenTuples].title {
		t.Fatalf("the iterator's tuple list is titled the same as the rule's: %q", c.title)
	}
	if !strings.Contains(c.subtitle, "input.data.object.identities") {
		t.Fatalf("the subtitle does not name the list being walked: %q", c.subtitle)
	}
}
