package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// tuplesAt is the slice a tuple editor at at is bound to: a rule's own tuples,
// or its iterator's. nil when there is no rule, or no iterator to hold them.
func tuplesAt(s *Session, at Focus) *[]mapping.Tuple {
	r := s.rule(at.Rule)
	if r == nil {
		return nil
	}
	if at.InIter {
		if r.Iterator == nil {
			return nil
		}
		return &r.Iterator.Tuples
	}
	return &r.Tuples
}

// tupleList lists a rule's tuples, or its iterator's.
type tupleList struct {
	s    *Session
	at   Focus
	list *uilist.List
}

func newTupleList(s *Session, at Focus) *tupleList {
	c := &tupleList{s: s, at: at, list: uilist.New()}
	c.sync()
	return c
}

// sync rebuilds the rows from the document. SetItems re-applies an active
// filter to the new rows.
func (c *tupleList) sync() {
	ts := tuplesAt(c.s, c.at)
	if ts == nil {
		c.list.SetItems(nil)
		return
	}
	items := make([]uilist.Item, 0, len(*ts))
	for i, t := range *ts {
		action := t.Action
		if action == "" {
			action = "write"
		}
		// Templates come from the file or the user's event payloads.
		title := style.SanitizeTerminal(fmt.Sprintf("%s %s %s", t.User, t.Relation, t.Object))
		items = append(items, uilist.Item{
			TitleText: title,
			DescText:  style.SanitizeTerminal(action),
			Filter:    title,
			ID:        fmt.Sprintf("tuple-%d", i),
			Index:     i,
		})
	}
	c.list.SetItems(items)
}

func (c *tupleList) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case closedMsg:
		c.sync()
		return c, nil
	case tea.KeyPressMsg:
		return c, c.key(msg)
	}
	return c, c.list.Update(msg)
}

func (c *tupleList) key(k tea.KeyPressMsg) tea.Cmd {
	if c.list.SettingFilter() {
		cmd := c.list.Update(k)
		c.list.ResyncFilter() // A2: enter and d act on the rows the user sees
		return cmd
	}
	ts := tuplesAt(c.s, c.at)
	switch k.String() {
	case "a":
		if ts != nil {
			*ts = append(*ts, mapping.Tuple{})
			c.sync()
			c.s.post(docChangedMsg{})
			c.open(len(*ts) - 1)
		}
		return nil
	case "enter":
		if it, ok := c.list.Selected(); ok {
			c.open(it.Index)
		}
		return nil
	case "d":
		if it, ok := c.list.Selected(); ok && ts != nil && it.Index < len(*ts) {
			*ts = append((*ts)[:it.Index], (*ts)[it.Index+1:]...)
			c.sync()
			c.s.post(docChangedMsg{})
		}
		return nil
	case "esc":
		c.s.post(doneMsg{})
		return nil
	}
	return c.list.Update(k)
}

func (c *tupleList) open(i int) {
	if f := newTupleForm(c.s, c.at, i); f != nil {
		c.s.post(openMsg{c: f})
	}
}

func (c *tupleList) View(width, height int) string {
	if ts := tuplesAt(c.s, c.at); ts == nil || len(*ts) == 0 {
		return emptyState("No tuples yet.", "a", "add one")
	}
	c.list.SetSize(width, height)
	return c.list.View()
}

func (c *tupleList) Keys() []string {
	return []string{"a add", "↵ edit", "d delete", "esc back", "? help"}
}

// heading tells the rule's own tuples from the iterator's: the two lists are
// otherwise drawn identically.
func (c *tupleList) heading() (string, string) {
	if !c.at.InIter {
		return "Tuples", "The relationships this rule writes or deletes."
	}
	sub := "Written once for each item in the list."
	if r := c.s.rule(c.at.Rule); r != nil && r.Iterator != nil && r.Iterator.Source != "" {
		sub = "Written once for each item in " + style.SanitizeTerminal(r.Iterator.Source) + "."
	}
	return "Iterator tuples", sub
}

func (c *tupleList) focus() Focus    { return c.at }
func (c *tupleList) filtering() bool { return c.list.SettingFilter() }

func (c *tupleList) help() (string, string) {
	return "Tuples", "A tuple is one relationship: a user, a relation and an object. " +
		"Wrap an expression in {{ }} to read from the event.\n\n" +
		"  user:alice        member  organization:acme\n" +
		"  group:eng#member  viewer  document:roadmap\n" +
		"    everyone in the group\n" +
		"  user:*            viewer  document:handbook\n" +
		"    every user\n\n" +
		"The relation can be a template too, such as {{ grant.role }}. A tuple's own when skips " +
		"just that tuple, and action delete removes the tuple instead of writing it. A field " +
		"missing from the event fails the whole event; the rule's ? help lists the fallbacks."
}
