package mapping

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

// TestQuestionMarkExplainsTheCurrentScreen asserts a distinctive token from
// the tuples help body itself, not a word ("tuple") that also appears in the
// chrome or a YAML key — paneView still renders the breadcrumb, context chips
// and the live YAML preview underneath the overlay body, and the breadcrumb's
// title "Tuples" would make a bare-word check pass even with an empty body.
//
// A token, not a phrase: once the body wraps to cw, any multi-word phrase is
// width-dependent ("who, what, which" is present at 120 but absent at 100 and
// at the 44-col floor). user:alice is 10 cells and cw never goes below 32, so
// it survives every supported width — and it is part of the body, so blanking
// the body still fails this test. Asserting at both extremes pins that down.
func TestQuestionMarkExplainsTheCurrentScreen(t *testing.T) {
	m := atTuples(t)
	send(m, key("?"))
	if m.top() != screenHelp {
		t.Fatalf("top = %v, want the help overlay", m.top())
	}
	for _, w := range []int{120, minCols} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		if !strings.Contains(m.viewString(), "user:alice") {
			t.Fatalf("the overlay does not explain tuples at width %d:\n%s", w, m.viewString())
		}
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
// one added to helpFor without a matching case in filtering(). Asserting only
// top() would still pass if ? were swallowed instead of reaching the filter
// box, so each row also checks the filter actually received the character.
func TestQuestionMarkTypesIntoAListFilter(t *testing.T) {
	for _, c := range []struct {
		name        string
		open        func(t *testing.T) *wizardModel
		want        screen
		filterValue func(*wizardModel) string
	}{
		{"rules hub", atRulesHubWithRule, screenRules,
			func(m *wizardModel) string { return m.rules.Model.FilterValue() }},
		{"tuples", atTuplesWithOne, screenTuples,
			func(m *wizardModel) string { return m.tupleList.Model.FilterValue() }},
		{"variables", atVariables, screenVariables,
			func(m *wizardModel) string { return m.varList.Model.FilterValue() }},
		{"filters", atFilters, screenFilters,
			func(m *wizardModel) string { return m.filterList.Model.FilterValue() }},
		{"event pick", atEventPick, screenEventPick,
			func(m *wizardModel) string { return m.events.Model.FilterValue() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := c.open(t)
			send(m, key("/"), key("?"))
			if m.top() != c.want {
				t.Fatalf("? opened help from inside a filter: top = %v", m.top())
			}
			if got := c.filterValue(m); !strings.Contains(got, "?") {
				t.Fatalf("the ? never reached the filter box: filter value = %q", got)
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

// TestHelpNeverOverflowsTheTerminal sweeps screenChrome, and screenHelp has no
// entry there — so the overlay's body, the one piece of screen content the
// wizard renders from its own string rather than a widget, escapes it. This is
// the test that catches it.
func TestHelpNeverOverflowsTheTerminal(t *testing.T) {
	for _, under := range []screen{
		screenRules, screenTuples, screenVariables,
		screenFilters, screenAction, screenEventPick,
	} {
		for _, sz := range []struct{ w, h int }{{minCols, minRows}, {72, 24}, {120, 40}} {
			m := newTestWizard(t, nil)
			// Set the stack rather than navigating, the same convention
			// atRulesHub documents: this means "help open over that screen".
			m.stack = []screen{under, screenHelp}
			m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
			for i, line := range strings.Split(m.viewString(), "\n") {
				if w := lipgloss.Width(line); w > sz.w {
					t.Errorf("help over %v at %dx%d: line %d is %d cells wide",
						under, sz.w, sz.h, i, w)
				}
			}
		}
	}
}

// The empty hub replaces its hints wholesale, so it can silently drop ? while
// the binding still works — a gap TestHelpAdvertisingMatchesHelpFor cannot see,
// because that one reads the static screenChrome map rather than chromeFor().
func TestEmptyHubAdvertisesHelp(t *testing.T) {
	m := atRulesHub(t)
	if len(m.doc.Rules) != 0 {
		t.Fatalf("want an empty hub, got %d rules", len(m.doc.Rules))
	}
	for _, h := range m.chromeFor().keys {
		if h.key == "?" {
			return
		}
	}
	t.Fatal("the empty rules hub does not advertise ?, but ? opens help there")
}

func TestTheIteratorHelpNamesTheLimitsNoSampleCanShow(t *testing.T) {
	_, body, ok := helpFor(screenIterator)
	if !ok {
		t.Fatal("no help for the iterator screen")
	}
	for _, want := range []string{"1000", "40"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the iterator help does not name %s:\n%s", want, body)
		}
	}
}

func TestHelpScrollsOnTheArrowsAndClosesOnAnythingElse(t *testing.T) {
	m := atFilters(t)
	// The narrowest terminal the wizard supports, where the filters help is far
	// longer than the card can hold — the case the arrows exist for.
	m.width, m.height = minCols, minRows
	m.applySize()
	send(m, key("?"))
	if m.top() != screenHelp {
		t.Fatalf("top = %v, want help", m.top())
	}

	first := m.viewString()
	send(m, key("down"))
	if m.top() != screenHelp {
		t.Fatal("an arrow closed the overlay instead of scrolling it")
	}
	if m.viewString() == first {
		t.Fatal("the help did not scroll")
	}
	send(m, key("up"))
	if got := m.viewString(); got != first {
		t.Fatal("scrolling back up did not return to the top of the help")
	}

	send(m, key("x"))
	if m.top() == screenHelp {
		t.Fatal("a key that is not an arrow should close the overlay")
	}
}

func TestHelpDoesNotShareTheScreenWithThePreview(t *testing.T) {
	// The help overlay is a modal, and stacked on a short terminal the preview
	// pane lands underneath it and takes the rows the explanation needs. The
	// longest help bodies were the ones that got cut.
	m := atFilters(t)
	m.width, m.height = 80, 24
	m.applySize()
	send(m, key("?"))
	if got := plain(m.viewString()); strings.Contains(got, "preview") {
		t.Fatalf("the help overlay still draws the preview pane:\n%s", got)
	}
}
