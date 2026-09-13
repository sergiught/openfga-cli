package mapping

import (
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// atRulesHub returns a wizard sitting on the rules hub with no model.
func atRulesHub(t *testing.T) *wizardModel {
	t.Helper()
	m := newTestWizard(t, nil)
	send(m, key("enter"), key("enter"))
	if m.top() != screenRules {
		t.Fatalf("top = %v", m.top())
	}
	return m
}

// mappingRule builds a rule with n placeholder tuples, for hub-rendering tests.
func mappingRule(name, event string, n int) []mapping.Rule {
	r := mapping.Rule{Name: name, When: `input.type == "` + event + `"`}
	for i := 0; i < n; i++ {
		r.Tuples = append(r.Tuples, mapping.Tuple{User: "user:1", Relation: "member", Object: "organization:1"})
	}
	return []mapping.Rule{r}
}

func TestAddRuleOpensTheRuleHubOnTrigger(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d", len(m.doc.Rules))
	}
	if m.top() != screenTrigger {
		t.Fatalf("top = %v, want trigger", m.top())
	}
	if m.ruleIdx != 0 {
		t.Fatalf("ruleIdx = %d", m.ruleIdx)
	}
}

func TestRulesHubListsRulesWithStatus(t *testing.T) {
	m := atRulesHub(t)
	m.doc.Rules = mappingRule("member-added", "organization.member.added", 2)
	m.syncRules()
	v := m.rulesBody()
	if !strings.Contains(v, "member-added") {
		t.Fatalf("hub does not list the rule:\n%s", v)
	}
	if !strings.Contains(v, "2 tuples") {
		t.Fatalf("hub does not show the tuple count:\n%s", v)
	}
}

func TestDeleteRuleConfirms(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))   // add a rule
	send(m, key("esc")) // trigger -> rule hub
	send(m, key("esc")) // rule hub -> rules hub
	send(m, key("d"))
	if m.top() != screenConfirmDelete {
		t.Fatalf("top = %v, want the confirm dialog", m.top())
	}
	send(m, key("n"))
	if len(m.doc.Rules) != 1 {
		t.Fatalf("cancelling must keep the rule: %d", len(m.doc.Rules))
	}
	send(m, key("d"), key("y"))
	if len(m.doc.Rules) != 0 {
		t.Fatalf("rules = %d, want 0", len(m.doc.Rules))
	}
	if m.top() != screenRules {
		t.Fatalf("top = %v", m.top())
	}
}

func TestRuleHubListsSixSections(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("esc"))
	if m.top() != screenRule {
		t.Fatalf("top = %v", m.top())
	}
	v := m.viewString()
	for _, want := range []string{"Trigger", "Action", "Variables", "Iterator", "Tuple filters", "Tuples"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing section %q in:\n%s", want, v)
		}
	}
}

func TestRuleHubShowsSectionProblems(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("esc"))
	// A brand-new rule has no tuples, which lint reports as blocking.
	if !strings.Contains(m.viewString(), "no tuples") {
		t.Fatalf("the hub should surface the lint problem:\n%s", m.viewString())
	}
}

func TestApplyEventTypeAutoFillsNameAndWhen(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	m.applyEventType("organization.member.added")

	r := m.rule()
	if r.Name != "organization.member.added" {
		t.Fatalf("name = %q", r.Name)
	}
	if r.When != `input.type == "organization.member.added"` {
		t.Fatalf("when = %q", r.When)
	}
	if r.AutoName != r.Name || r.AutoWhen != r.When {
		t.Fatalf("bookkeeping not recorded: %+v", r)
	}
}

func TestApplyEventTypeReplacesUntouchedAutoFill(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	m.applyEventType("organization.member.added")
	m.applyEventType("organization.member.deleted")

	r := m.rule()
	if r.Name != "organization.member.deleted" {
		t.Fatalf("name = %q, want the new event", r.Name)
	}
	if r.When != `input.type == "organization.member.deleted"` {
		t.Fatalf("when = %q", r.When)
	}
}

func TestApplyEventTypeNeverClobbersHandEdits(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	m.applyEventType("organization.member.added")

	r := m.rule()
	r.Name = "my-rule"
	r.When = `input.type == "organization.member.added" and input.data.object.user.user_id != ""`
	handWhen := r.When

	m.applyEventType("organization.member.deleted")

	if r.Name != "my-rule" {
		t.Fatalf("hand-edited name was clobbered: %q", r.Name)
	}
	if r.When != handWhen {
		t.Fatalf("hand-edited when was clobbered: %q", r.When)
	}
}

func TestApplyEventTypeFillsEmptyFieldsEvenAfterAHandEdit(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	r := m.rule()
	r.Name = "my-rule"
	// `when` was never set, so it is still fair game.
	m.applyEventType("user.created")
	if r.Name != "my-rule" {
		t.Fatalf("name = %q", r.Name)
	}
	if r.When != `input.type == "user.created"` {
		t.Fatalf("when = %q", r.When)
	}
}

func TestTriggerFormEditsNameAndWhen(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	if m.top() != screenTrigger {
		t.Fatalf("top = %v", m.top())
	}
	m.trigger.SetValues([]string{"hand-named", `input.type == "x"`})
	send(m, key("esc")) // esc commits the form back into the rule
	r := m.rule()
	if r.Name != "hand-named" || r.When != `input.type == "x"` {
		t.Fatalf("rule = %+v", r)
	}
}

func TestEscFromRuleHubReturnsToRulesHubAndSyncs(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	m.applyEventType("user.created")
	send(m, key("esc"), key("esc"))
	if m.top() != screenRules {
		t.Fatalf("top = %v", m.top())
	}
	if !strings.Contains(m.rulesBody(), "user.created") {
		t.Fatalf("the hub was not resynced:\n%s", m.rulesBody())
	}
}

// TestRuleHubEnterOpensSectionWithoutRenderingFirst guards the C1 fix:
// m.sections must be populated by refresh() (reached from every mutation),
// not by rendering. If the picker were instead rebuilt only in the view, the
// first keypress against a never-rendered rule hub would hit an empty picker,
// Selected() would return a zero Item, and the keypress would be silently
// swallowed. Do not "simplify" this test by calling m.viewString() or
// m.View() before the keys below — that would repopulate m.sections as a
// side effect and mask the exact regression this test exists to catch.
func TestRuleHubEnterOpensSectionWithoutRenderingFirst(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("esc"))
	if m.top() != screenRule {
		t.Fatalf("top = %v, want the rule hub", m.top())
	}
	send(m, key("down"), key("enter")) // cursor -> "Action" row, non-zero cursor
	if m.top() != screenAction {
		t.Fatalf("top = %v, want action; the section picker was likely empty", m.top())
	}
}

// The wizard's premise is that the preview tracks what you are looking at, so
// the evaluation pane has to follow the highlight around the hub rather than
// staying on whichever rule was opened last.
func TestMovingTheRulesCursorMovesThePreview(t *testing.T) {
	m := atRulesHub(t)
	m.doc.Rules = []mapping.Rule{
		{
			Name:   "first",
			When:   `input.type == "organization.member.added"`,
			Sample: &mapping.Sample{Label: "member added", Event: memberAddedEvent()},
			Tuples: []mapping.Tuple{{User: "user:a", Relation: "member", Object: "organization:1"}},
		},
		{
			Name:   "second",
			When:   `input.type == "organization.member.added"`,
			Sample: &mapping.Sample{Label: "member added", Event: memberAddedEvent()},
			Tuples: []mapping.Tuple{{User: "user:b", Relation: "admin", Object: "organization:2"}},
		},
	}
	m.syncRules()
	m.ruleIdx = 0
	m.refresh()

	send(m, key("down"))

	if m.ruleIdx != 1 {
		t.Fatalf("ruleIdx = %d, want 1", m.ruleIdx)
	}
	if r := m.rule(); r == nil || r.Name != "second" {
		t.Fatalf("current rule = %+v", r)
	}
}
