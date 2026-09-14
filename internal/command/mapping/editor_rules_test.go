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
	// Set the stack rather than navigating: this helper means "a wizard sitting
	// on the hub", not "whatever two enters happen to reach this month".
	m.stack = []screen{screenRules}
	return m
}

// addRuleAtTrigger creates a fresh rule and opens it on the trigger screen,
// exactly what "a" used to do before it started routing through the
// payload-kind fork (see TestForkOwnPayloadPasteAppendsARuleOnAccept). Tests
// below that only exercise the rule hub or trigger machinery wire the rule in
// directly rather than walking the fork.
func addRuleAtTrigger(m *wizardModel) {
	m.doc.Rules = append(m.doc.Rules, mapping.Rule{})
	m.ruleIdx = len(m.doc.Rules) - 1
	m.syncRules()
	m.push(screenRule)
	m.openTrigger()
}

// mappingRule builds a rule with n placeholder tuples, for hub-rendering tests.
func mappingRule(name, event string, n int) []mapping.Rule {
	r := mapping.Rule{Name: name, When: `input.type == "` + event + `"`}
	for i := 0; i < n; i++ {
		r.Tuples = append(r.Tuples, mapping.Tuple{User: "user:1", Relation: "member", Object: "organization:1"})
	}
	return []mapping.Rule{r}
}

// TestForkOwnPayloadPasteAppendsARuleOnAccept proves the fix for the
// data-loss bug task 6 could otherwise have introduced: "a" alone must
// append nothing (an abandoned pick must not strand an empty rule on the
// hub), and walking the full path — kind screen, own payload, paste, accept
// — must create exactly one rule with the pasted sample attached, addressed
// by a fresh ruleIdx rather than a stale one, and land on the new rule's hub.
func TestForkOwnPayloadPasteAppendsARuleOnAccept(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	if len(m.doc.Rules) != 0 {
		t.Fatalf("rules = %d, want 0 — picking must not append until accepted", len(m.doc.Rules))
	}
	send(m, key("esc"))
	if len(m.doc.Rules) != 0 {
		t.Fatalf("abandoning the pick appended a rule: %d", len(m.doc.Rules))
	}

	send(m, key("a"), key("down"), key("enter")) // kind screen -> "Another JSON payload" -> paste
	if m.top() != screenEventPaste {
		t.Fatalf("top = %v, want the paste screen", m.top())
	}
	m.paste.SetValue(`{"type": "user.created"}`)
	send(m, key("ctrl+d"))

	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(m.doc.Rules))
	}
	if m.ruleIdx != 0 {
		t.Fatalf("ruleIdx = %d", m.ruleIdx)
	}
	r := m.rule()
	if r == nil || r.Sample == nil || r.Sample.Label != "user.created" {
		t.Fatalf("rule = %+v, want the pasted sample attached", r)
	}
	if m.top() != screenRule {
		t.Fatalf("top = %v, want the new rule's hub", m.top())
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
	addRuleAtTrigger(m) // add a rule
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
	addRuleAtTrigger(m)
	send(m, key("esc"))
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

// The empty hub invites the user to add their first rule. The preview beside it
// used to answer that invitation with the compiler's "rules: must contain at
// least one rule" in red — the wizard reporting, as a fault, that the user had
// not yet done what it was in the middle of asking them to do. It stays quiet
// until there is a rule the message could actually be about — TestRuleHubShows-
// SectionProblems, directly below, is what holds that second half down.
func TestTheEmptyHubDoesNotReportTheEmptyDocumentAsAFault(t *testing.T) {
	m := atRulesHub(t)
	if len(m.doc.Rules) != 0 {
		t.Fatalf("rules = %d, want an empty document", len(m.doc.Rules))
	}
	v := m.viewString()
	if strings.Contains(v, "at least one rule") {
		t.Fatalf("the empty hub scolds the user for being empty:\n%s", v)
	}
}

func TestRuleHubShowsSectionProblems(t *testing.T) {
	m := atRulesHub(t)
	addRuleAtTrigger(m)
	send(m, key("esc"))
	// A brand-new rule has no tuples, which lint reports as blocking.
	if !strings.Contains(m.viewString(), "no tuples") {
		t.Fatalf("the hub should surface the lint problem:\n%s", m.viewString())
	}
}

func TestApplyEventTypeAutoFillsNameAndWhen(t *testing.T) {
	m := atRulesHub(t)
	addRuleAtTrigger(m)
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
	addRuleAtTrigger(m)
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
	addRuleAtTrigger(m)
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
	addRuleAtTrigger(m)
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
	addRuleAtTrigger(m)
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
	addRuleAtTrigger(m)
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
	addRuleAtTrigger(m)
	send(m, key("esc"))
	if m.top() != screenRule {
		t.Fatalf("top = %v, want the rule hub", m.top())
	}
	send(m, key("down"), key("enter")) // cursor -> "Action" row, non-zero cursor
	if m.top() != screenAction {
		t.Fatalf("top = %v, want action; the section picker was likely empty", m.top())
	}
}

// Deleting the last rule leaves ruleIdx pointing one past the end, and every
// screen that reads m.rule() from then on — the hub's preview included — would
// be reading off the end of the slice. deleteRule clamps it; nothing covered
// that, so the clamp could be deleted without turning the suite red.
func TestDeletingTheLastRuleLandsOnTheNewLastOne(t *testing.T) {
	m := atRulesHub(t)
	m.doc.Rules = []mapping.Rule{
		{Name: "first", When: `input.type == "a"`},
		{Name: "second", When: `input.type == "b"`},
		{Name: "third", When: `input.type == "c"`},
	}
	m.syncRules()

	// Delete the last one, the way the hub does: select it, then confirm.
	m.rules.SelectID("rule-2")
	send(m, key("d"))
	if m.top() != screenConfirmDelete {
		t.Fatalf("top = %v, want the delete confirmation", m.top())
	}
	send(m, key("y"))

	if len(m.doc.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(m.doc.Rules))
	}
	if m.ruleIdx != 1 {
		t.Fatalf("ruleIdx = %d, want 1 — it must land on the new last rule", m.ruleIdx)
	}
	r := m.rule()
	if r == nil {
		t.Fatal("rule() = nil after deleting the last rule")
	}
	if r.Name != "second" {
		t.Fatalf("rule = %q, want the new last rule", r.Name)
	}
}

// The wizard's premise is that the preview tracks what you are looking at, so
// the evaluation pane has to follow the highlight around the hub rather than
// staying on whichever rule was opened last.
func TestMovingTheRulesCursorMovesThePreview(t *testing.T) {
	m := atRulesHub(t)
	// The two rules match different events on purpose. refresh() evaluates the
	// whole document against the selected rule's sample, so two rules sharing a
	// `when` and a sample both fire whatever the cursor is on and the pane comes
	// out byte-identical at either position — there would be nothing for an
	// assertion to catch.
	deleted := memberAddedEvent()
	deleted["type"] = "organization.member.deleted"
	m.doc.Rules = []mapping.Rule{
		{
			Name:   "first",
			When:   `input.type == "organization.member.added"`,
			Sample: &mapping.Sample{Label: "member added", Event: memberAddedEvent()},
			Tuples: []mapping.Tuple{{User: "user:a", Relation: "member", Object: "organization:1"}},
		},
		{
			Name:   "second",
			When:   `input.type == "organization.member.deleted"`,
			Sample: &mapping.Sample{Label: "member deleted", Event: deleted},
			Tuples: []mapping.Tuple{{User: "user:b", Relation: "admin", Object: "organization:2"}},
		},
	}
	m.syncRules()
	m.ruleIdx = 0
	m.refresh()

	// What moves with the cursor is which rule fires and which is skipped.
	// "skipped" is vocabulary only the evaluation half has: the YAML half above
	// it lists both rules whatever the cursor is on, so neither rule's own
	// tuples, users or relations can tell the two positions apart.
	if pane := previewOf(m); !strings.Contains(pane, "second: skipped") {
		t.Fatalf("the first rule's sample should leave the second rule skipped:\n%s", pane)
	}

	send(m, key("down"))

	if m.ruleIdx != 1 {
		t.Fatalf("ruleIdx = %d, want 1", m.ruleIdx)
	}
	if r := m.rule(); r == nil || r.Name != "second" {
		t.Fatalf("current rule = %+v", r)
	}
	// The cursor half of the move is covered above; this is the preview half.
	if pane := previewOf(m); !strings.Contains(pane, "first: skipped") {
		t.Fatalf("the preview did not follow the cursor to the second rule:\n%s", pane)
	}
	// ...and this is the half no other test still covers: that the pane is
	// composed into the rendered view at all. Every other preview assertion now
	// calls previewPane directly, to avoid matching chrome, which between them
	// left "paneView never joins the preview in" invisible to the whole suite.
	// "skipped" is safe to grep for here because only the evaluation half
	// writes it.
	if !strings.Contains(m.viewString(), "first: skipped") {
		t.Fatalf("the preview is not composed into the rendered view:\n%s", m.viewString())
	}
}

// The delete modal offers "y delete  n cancel" and nothing else, so enter must
// not be a third, unadvertised way to destroy a rule. Every other screen trains
// enter as "proceed", which is exactly why it is dangerous here: the reflex is
// the wizard's own doing and the delete has no undo.
func TestEnterDoesNotConfirmADelete(t *testing.T) {
	m := atRulesHub(t)
	addRuleAtTrigger(m)
	send(m, key("esc"), key("esc"))
	send(m, key("d"))
	if m.top() != screenConfirmDelete {
		t.Fatalf("top = %v, want the confirm dialog", m.top())
	}

	send(m, key("enter"))
	if len(m.doc.Rules) != 1 {
		t.Fatalf("enter deleted the rule: %d rules left", len(m.doc.Rules))
	}
	if m.top() != screenConfirmDelete {
		t.Fatalf("top = %v, want to still be on the dialog — enter must not answer it", m.top())
	}

	// The advertised keys still work, so refusing enter did not strand anyone.
	send(m, key("y"))
	if len(m.doc.Rules) != 0 {
		t.Fatalf("y did not delete: %d rules left", len(m.doc.Rules))
	}
}

// Three surfaces report the same model warning and used to disagree about it:
// the rules list marked the rule ✓, the rule hub marked the section ✗ — the mark
// it uses for problems that stop a save — and the gate said the mapping
// compiles. A warning is its own severity; reading it off one screen has to
// predict the next.
func TestAModelWarningIsNeitherAPassNorAnError(t *testing.T) {
	m := atRulesHub(t)
	m.doc.Rules = mappingRule("organization.member.added", "organization.member.added", 1)
	m.doc.Rules[0].Tuples[0].Relation = "owner"
	m.index = mapping.IndexModel(testModel())
	m.syncRules()

	if len(mapping.Blocking(m.problems)) != 0 {
		t.Fatalf("this rule is supposed to be savable; problems = %+v", m.problems)
	}

	it, ok := m.rules.Selected()
	if !ok {
		t.Fatal("the hub has no rows")
	}
	row := it.TitleText
	if strings.HasPrefix(row, "✓") {
		t.Fatalf("the rules list passes a rule the model rejects: %q", row)
	}
	if strings.HasPrefix(row, "✗") {
		t.Fatalf("the rules list marks an advisory warning as blocking: %q", row)
	}

	var tuples string
	for _, it := range m.ruleSections() {
		if it.Value == "tuples" {
			tuples = it.Desc
		}
	}
	if strings.HasPrefix(tuples, "✗") {
		t.Fatalf("the rule hub marks an advisory warning as blocking: %q", tuples)
	}
	if !strings.Contains(tuples, "owner") {
		t.Fatalf("the rule hub does not report the warning at all: %q", tuples)
	}
}
