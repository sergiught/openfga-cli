package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/ui/field"
)

// SetSample attaches event to r as its sample and, when the event names its
// type, auto-fills the trigger from it.
func SetSample(r *mapping.Rule, label string, event map[string]any) {
	r.Sample = &mapping.Sample{Label: label, Event: event}
	if typ, ok := event["type"].(string); ok && typ != "" {
		applyEventType(r, typ)
	}
}

// applyEventType records the picked event on the rule, auto-filling name and
// when.
//
// Auto-fill must never destroy the user's work, so each field is only replaced
// when it is empty or still holds exactly what the previous pick wrote. That is
// what AutoName and AutoWhen record — without them a second pick could not tell
// "unchanged since I filled it" from "deliberately typed".
func applyEventType(r *mapping.Rule, typ string) {
	when := fmt.Sprintf("input.type == %q", typ)
	if r.Name == "" || r.Name == r.AutoName {
		r.Name = typ
	}
	if r.When == "" || r.When == r.AutoWhen {
		r.When = when
	}
	r.AutoName, r.AutoWhen = typ, when
}

// buildTriggerForm builds the name-and-when form.
func buildTriggerForm() *field.Form {
	return field.NewForm(
		field.New("Rule name", "organization.member.added"),
		field.NewArea("When (expression)", `input.type == "organization.member.added"`),
	)
}

// triggerForm edits a rule's name and when, and attaches its sample event.
type triggerForm struct {
	s         *Session
	at        Focus
	form      *field.Form
	pathField int
}

func newTriggerForm(s *Session, at Focus) *triggerForm {
	c := &triggerForm{s: s, at: at, form: buildTriggerForm()}
	// Reset before SetValues, never after: Reset clears the values along with
	// the errors and the focus.
	c.form.Reset()
	if r := s.rule(at.Rule); r != nil {
		c.form.SetValues([]string{r.Name, r.When})
	}
	return c
}

func (c *triggerForm) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return c, c.key(msg)
	case tea.PasteMsg:
		cmd := c.form.Update(msg)
		c.commit()
		return c, cmd
	case closedMsg:
		switch p := msg.result.(type) {
		case pathPick:
			insertPath(c.form, c.pathField, false, p.Expr)
			c.commit()
		case Picked:
			if r := c.s.rule(c.at.Rule); r != nil {
				SetSample(r, p.Label, p.Event)
				// The auto-fill may have rewritten both fields. SetValues
				// without Reset, so the focus stays where the user left it.
				c.form.SetValues([]string{r.Name, r.When})
				c.s.post(docChangedMsg{})
			}
		}
		return c, nil
	}
	return c, c.form.Update(msg)
}

func (c *triggerForm) key(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		c.commit()
		c.s.post(doneMsg{})
		return nil
	case "ctrl+e":
		// Choosing an event is the one action on this screen that is not
		// typing, so it gets its own key rather than a field.
		c.commit()
		c.s.post(openMsg{c: newEventPick(c.s, c.at, false)})
		return nil
	case "ctrl+p":
		p, why := newPathPicker(c.s, c.at)
		if p == nil {
			c.s.post(toastMsg{text: why, err: true})
			return nil
		}
		c.pathField = c.form.FocusedIndex()
		c.s.post(openMsg{c: p})
		return nil
	}
	cmd := c.form.Update(k)
	// A form submits on enter at its last field, and a submitted form stops
	// accepting keys, so a submit is the exit esc already performs.
	if c.form.Completed() {
		c.commit()
		c.s.post(doneMsg{})
		return nil
	}
	c.commit()
	return cmd
}

// commit writes the form back into the rule, untrimmed as it always was.
func (c *triggerForm) commit() {
	r := c.s.rule(c.at.Rule)
	if r == nil {
		return
	}
	v := c.form.Values()
	if r.Name == v[0] && r.When == v[1] {
		return
	}
	r.Name, r.When = v[0], v[1]
	c.s.post(docChangedMsg{})
}

func (c *triggerForm) View(width, height int) string {
	c.form.SetWidth(width)
	c.form.SetHeight(height)
	return c.form.View()
}

func (c *triggerForm) Keys() []string {
	return []string{"tab next", "ctrl+e pick event", "ctrl+p insert path", "alt+↵ new line", "esc done"}
}

func (c *triggerForm) heading() (string, string) {
	return "Trigger", "Name the rule, and say which events it matches."
}

func (c *triggerForm) typing() bool { return true }

func (c *triggerForm) focus() Focus { return c.at }
