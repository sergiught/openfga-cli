package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// ruleFork asks for an event and appends a rule for it. A catalog event shows
// its recipe (see recipeView) and appends the recipe's rules; a pasted or
// loaded event appends one rule carrying that sample. It closes with RuleAdded,
// or with doneMsg{} when the user backs out.
//
// The rule is created on accept rather than when the fork is entered:
// otherwise esc on an abandoned pick would strand an empty rule on the hub.
type ruleFork struct {
	s     *Session
	paste bool
	// pick is the event pick, kept so that esc on a recipe comes back to it
	// with its cursor and filter where they were.
	pick     component
	inRecipe bool
}

func newRuleFork(s *Session, paste bool) *ruleFork {
	return &ruleFork{s: s, paste: paste}
}

// Init opens the first question over the fork, which only ever holds the
// answers.
func (c *ruleFork) Init() tea.Cmd {
	if c.paste {
		c.s.post(openMsg{c: newEventPaste(c.s)})
		return nil
	}
	c.pick = newEventPick(c.s, Focus{}, true)
	c.s.post(openMsg{c: c.pick})
	return nil
}

func (c *ruleFork) Update(msg tea.Msg) (component, tea.Cmd) {
	closed, ok := msg.(closedMsg)
	if !ok {
		return c, nil
	}
	switch r := closed.result.(type) {
	case Picked:
		if r.Catalog != nil {
			c.inRecipe = true
			c.s.post(openMsg{c: newRecipeView(c.s, *r.Catalog)})
			return c, nil
		}
		// The recipe refuses a read-only session itself; a pasted or loaded
		// event is appended here, so it is refused here.
		if c.s.ReadOnly {
			c.s.post(doneMsg{})
			return c, nil
		}
		c.s.Doc.Rules = append(c.s.Doc.Rules, mapping.Rule{New: true})
		SetSample(&c.s.Doc.Rules[len(c.s.Doc.Rules)-1], r.Label, r.Event)
		c.s.post(docChangedMsg{})
		c.s.post(doneMsg{result: RuleAdded{Index: len(c.s.Doc.Rules) - 1}})
	case RuleAdded:
		c.s.post(doneMsg{result: r})
	default:
		// esc on a recipe goes back to the pick it came from; esc anywhere else
		// abandons the fork.
		if c.inRecipe && c.pick != nil {
			c.inRecipe = false
			c.s.post(openMsg{c: c.pick})
			return c, nil
		}
		c.s.post(doneMsg{})
	}
	return c, nil
}

// View and Keys are never seen: the fork has a child open from Init until it
// closes.
func (c *ruleFork) View(int, int) string { return "" }
func (c *ruleFork) Keys() []string       { return nil }

// heading names the pick the recipe was opened from while a recipe is on top,
// so the breadcrumb says where esc goes. At any other time a child is on top
// and the fork has no title of its own.
func (c *ruleFork) heading() (string, string) {
	if c.inRecipe {
		return "Pick an event", ""
	}
	return "", ""
}
