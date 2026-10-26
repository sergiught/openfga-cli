package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// RulesRequest is what the rules hub asks of its host: the actions that leave
// the editor. The hub closes with one and stays where it is, for the host to
// come back to.
type RulesRequest int

const (
	// RequestAdd asks for a new rule, which the host's add-rule fork creates.
	RequestAdd RulesRequest = iota
	// RequestModel asks for the model source.
	RequestModel
	// RequestSave asks for the save dialog.
	RequestSave
	// RequestQuit asks to leave: outright with no rules, or by way of saving.
	RequestQuit
	// RequestTests asks for the tests, which the host shows over the hub.
	RequestTests
)

// rulesHub lists the document's rules, each marked with its worst problem.
type rulesHub struct {
	s    *Session
	list *uilist.List
	// last is the rule the cursor was last seen on, which the preview keeps
	// while a filter matches no rule.
	last int
}

func newRulesHub(s *Session) *rulesHub {
	c := &rulesHub{s: s, list: uilist.New()}
	c.sync()
	return c
}

// OpenRules opens the rules hub, the root of the workspace's rule editing.
func OpenRules(s *Session) *Stack { return newStack(s, newRulesHub(s)) }

// ShowRule opens rule i's hub over the rules hub at the root of st, as enter on
// its row would: for a host that has just created the rule. Whatever was open
// over the rules hub closes.
func ShowRule(st *Stack, i int) {
	h, ok := st.items[0].(*rulesHub)
	if !ok {
		return
	}
	st.items = st.items[:1]
	h.list.ResetFilter()
	h.sync()
	h.list.SelectIndex(i)
	st.items = append(st.items, newRuleSpoke(st.s, Focus{Rule: i}))
}

// sync rebuilds the rows from the document. The marks read lint and every
// sample's check together, so a rule whose sample makes no test is marked here
// and not only when the user tries to save.
func (c *rulesHub) sync() {
	problems := append(mapping.Lint(c.s.Doc, c.s.Index), mapping.CheckSamples(c.s.Ctx, c.s.Doc)...)
	items := make([]uilist.Item, 0, len(c.s.Doc.Rules))
	for i, r := range c.s.Doc.Rules {
		// Both halves of a row can be auto-filled from the user's own event
		// payload, so both are sanitized before they reach the screen.
		name := style.SanitizeTerminal(r.Name)
		if name == "" {
			name = "(unnamed)"
		}
		mark := "✓"
		if p, ok := worstProblem(problems, i); ok {
			mark = ProblemMark(p)
		}
		n := ruleTuples(r)
		event := "no event"
		if r.Sample != nil && r.Sample.Label != "" {
			event = style.SanitizeTerminal(r.Sample.Label)
		}
		items = append(items, uilist.Item{
			TitleText: fmt.Sprintf("%s %s", mark, name),
			DescText:  ruleRowDesc(name, event, mapping.Plural(n, "tuple")),
			Filter:    name + " " + event,
			ID:        fmt.Sprintf("rule-%d", i),
			Index:     i,
		})
	}
	c.list.SetItems(items)
}

// ruleRowDesc is a hub row's second line. A rule auto-named after its event would
// say the event twice, so the line keeps only the count then.
func ruleRowDesc(name, event, tuples string) string {
	if name == event {
		return tuples
	}
	return fmt.Sprintf("%s · %s", event, tuples)
}

// worstProblem is the problem that speaks for rule i: a blocking one if there
// is any, otherwise the first warning.
func worstProblem(ps []mapping.Problem, i int) (mapping.Problem, bool) {
	var warning *mapping.Problem
	for _, p := range ps {
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

func (c *rulesHub) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case closedMsg:
		added, landing := msg.result.(RuleAdded)
		landing = landing && c.s.rule(added.Index) != nil
		if landing {
			c.list.ResetFilter()
		}
		c.sync()
		// A rule fork lands on the rule it made, as enter on its row would.
		if landing {
			c.list.SelectIndex(added.Index)
			c.s.post(openMsg{c: newRuleSpoke(c.s, Focus{Rule: added.Index})})
		}
		return c, nil
	case tea.KeyPressMsg:
		return c, c.key(msg)
	}
	return c, c.list.Update(msg)
}

func (c *rulesHub) key(k tea.KeyPressMsg) tea.Cmd {
	if c.list.SettingFilter() {
		cmd := c.list.Update(k)
		c.list.ResyncFilter() // A2
		return cmd
	}
	switch k.String() {
	case "a":
		if c.s.ReadOnly {
			return nil
		}
		c.s.post(doneMsg{result: RequestAdd})
	case "m":
		c.s.post(doneMsg{result: RequestModel})
	case "s":
		if c.s.ReadOnly {
			return nil
		}
		// ctrl+s is the host's; s is the hub-local spelling of it.
		c.s.post(doneMsg{result: RequestSave})
	case "esc", "q":
		c.s.post(doneMsg{result: RequestQuit})
	case "enter":
		if it, ok := c.list.Selected(); ok {
			c.s.post(openMsg{c: newRuleSpoke(c.s, Focus{Rule: it.Index})})
		}
	case "d":
		if c.s.ReadOnly {
			return nil
		}
		if it, ok := c.list.Selected(); ok && it.Index < len(c.s.Doc.Rules) {
			c.s.post(openMsg{c: newConfirmDelete(c.s, it.Index)})
		}
	case "t":
		// The tests open on the highlighted rule, so the preview keeps it.
		if len(c.s.Doc.Rules) > 0 {
			c.s.post(doneMsg{result: RequestTests})
		}
	default:
		return c.list.Update(k)
	}
	return nil
}

func (c *rulesHub) View(width, height int) string {
	c.list.SetSize(width, height)
	return listOrEmpty(c.list, c.s.ReadOnly, "No rules yet.", "a", "add the first one")
}

// Keys leaves out what an empty hub has nothing to act on: offering open,
// delete or save there sends the user to a dialog whose only content is that
// there was nothing to do. Loading a model is still worth offering.
func (c *rulesHub) Keys() []string {
	if len(c.s.Doc.Rules) == 0 {
		if c.s.ReadOnly {
			return []string{"m model", "esc quit", "? help"}
		}
		return []string{"a add", "m model", "esc quit", "? help"}
	}
	if c.s.ReadOnly {
		return []string{"↵ open", "t tests", "m model", "esc quit", "? help"}
	}
	return []string{"a add", "↵ open", "d delete", "t tests", "m model", "esc quit", "? help"}
}

func (c *rulesHub) heading() (string, string) {
	return "Rules", "Each rule turns one kind of event into tuples."
}

func (c *rulesHub) help() (string, string) {
	return "Rules", "A rule is one kind of event and the tuples it produces. " +
		"Rules are tried in order; every rule whose `when` matches runs.\n\n" +
		"  when: input.type == \"organization.member.added\"\n\n" +
		"t shows the tests a save writes, run against the rules as they are, and adds your own."
}

// focus is the highlighted rule: the preview follows the cursor. A filter that
// matches nothing highlights no row, and the preview stays where it was rather
// than jumping to the first rule.
func (c *rulesHub) focus() Focus {
	if it, ok := c.list.Selected(); ok {
		c.last = it.Index
	}
	return Focus{Rule: c.last}
}

func (c *rulesHub) filtering() bool { return c.list.SettingFilter() }

// confirmDelete asks before a rule is deleted. There is no undo.
type confirmDelete struct {
	s    *Session
	rule int
}

func newConfirmDelete(s *Session, i int) *confirmDelete { return &confirmDelete{s: s, rule: i} }

func (c *confirmDelete) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch k.String() {
	case "y":
		if c.rule >= 0 && c.rule < len(c.s.Doc.Rules) {
			c.s.Doc.Rules = append(c.s.Doc.Rules[:c.rule], c.s.Doc.Rules[c.rule+1:]...)
			c.s.post(docChangedMsg{})
		}
		c.s.post(doneMsg{})
	case "n", "esc", "enter":
		// enter cancels: every other screen trains it as "proceed", so going on
		// out of habit would destroy a rule with no undo. Only y deletes.
		c.s.post(doneMsg{})
	}
	return c, nil
}

func (c *confirmDelete) View(width, height int) string {
	name := ""
	if r := c.s.rule(c.rule); r != nil {
		name = style.SanitizeTerminal(r.Name)
	}
	return windowAround(strings.Split(
		lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("Delete rule %q?", name)), "\n"), 0, height)
}

func (c *confirmDelete) Keys() []string { return []string{"y delete", "n / esc / enter cancel"} }

func (c *confirmDelete) heading() (string, string) { return "Delete rule", "" }

func (c *confirmDelete) isDialog() {}

// ruleTuples counts a rule's own tuples and its iterator's together, one per
// template. It is a floor rather than a total, since an iterator writes its
// tuples once per item.
func ruleTuples(r mapping.Rule) int {
	n := len(r.Tuples)
	if r.Iterator != nil {
		n += len(r.Iterator.Tuples)
	}
	return n
}
