package mapping

import (
	"strings"
	"testing"
)

// TestQuestionMarkExplainsTheCurrentScreen asserts a distinctive phrase from
// the tuples help body itself, not a word ("tuple") that also appears in the
// chrome or a YAML key — paneView still renders the breadcrumb, context chips
// and the live YAML preview underneath the overlay body, and the breadcrumb's
// title "Tuples" would make a bare-word check pass even with an empty body.
func TestQuestionMarkExplainsTheCurrentScreen(t *testing.T) {
	m := atTuples(t)
	send(m, key("?"))
	if m.top() != screenHelp {
		t.Fatalf("top = %v, want the help overlay", m.top())
	}
	if !strings.Contains(m.viewString(), "who, what, which thing") {
		t.Fatalf("the overlay does not explain tuples:\n%s", m.viewString())
	}
}

func TestAnyKeyDismissesHelp(t *testing.T) {
	m := atTuples(t)
	send(m, key("?"), key("x"))
	if m.top() != screenTuples {
		t.Fatalf("top = %v, want to be back where we started", m.top())
	}
}

// A form screen must not steal a literal ? from someone typing a template.
func TestQuestionMarkTypesIntoAForm(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"))
	typeText(m, "organization:a?b")
	if m.top() != screenTuple {
		t.Fatalf("? opened help from inside a form: top = %v", m.top())
	}
	if got := (*m.tuples())[0].Object; got != "organization:a?b" {
		t.Fatalf("the ? was swallowed: %q", got)
	}
}

// atRulesHubWithRule is atRulesHub with one rule already in the document.
// bubbles disables its "/" filter binding on an empty list, so exercising the
// filter guard needs a hub that is not empty — the shape every real hub the
// user filters is in anyway.
func atRulesHubWithRule(t *testing.T) *wizardModel {
	t.Helper()
	m := atRulesHub(t)
	addRuleAtTrigger(m)
	send(m, key("esc"), key("esc")) // trigger -> rule -> rules hub
	if m.top() != screenRules {
		t.Fatalf("top = %v, want rules hub", m.top())
	}
	return m
}

// atTuplesWithOne is atTuples with one tuple already in the list, for the same
// reason atRulesHubWithRule needs one rule.
func atTuplesWithOne(t *testing.T) *wizardModel {
	t.Helper()
	m := atTuples(t)
	send(m, key("a"), key("esc"))
	if m.top() != screenTuples {
		t.Fatalf("top = %v, want tuples list", m.top())
	}
	return m
}

// atVariables returns a wizard with a model, one rule and one variable,
// sitting on the variables list.
func atVariables(t *testing.T) *wizardModel {
	t.Helper()
	m := atRuleHub(t)
	openSection(t, m, 2, screenVariables)
	send(m, key("a"), key("esc"))
	if m.top() != screenVariables {
		t.Fatalf("top = %v, want variables list", m.top())
	}
	return m
}

// atFilters returns a wizard with a model, one rule and one tuple filter,
// sitting on the tuple filters list.
func atFilters(t *testing.T) *wizardModel {
	t.Helper()
	m := atRuleHub(t)
	openSection(t, m, 4, screenFilters)
	send(m, key("a"), key("esc"))
	if m.top() != screenFilters {
		t.Fatalf("top = %v, want filters list", m.top())
	}
	return m
}

// atEventPick returns a wizard with a fresh rule, sitting on the event-pick
// list — reached from the trigger screen the same way a real user gets there.
func atEventPick(t *testing.T) *wizardModel {
	t.Helper()
	m := atTrigger(t)
	send(m, key("ctrl+e"))
	if m.top() != screenEventPick {
		t.Fatalf("top = %v, want screenEventPick", m.top())
	}
	return m
}

// / puts the list in filter mode, where ? is a character the user is typing.
// Covered per screen, not just once, because the whole point of filtering()
// is that these five screens differ — a single case would not catch the next
// one added to helpFor without a matching case in filtering().
func TestQuestionMarkTypesIntoAListFilter(t *testing.T) {
	for _, c := range []struct {
		name string
		open func(t *testing.T) *wizardModel
		want screen
	}{
		{"rules hub", atRulesHubWithRule, screenRules},
		{"tuples", atTuplesWithOne, screenTuples},
		{"variables", atVariables, screenVariables},
		{"filters", atFilters, screenFilters},
		{"event pick", atEventPick, screenEventPick},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := c.open(t)
			send(m, key("/"), key("?"))
			if m.top() != c.want {
				t.Fatalf("? opened help from inside a filter: top = %v", m.top())
			}
		})
	}
}

// TestHelpAdvertisingMatchesHelpFor checks the two directions the brief's
// weaker version missed: every screen helpFor answers for must advertise ? in
// its hints, and no screen that declines may advertise it — otherwise a user
// presses a key the wizard has told them exists and nothing happens. This is
// the test that catches the next helpFor entry that forgets its hint, the
// same drift the filtering()/helpFor pairing above guards against.
func TestHelpAdvertisingMatchesHelpFor(t *testing.T) {
	for s := screen(0); s < screenCount; s++ {
		_, body, ok := helpFor(s)
		if ok && strings.TrimSpace(body) == "" {
			t.Errorf("screen %v advertises help with an empty body", s)
		}

		advertised := false
		for _, h := range screenChrome[s].keys {
			if h.key == "?" {
				advertised = true
			}
		}
		if ok && !advertised {
			t.Errorf("screen %v has a helpFor entry but does not advertise ? in its hints", s)
		}
		if !ok && advertised {
			t.Errorf("screen %v advertises ? but has no helpFor entry", s)
		}
	}
}
