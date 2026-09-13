package mapping

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// openTrigger loads the current rule into the trigger form and shows it.
func (m *wizardModel) openTrigger() {
	r := m.rule()
	if r == nil {
		return
	}
	// Reset before SetValues, never after: Reset clears the values along with
	// the errors and the focus. This order is load-bearing in every open* below.
	m.trigger.Reset()
	m.trigger.SetValues([]string{r.Name, r.When})
	m.push(screenTrigger)
}

// commitTrigger writes the form back into the rule. Called on every exit from
// the trigger screen so a half-finished edit is never silently dropped.
func (m *wizardModel) commitTrigger() {
	r := m.rule()
	if r == nil {
		return
	}
	v := m.trigger.Values()
	r.Name, r.When = v[0], v[1]
	m.syncRules()
}

func (m *wizardModel) keyTrigger(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.commitTrigger()
		m.pop()
		return nil
	case "ctrl+e":
		// Choosing an event is the one action on this screen that is not typing,
		// so it gets its own key rather than a field.
		m.commitTrigger()
		m.openEventPick()
		return nil
	case "ctrl+p":
		m.openPathPick(m.trigger, m.trigger.FocusedIndex(), false)
		return nil
	}
	return m.trigger.Update(k)
}

// applyEventType records the picked event on the rule, auto-filling name and
// when.
//
// Auto-fill must never destroy the user's work, so each field is only replaced
// when it is empty or still holds exactly what the previous pick wrote. That is
// what AutoName and AutoWhen record — without them a second pick could not tell
// "unchanged since I filled it" from "deliberately typed".
func (m *wizardModel) applyEventType(typ string) {
	r := m.rule()
	if r == nil {
		return
	}
	when := fmt.Sprintf("input.type == %q", typ)

	if r.Name == "" || r.Name == r.AutoName {
		r.Name = typ
	}
	if r.When == "" || r.When == r.AutoWhen {
		r.When = when
	}
	r.AutoName, r.AutoWhen = typ, when

	m.trigger.SetValues([]string{r.Name, r.When})
	m.syncRules()
}
