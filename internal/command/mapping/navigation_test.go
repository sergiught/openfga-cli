package mapping

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

// wayfindingBar is the frame's top border, which every screen draws and which
// carries the location and the way out set into it.
func wayfindingBar(t *testing.T, view string) string {
	t.Helper()
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "╭") {
			return l
		}
	}
	t.Fatalf("the view has no frame to carry a wayfinding bar:\n%s", view)
	return ""
}

// TestSaveWorksFromEveryScreen pins the key down as global. It used to be
// handled on exactly two screens — the rules hub and the rule hub — and on the
// form screens it fell through to the field package, where ctrl+s means
// "submit this form", so a user who learned it on the hub and pressed it while
// editing a trigger saved nothing and silently left the screen instead.
func TestSaveWorksFromEveryScreen(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   func(*testing.T) *wizardModel
	}{
		{"trigger form", func(t *testing.T) *wizardModel {
			t.Helper()
			m := atRulesHub(t)
			addRuleAtTrigger(m)
			return m
		}},
		{"tuple list", atTuples},
		{"rule hub", func(t *testing.T) *wizardModel {
			t.Helper()
			m := atRulesHub(t)
			addRuleAtTrigger(m)
			m.pop()
			return m
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.at(t)
			send(m, key("ctrl+s"))
			if m.top() != screenConfirmSave {
				t.Fatalf("ctrl+s from %s landed on %v, want the save dialog", tc.name, m.top())
			}
		})
	}
}

// TestSaveIsAdvertisedWhereverItWorks. The key being global is only half the
// fix: the complaint was that nothing said ctrl+s was for the whole file, and
// it was named on 2 of 28 hint sets.
func TestSaveIsAdvertisedWhereverItWorks(t *testing.T) {
	m := atRulesHub(t)
	addRuleAtTrigger(m)
	view := plain(m.viewString())
	if !strings.Contains(view, "^s") {
		t.Fatalf("the trigger screen does not mention ^s:\n%s", view)
	}
}

// TestSaveIsNotOfferedWithNothingToSave. An empty document has no rules, so the
// dialog it would push says only that there is nothing to save — advertising a
// key whose whole effect is that rebuke is worse than leaving it out.
func TestSaveIsNotOfferedWithNothingToSave(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("ctrl+s"))
	if m.top() == screenConfirmSave {
		t.Fatal("ctrl+s opened the save dialog with no rules to save")
	}
	if strings.Contains(plain(m.viewString()), "^s") {
		t.Fatalf("the empty hub advertises ^s:\n%s", plain(m.viewString()))
	}
}

// TestTopBarNamesWhereEscGoes. The complaint that started this: after picking
// an Auth0 event there was nothing on screen saying esc was the way back, or
// where back was. "esc back" would have answered half of it; the destination
// is the half a hint row cannot carry, because the hint row is the same on
// every screen that reaches this one.
func TestTopBarNamesWhereEscGoes(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, "organization.member.added")
	if m.top() != screenRecipe {
		t.Fatalf("top = %v, want the recipe card", m.top())
	}
	top := wayfindingBar(t, plain(m.viewString()))
	if !strings.Contains(top, "esc ‹ Pick an event") {
		t.Fatalf("the recipe card does not say where esc goes:\n%s", top)
	}
}

// The wayfinding bar is on every screen, cards included. Cards used to render
// no status bar at all, so the six screens that use one — the welcome, the
// payload fork, the recipe, the help overlay and both confirmations — were the
// only ones with no breadcrumb and no way out on screen.
func TestTopBarIsOnCardScreensToo(t *testing.T) {
	m := atRuleFor(t, "organization.member.added")
	send(m, key("ctrl+s"))
	if m.top() != screenConfirmSave {
		t.Fatalf("top = %v, want the save dialog", m.top())
	}
	if top := wayfindingBar(t, plain(m.viewString())); !strings.Contains(top, "Rules") {
		t.Fatalf("the save card carries no breadcrumb:\n%s", top)
	}
}

// The breadcrumb read screenChrome's static titles, so the three screens that
// retitle themselves at runtime appeared under the wrong name — the iterator's
// tuple list showed as the rule's own "Tuples", which is the one pair of
// screens a user cannot otherwise tell apart.
func TestBreadcrumbUsesRuntimeTitles(t *testing.T) {
	m := atRuleFor(t, "user.created")
	m.inIter = true
	m.push(screenTuples)
	top := wayfindingBar(t, plain(m.viewString()))
	if !strings.Contains(top, "Iterator tuples") {
		t.Fatalf("breadcrumb does not name the iterator's list:\n%s", top)
	}
}

// The bar costs no rows at all: it is set into a border the frame was drawing
// anyway. The chrome is the two status rows, and the body keeps everything
// else — which matters at the floor, where it is only nine rows.
func TestChromeStillFitsTheTerminalExactly(t *testing.T) {
	for _, sz := range [][2]int{{minCols, minRows}, {80, 24}, {120, 40}} {
		m := atRuleFor(t, "organization.member.added")
		m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		if h := lipgloss.Height(m.viewString()); h != sz[1] {
			t.Fatalf("at %d×%d the view is %d rows, want %d", sz[0], sz[1], h, sz[1])
		}
	}
}

// The frame does not sit flush against the top of the terminal. It is the
// vertical counterpart of the column of breathing room on its left, and the
// only row of the wizard that is deliberately empty.
func TestTheFrameDoesNotTouchTheTopOfTheTerminal(t *testing.T) {
	for _, sz := range [][2]int{{minCols, minRows}, {100, 24}} {
		m := atRuleFor(t, "organization.member.added")
		m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		view := plain(m.viewString())
		top, _, _ := strings.Cut(view, "\n")
		if strings.TrimSpace(top) != "" {
			t.Fatalf("at %d×%d the frame starts on row 0: %q", sz[0], sz[1], top)
		}
	}
}
