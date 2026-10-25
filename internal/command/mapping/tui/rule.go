package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// ruleSpoke is one rule's hub: six rows, each annotated with a summary and its
// first problem, so the hub doubles as the rule's checklist.
type ruleSpoke struct {
	s   *Session
	at  Focus
	hub *picker.Picker
}

func newRuleSpoke(s *Session, at Focus) *ruleSpoke {
	c := &ruleSpoke{s: s, at: at, hub: picker.New(nil)}
	c.sync()
	return c
}

// sync rebuilds the rows from the document and a fresh lint, keeping the
// cursor.
func (c *ruleSpoke) sync() {
	cursor := c.hub.Cursor()
	c.hub = picker.New(c.rows())
	c.hub.SetCursor(cursor)
}

func (c *ruleSpoke) rows() []picker.Item {
	r := c.s.rule(c.at.Rule)
	if r == nil {
		return nil
	}
	problems := mapping.Lint(c.s.Doc, c.s.Index)
	rows := []struct{ title, section, desc string }{
		{"Trigger", "trigger", triggerSummary(r)},
		{"Action", "action", actionSummary(r)},
		{"Variables", "variables", mapping.Plural(len(r.Variables), "variable")},
		{"Iterator", "iterator", iteratorSummary(r)},
		{"Tuple filters", "filters", mapping.Plural(len(r.Filters), "filter")},
		{"Tuples", "tuples", tuplesSummary(r)},
	}
	items := make([]picker.Item, 0, len(rows))
	for _, row := range rows {
		desc := row.desc
		if p, ok := firstProblem(problems, c.at.Rule, row.section); ok {
			desc = ProblemMark(p) + " " + p.Message
			if row.section == "tuples" && isGuided(r) {
				desc = newRuleGuidance
			}
		}
		// Summaries quote the rule, and a problem message quotes its fault.
		items = append(items, picker.Item{Title: row.title, Desc: style.SanitizeTerminal(desc), Value: row.section})
	}
	return items
}

// newRuleGuidance is what a rule that is still new says in place of mapper's
// "must contain at least one tuple": the rule is not wrong yet, only unfinished.
const newRuleGuidance = "add a tuple or a filter so this rule does something"

// isGuided reports whether r gets guidance instead of the empty-rule error: it
// is new, its editor has not been left, and it still does nothing. Check keeps
// listing the finding throughout.
func isGuided(r *mapping.Rule) bool {
	return r.New && !r.WritesTuples() && len(r.Filters) == 0
}

// firstProblem is rule's first problem in section. The "rule has no tuples
// yet" problem is filed under "tuple"; it surfaces on the Tuples row, which is
// where the user fixes it.
func firstProblem(ps []mapping.Problem, rule int, section string) (mapping.Problem, bool) {
	for _, p := range ps {
		if p.Rule == rule && p.Section == section {
			return p, true
		}
	}
	if section == "tuples" {
		for _, p := range ps {
			if p.Rule == rule && p.Section == "tuple" {
				return p, true
			}
		}
	}
	return mapping.Problem{}, false
}

func triggerSummary(r *mapping.Rule) string {
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

// tuplesSummary counts the rule's own tuples and names the iterator's
// separately when it has any. The recipes for user.updated and
// connection.updated write from the iterator alone, and a bare "0 tuples"
// beside a preview plainly showing a tuple reads as the recipe having failed.
func tuplesSummary(r *mapping.Rule) string {
	iter := 0
	if r.Iterator != nil {
		iter = len(r.Iterator.Tuples)
	}
	if iter == 0 {
		return mapping.Plural(len(r.Tuples), "tuple")
	}
	own := mapping.Plural(len(r.Tuples), "tuple")
	if len(r.Tuples) == 0 {
		own = "none of its own"
	}
	return fmt.Sprintf("%s · %d in the iterator", own, iter)
}

func iteratorSummary(r *mapping.Rule) string {
	if r.Iterator == nil {
		return "none"
	}
	return fmt.Sprintf("%s as %s · %s", r.Iterator.Source, r.Iterator.As, mapping.Plural(len(r.Iterator.Tuples), "tuple"))
}

func (c *ruleSpoke) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case closedMsg:
		c.sync()
	case tea.KeyPressMsg:
		c.key(msg)
	}
	return c, nil
}

func (c *ruleSpoke) key(k tea.KeyPressMsg) {
	switch k.String() {
	case "up", "k":
		c.hub.Move(-1)
	case "down", "j":
		c.hub.Move(1)
	case "esc":
		// Leaving the rule once ends its guidance for the session.
		if r := c.s.rule(c.at.Rule); r != nil {
			r.New = false
		}
		c.s.post(doneMsg{})
	case "enter", " ":
		if c.s.ReadOnly {
			return
		}
		var next component
		switch c.hub.Selected().Value {
		case "trigger":
			next = newTriggerForm(c.s, c.at)
		case "action":
			next = newActionPick(c.s, c.at)
		case "variables":
			next = newVarList(c.s, c.at)
		case "iterator":
			next = newIterHub(c.s, c.at)
		case "filters":
			next = newFilterList(c.s, c.at)
		case "tuples":
			next = newTupleList(c.s, Focus{Rule: c.at.Rule})
		}
		if next != nil {
			c.s.post(openMsg{c: next})
		}
	}
}

func (c *ruleSpoke) View(width, height int) string { return pickerWindow(c.hub, width, height) }

func (c *ruleSpoke) Keys() []string {
	if c.s.ReadOnly {
		return []string{"↑↓ move", "esc back", "? help"}
	}
	return []string{"↑↓ move", "↵ open", "esc back", "? help"}
}

func (c *ruleSpoke) heading() (string, string) { return "Rule", "Pick a part of this rule to edit." }

func (c *ruleSpoke) help() (string, string) {
	return "Rule", "A rule reads: when this event arrives, write or delete these relationships.\n\n" +
		"Trigger names the rule and the events it matches. Tuples are the relationships it " +
		"writes or deletes. Tuple filters name existing tuples the rule owns, to keep them in step " +
		"with its tuples or to remove them; a rule needs tuples, filters, or an iterator's tuples. Action sets " +
		"write or delete for the whole rule. Variables name an expression you use more than once, " +
		"and Iterator repeats its own set of tuples over a list in the event.\n\n" +
		expressionHelp
}

// expressionHelp is the expression reference. It lives on the rule's help
// because the forms that take expressions have none: ? is a character there.
const expressionHelp = "Expressions — a when, a variable, an iterator source, and anything " +
	"inside {{ }} — read the event as input:\n\n" +
	"  input.data.object.user_id\n" +
	"    a field\n" +
	"  input.data.object.plan ?? \"free\"\n" +
	"    a fallback when the field is missing\n" +
	"  input.data.object.org?.id\n" +
	"    nil rather than an error when org is missing\n" +
	"  json_path(input.data, \"a.b.0\")\n" +
	"    a path written as a string\n" +
	"  fga_escape(x)\n" +
	"    x made safe as an id: escapes # : @ * % and spaces\n" +
	"    fga_escape(\"jane@acme.com\") is jane%40acme.com: ids can't hold @ # : * % or spaces, so they are percent-encoded\n" +
	"  lower upper trim split join replace\n" +
	"  hasPrefix hasSuffix len\n" +
	"  == != && || ! in   cond ? a : b\n\n" +
	"A missing field is nil. In a when, nil is false, so the rule or tuple is skipped. Inside " +
	"{{ }} it fails the whole event, as does reading through it (input.a.b when a is missing): " +
	"give it a ?? fallback, or a when that checks it first. A rule's own when sees only input; " +
	"everything else can also read variables.<name>."

func (c *ruleSpoke) focus() Focus { return c.at }

// actionPick sets the action for the whole rule, or leaves it per tuple.
type actionPick struct {
	s    *Session
	at   Focus
	pick *picker.Picker
}

func newActionPick(s *Session, at Focus) *actionPick {
	p := picker.New([]picker.Item{
		{Title: "Per tuple", Desc: "each tuple names its own action", Value: ""},
		{Title: "write", Desc: "every tuple in this rule is written", Value: "write"},
		{Title: "delete", Desc: "every tuple in this rule is deleted", Value: "delete"},
	})
	if r := s.rule(at.Rule); r != nil {
		for i := 0; i < p.Len(); i++ {
			p.SetCursor(i)
			if p.Selected().Value == r.Action {
				break
			}
		}
		// An action the picker does not offer opens on the first item, not on
		// the last one the search tried.
		if p.Selected().Value != r.Action {
			p.SetCursor(0)
		}
	}
	return &actionPick{s: s, at: at, pick: p}
}

func (c *actionPick) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch k.String() {
	case "up", "k":
		c.pick.Move(-1)
	case "down", "j":
		c.pick.Move(1)
	case "esc":
		c.s.post(doneMsg{})
	case "enter", " ":
		// Choosing what is already set changes nothing, so it must not mark
		// the document edited.
		if r := c.s.rule(c.at.Rule); r != nil && r.Action != c.pick.Selected().Value {
			r.Action = c.pick.Selected().Value
			c.s.post(docChangedMsg{})
		}
		c.s.post(doneMsg{})
	}
	return c, nil
}

func (c *actionPick) View(width, height int) string { return pickerWindow(c.pick, width, height) }

func (c *actionPick) Keys() []string {
	return []string{"↑↓ move", "↵ select", "esc back", "? help"}
}

func (c *actionPick) heading() (string, string) {
	return "Rule action", "Write or delete every tuple in this rule?"
}

func (c *actionPick) help() (string, string) {
	return "Action", "write adds the tuples, delete removes them. " +
		"Set it here for the whole rule, or per tuple — never both."
}

func (c *actionPick) focus() Focus { return c.at }
