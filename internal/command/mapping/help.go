package mapping

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

// helpFor returns the concept behind a screen. Only screens whose focused
// widget does not take typed input get an entry: a form must not steal a
// literal ? from someone typing a template, so its concept is reachable from
// the section list one level up instead. A filterable list also takes typed
// input, but only once its filter is open — that entry is conditional, and
// filtering() below is what guards it.
//
// Kept in sync with filtering() below: every screen that answers here and
// backs onto a filterable list must also be listed there, or a filter box on
// that screen would lose its ? to this overlay instead.
func helpFor(s screen) (title, body string, ok bool) {
	switch s {
	case screenRules:
		return "Rules", "A rule is one kind of event and the tuples it produces. " +
			"Rules are tried in order; every rule whose `when` matches runs.\n\n" +
			"  when: input.type == \"organization.member.added\"", true
	case screenTuples:
		return "Tuples", "A tuple is one relationship: who, what, which thing. " +
			"Wrap an expression in {{ }} to read from the event.\n\n" +
			"  user:alice  member  organization:acme", true
	case screenVariables:
		return "Variables", "Name an expression once, then reuse it in tuples. " +
			"Order matters: a variable sees only the ones declared before it.\n\n" +
			"  org: input.data.object.organization.id", true
	case screenFilters:
		return "Tuple filters", "A filter describes existing tuples this rule owns, so " +
			"mapper can remove the ones the event says are gone. " +
			"The object always names a type; user and relation may be blank.\n\n" +
			"  object: organization:  user: user:alice  action: delete", true
	case screenAction:
		return "Action", "write adds the tuples, delete removes them. " +
			"Set it here for the whole rule, or per tuple — never both.", true
	case screenRule:
		return "Rule", "A rule reads: when this event arrives, write or delete these relationships.\n\n" +
			"Trigger names the rule and the events it matches. Tuples are the relationships it " +
			"writes — or Tuple filters, which remove existing ones the event says are gone; a rule " +
			"needs at least one of the two. Action sets write or delete for the whole rule. " +
			"Variables name an expression you use more than once, and Iterator repeats its own set " +
			"of tuples over a list in the event.", true
	case screenModelSource:
		return "Authorization model", "Loading your model lets the wizard check that every type and " +
			"relation a tuple names exists, and offer them with ^o while you type.\n\n" +
			"It is optional. The mapping is just as valid without one, and a mismatch is reported " +
			"but never blocks a save — your model may simply be older than the mapping.", true
	case screenEventPick:
		return "Events", fmt.Sprintf("Pick the event you want to map — %d of the %d come with a "+
			"ready-made mapping, and the rest explain why they need none. Either way you "+
			"end up with a rule you can edit.", mappedCount(), len(auth0.Catalog())), true
	}
	return "", "", false
}

// filtering reports whether the list on the current screen is taking filter
// input. ? is a printable character there, so it belongs to the filter box
// rather than to the help overlay — the same reason a form screen gets no
// helpFor entry at all.
//
// Every screen helpFor answers that hosts a filterable list needs a case here.
// The rule hub and the model source are answered there and absent here on
// purpose: both are pickers with no filter box, so nothing on them can be
// taking ? as text.
func (m *wizardModel) filtering() bool {
	switch m.top() {
	case screenRules:
		return m.rules.SettingFilter()
	case screenTuples:
		return m.tupleList.SettingFilter()
	case screenVariables:
		return m.varList.SettingFilter()
	case screenFilters:
		return m.filterList.SettingFilter()
	case screenEventPick:
		return m.events.SettingFilter()
	}
	return false
}

// keyHelp dismisses the overlay on any key. screenHelp is a leaf — nothing it
// handles pushes another screen — so popping always returns to the screen the
// overlay was opened from.
func (m *wizardModel) keyHelp(tea.KeyPressMsg) tea.Cmd {
	m.pop()
	return nil
}
