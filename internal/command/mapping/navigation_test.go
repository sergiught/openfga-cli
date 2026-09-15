package mapping

import (
	"strings"
	"testing"
)

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
