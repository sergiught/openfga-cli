package tui

import (
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func freshRule() *mapping.Document { return &mapping.Document{Rules: []mapping.Rule{{}}} }

func openTrigger(s *Session) component { return newTriggerForm(s, Focus{}) }

func TestApplyEventTypeAutoFillsNameAndWhen(t *testing.T) {
	r := &mapping.Rule{}
	applyEventType(r, "organization.member.added")
	if r.Name != "organization.member.added" || r.When != `input.type == "organization.member.added"` {
		t.Fatalf("rule = %+v", r)
	}
	if r.AutoName != r.Name || r.AutoWhen != r.When {
		t.Fatalf("auto-fill not recorded: %+v", r)
	}
}

func TestApplyEventTypeReplacesUntouchedAutoFill(t *testing.T) {
	r := &mapping.Rule{}
	applyEventType(r, "organization.member.added")
	applyEventType(r, "organization.member.deleted")
	if r.Name != "organization.member.deleted" {
		t.Fatalf("name = %q, want the new event", r.Name)
	}
	if r.When != `input.type == "organization.member.deleted"` {
		t.Fatalf("when = %q", r.When)
	}
}

func TestApplyEventTypeNeverClobbersHandEdits(t *testing.T) {
	r := &mapping.Rule{}
	applyEventType(r, "organization.member.added")
	r.Name = "my-rule"
	r.When = `input.type == "organization.member.added" and input.data.object.user.user_id != ""`
	handWhen := r.When
	applyEventType(r, "organization.member.deleted")
	if r.Name != "my-rule" || r.When != handWhen {
		t.Fatalf("hand edits clobbered: %+v", r)
	}
}

func TestApplyEventTypeFillsEmptyFieldsEvenAfterAHandEdit(t *testing.T) {
	r := &mapping.Rule{Name: "my-rule"} // when was never set, so it is still fair game
	applyEventType(r, "user.created")
	if r.Name != "my-rule" || r.When != `input.type == "user.created"` {
		t.Fatalf("rule = %+v", r)
	}
}

func TestTriggerFormEditsNameAndWhen(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	topAs[*triggerForm](t, h).form.SetValues([]string{"hand-named", `input.type == "x"`})
	h.press("esc")
	if r := h.s.Doc.Rules[0]; r.Name != "hand-named" || r.When != `input.type == "x"` || !h.closed {
		t.Fatalf("rule = %+v, closed = %v", r, h.closed)
	}
}

func TestTypingATriggerCommitsAsYouType(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	h.typeText("myrule")
	if got := h.s.Doc.Rules[0].Name; got != "myrule" || h.changes == 0 {
		t.Fatalf("name = %q, changes = %d", got, h.changes)
	}
}

// When is an expression, so a picked path goes in bare.
func TestAPathInTheWhenFieldIsBare(t *testing.T) {
	d := freshRule()
	d.Rules[0].Sample = &mapping.Sample{Label: "organization.member.added", Event: memberAddedEvent()}
	h := newHarness(t, d, nil, openTrigger)
	h.press("tab", "ctrl+p", "/")
	h.typeText("organization.id")
	h.press("enter", "enter")
	if got := h.s.Doc.Rules[0].When; got != "input.data.object.organization.id" {
		t.Fatalf("when = %q", got)
	}
}

func TestTabbingThroughATriggerChangesNothing(t *testing.T) {
	assertOnlyEditsChangeTheDocument(t, func() *mapping.Document {
		return &mapping.Document{Rules: []mapping.Rule{{Name: "my-rule", When: `input.type == "x"`}}}
	}, openTrigger)
}

// enter on the trigger's last field commits it and leaves the form, rather
// than leaving a submitted form on screen that takes no more keys.
func TestEnterOnTheLastFieldCommitsAndLeavesTheForm(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	h.typeText("myrule")
	h.press("tab")
	h.typeText("true")
	h.press("enter")
	if r := h.s.Doc.Rules[0]; !h.closed || r.Name != "myrule" || r.When != "true" {
		t.Fatalf("closed = %v, rule = %+v; want the trigger committed and the form gone", h.closed, r)
	}
}
