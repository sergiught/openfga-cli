package mapping

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// keyRules handles the rules hub: the list of every rule in the document.
func (m *wizardModel) keyRules(k tea.KeyPressMsg) tea.Cmd {
	if m.rules.SettingFilter() {
		return m.rules.Update(k)
	}
	switch k.String() {
	case "a":
		m.addRule()
		return nil
	case "m":
		// Finding #5: the model source used to be unreachable once passed.
		m.push(screenModelSource)
		return nil
	case "enter":
		if it, ok := m.rules.Selected(); ok {
			m.openRule(it.Index)
		}
		return nil
	case "d":
		if len(m.doc.Rules) > 0 {
			if it, ok := m.rules.Selected(); ok {
				m.ruleIdx = it.Index
				m.confirmMsg = fmt.Sprintf("Delete rule %q?", m.doc.Rules[m.ruleIdx].Name)
				m.push(screenConfirmDelete)
			}
		}
		return nil
	case "s":
		// ctrl+s is intercepted globally; s is the hub-local spelling of it, and
		// shares the same guard so the two cannot disagree about when saving is
		// on offer.
		if m.canSave() {
			m.push(screenConfirmSave)
		}
		return nil
	case "esc", "q":
		if len(m.doc.Rules) == 0 {
			m.cancelled = true
			return tea.Quit
		}
		m.push(screenConfirmSave)
		return nil
	}
	cmd := m.rules.Update(k)
	// The preview evaluates the current rule's sample, so moving the cursor has
	// to move the preview with it. Without this the pane keeps showing whichever
	// rule was last opened, which is the opposite of what the highlight says.
	if it, ok := m.rules.Selected(); ok && it.Index != m.ruleIdx {
		m.ruleIdx = it.Index
		m.refresh()
	}
	return cmd
}

// addRule asks what the user is mapping before creating anything. The rule
// itself is only appended once a pick is accepted (see acceptPick) — an
// abandoned pick must not leave an empty rule behind on the hub.
func (m *wizardModel) addRule() {
	m.openPayloadKind()
}

func (m *wizardModel) openRule(i int) {
	m.ruleIdx = i
	m.inIter = false
	m.refresh()
	m.push(screenRule)
}

func (m *wizardModel) deleteRule(i int) {
	if i < 0 || i >= len(m.doc.Rules) {
		return
	}
	m.doc.Rules = append(m.doc.Rules[:i], m.doc.Rules[i+1:]...)
	if m.ruleIdx >= len(m.doc.Rules) {
		m.ruleIdx = len(m.doc.Rules) - 1
	}
	m.syncRules()
}

// syncRules rebuilds the hub list from the document and recomputes the preview.
// Called after every change that could alter a row.
func (m *wizardModel) syncRules() {
	// Lint first. The marks below read m.problems, so building the rows before
	// refreshing them stamped each rule with the verdict on the document as it
	// was before the edit that triggered this sync — a rule stayed ✓ for one
	// more keystroke after it stopped being one.
	m.refresh()

	items := make([]uilist.Item, 0, len(m.doc.Rules))
	for i, r := range m.doc.Rules {
		// Both halves of a row can be auto-filled from the user's own event
		// payload, so both are sanitized before they reach the screen.
		name := style.SanitizeTerminal(r.Name)
		if name == "" {
			name = "(unnamed)"
		}
		mark := "✓"
		if p, ok := m.worstProblem(i); ok {
			mark = problemMark(p)
		}
		n := len(r.Tuples)
		if r.Iterator != nil {
			n += len(r.Iterator.Tuples)
		}
		event := "no event"
		if r.Sample != nil && r.Sample.Label != "" {
			event = style.SanitizeTerminal(r.Sample.Label)
		}
		items = append(items, uilist.Item{
			TitleText: fmt.Sprintf("%s %s", mark, name),
			DescText:  fmt.Sprintf("%s · %s", event, plural(n, "tuple")),
			Filter:    name + " " + event,
			ID:        fmt.Sprintf("rule-%d", i),
			Index:     i,
		})
	}
	m.rules.SetItems(items)
}

// worstProblem returns the problem that should speak for rule i: a blocking one
// if there is any, otherwise the first warning.
func (m *wizardModel) worstProblem(i int) (mapping.Problem, bool) {
	var warning *mapping.Problem
	for _, p := range m.problems {
		if p.Rule != i {
			continue
		}
		if !p.Warning {
			return p, true
		}
		if warning == nil {
			warning = &p
		}
	}
	if warning == nil {
		return mapping.Problem{}, false
	}
	return *warning, true
}

// problemMark is the one place severity becomes a character. A model warning is
// not a failure — the mapping saves, and a model older than the mapping it
// serves is the ordinary case — but it is not a clean bill of health either. It
// borrowed ✗ on the rule screen and ✓ on the list beside it, so the same finding
// read as fatal or as fine depending on which screen the user was looking at.
func problemMark(p mapping.Problem) string {
	if p.Warning {
		return "!"
	}
	return "✗"
}

// --- rule hub ---

// ruleSections builds the six-row menu, each row annotated with a summary and
// its first problem, so the hub doubles as the rule's checklist.
func (m *wizardModel) ruleSections() []picker.Item {
	r := m.rule()
	if r == nil {
		return nil
	}
	rows := []struct{ title, section, desc string }{
		{"Trigger", "trigger", m.triggerSummary(r)},
		{"Action", "action", actionSummary(r)},
		{"Variables", "variables", plural(len(r.Variables), "variable")},
		{"Iterator", "iterator", iteratorSummary(r)},
		{"Tuple filters", "filters", plural(len(r.Filters), "filter")},
		{"Tuples", "tuples", tuplesSummary(r)},
	}
	items := make([]picker.Item, 0, len(rows))
	for _, row := range rows {
		desc := row.desc
		if p, ok := m.firstProblem(row.section); ok {
			desc = problemMark(p) + " " + p.Message
		}
		items = append(items, picker.Item{Title: row.title, Desc: desc, Value: row.section})
	}
	return items
}

func (m *wizardModel) firstProblem(section string) (mapping.Problem, bool) {
	for _, p := range m.problems {
		if p.Rule == m.ruleIdx && p.Section == section {
			return p, true
		}
	}
	// The "rule has no tuples yet" problem is filed under "tuple"; surface it on
	// the Tuples row, which is where the user fixes it.
	if section == "tuples" {
		for _, p := range m.problems {
			if p.Rule == m.ruleIdx && p.Section == "tuple" {
				return p, true
			}
		}
	}
	return mapping.Problem{}, false
}

func (m *wizardModel) triggerSummary(r *mapping.Rule) string {
	if r.When == "" {
		return "no condition"
	}
	return r.When
}

func actionSummary(r *mapping.Rule) string {
	if r.Action == "" {
		return "per tuple"
	}
	return r.Action
}

// tuplesSummary counts the tuples on this row's list — the rule's own — and
// names the iterator's separately when it has any. A rule can write from both,
// and the ready-made recipes for user.created, user.updated and
// connection.updated write from the iterator alone. On those, a bare "0 tuples"
// here sits beside a preview plainly showing a tuple, which reads as the recipe
// having failed to fill the rule in rather than as the tuples living one screen
// over.
func tuplesSummary(r *mapping.Rule) string {
	iter := 0
	if r.Iterator != nil {
		iter = len(r.Iterator.Tuples)
	}
	if iter == 0 {
		return plural(len(r.Tuples), "tuple")
	}
	own := plural(len(r.Tuples), "tuple")
	if len(r.Tuples) == 0 {
		own = "none of its own"
	}
	return fmt.Sprintf("%s · %d in the iterator", own, iter)
}

func iteratorSummary(r *mapping.Rule) string {
	if r.Iterator == nil {
		return "none"
	}
	return fmt.Sprintf("%s as %s · %s", r.Iterator.Source, r.Iterator.As, plural(len(r.Iterator.Tuples), "tuple"))
}

// keyRule handles the rule hub.
func (m *wizardModel) keyRule(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "up", "k":
		m.sections.Move(-1)
	case "down", "j":
		m.sections.Move(1)
	case "esc":
		m.pop()
		m.syncRules()
	case "enter", " ":
		switch m.sections.Selected().Value {
		case "trigger":
			m.openTrigger()
		case "action":
			m.openAction()
		case "variables":
			m.openVariables()
		case "iterator":
			m.openIterator()
		case "filters":
			m.openFilters()
		case "tuples":
			m.inIter = false
			m.openTuples()
		}
	}
	return nil
}

// keyConfirmDelete handles the delete confirmation dialog.
func (m *wizardModel) keyConfirmDelete(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "y":
		m.deleteRule(m.ruleIdx)
		m.pop()
	case "n", "esc":
		m.pop()
	}
	// enter is deliberately not a confirmation. Every other screen trains it as
	// "proceed" — ↵ begin, ↵ choose, ↵ use this, ↵ save — so a user who taps `d`
	// to find out what delete does and then taps enter out of habit would
	// destroy a rule with no undo, having been offered only "y delete  n cancel".
	// keyConfirmSave already refuses enter when the answer carries consequences;
	// an irreversible delete carries more, so here it never answers at all.
	return nil
}
