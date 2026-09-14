package mapping

import (
	"strings"
	"testing"
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

// Accepting the recipe leaves the user on the hub with a real rule, not on a
// stack five screens deep.
func TestUsingARecipeLandsOnTheHubWithTheRule(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, "organization.member.added")
	send(m, key("enter"))

	if m.top() != screenRules {
		t.Fatalf("top = %v, want the hub", m.top())
	}
	if len(m.stack) != 1 {
		t.Fatalf("stack is %d deep, want 1", len(m.stack))
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
	send(m, key("enter")) // use it -> the hub, with the recipe's rule

	// Open the rule the recipe wrote, and its trigger form.
	send(m, key("enter"))
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

// The nine events that map to nothing explain themselves rather than dead-ending.
func TestAnEventWithNoMappingExplainsWhy(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, "organization.created")

	out := m.viewString()
	if !strings.Contains(out, "relationships") {
		t.Fatalf("no explanation for an event that maps to nothing:\n%s", out)
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
