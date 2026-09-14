package mapping

import tea "charm.land/bubbletea/v2"

// helpFor returns the concept behind a screen. Only screens whose focused
// widget is not a text field get an entry: a form must not steal a literal ?
// from someone typing a template, so their concepts are reachable from the
// section list one level up instead.
//
// Kept in sync with filtering() below: every screen that answers here and
// backs onto a filterable list must also be listed there, or a filter box on
// that screen would lose its ? to this overlay instead.
func helpFor(s screen) (title, body string, ok bool) {
	switch s {
	case screenRules:
		return "Rules", "A rule is one kind of event and the tuples it produces.\n" +
			"Rules are tried in order; every rule whose `when` matches runs.\n\n" +
			"  when: input.type == \"organization.member.added\"", true
	case screenTuples:
		return "Tuples", "A tuple is one relationship: who, what, which thing.\n" +
			"Wrap an expression in {{ }} to read from the event.\n\n" +
			"  user:alice  member  organization:acme", true
	case screenVariables:
		return "Variables", "Name an expression once, then reuse it in tuples.\n" +
			"Order matters: a variable sees only the ones declared before it.\n\n" +
			"  org: input.data.object.organization.id", true
	case screenFilters:
		return "Tuple filters", "A filter describes existing tuples this rule owns, so\n" +
			"mapper can remove the ones the event says are gone.\n" +
			"The object always names a type; user and relation may be blank.\n\n" +
			"  object: organization:  user: user:alice  action: delete", true
	case screenAction:
		return "Action", "write adds the tuples, delete removes them.\n" +
			"Set it here for the whole rule, or per tuple — never both.", true
	case screenEventPick:
		return "Events", "Pick the event you want to map. Twelve of them come with a\n" +
			"worked mapping; the rest explain why they need none.", true
	}
	return "", "", false
}

// filtering reports whether the list on the current screen is taking filter
// input. ? is a printable character there, so it belongs to the filter box
// rather than to the help overlay — the same reason a form screen gets no
// helpFor entry at all.
//
// Kept in sync with helpFor above: every case here mirrors a screen answered
// there, so the two cannot drift without both being visibly incomplete.
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
