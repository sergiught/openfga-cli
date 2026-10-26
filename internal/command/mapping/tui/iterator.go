package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// iterHub is the iterator's own hub. An iterator is a source, a name for the
// element, and a list of tuples written once per item, and each is a row: the
// tuples were once reachable only by a chord from inside the form, which read
// as absent rather than hidden.
type iterHub struct {
	s   *Session
	at  Focus
	hub *picker.Picker
}

func newIterHub(s *Session, at Focus) *iterHub {
	c := &iterHub{s: s, at: at, hub: picker.New(nil)}
	c.sync()
	return c
}

// sync rebuilds the rows from the document, keeping the cursor.
func (c *iterHub) sync() {
	cursor := c.hub.Cursor()
	c.hub = picker.New(c.rows())
	c.hub.SetCursor(cursor)
}

func (c *iterHub) rows() []picker.Item {
	r := c.s.rule(c.at.Rule)
	if r == nil {
		return nil
	}
	src, as, tuples := "not set", "not set", "set a source first"
	if r.Iterator != nil {
		if r.Iterator.Source != "" {
			src = style.SanitizeTerminal(r.Iterator.Source)
		}
		if r.Iterator.As != "" {
			as = style.SanitizeTerminal(r.Iterator.As)
		}
		tuples = mapping.Plural(len(r.Iterator.Tuples), "tuple")
	}
	return []picker.Item{
		{Title: "Source", Desc: src, Value: "source"},
		{Title: "As", Desc: as, Value: "as"},
		{Title: "Tuples", Desc: tuples, Value: "tuples"},
	}
}

func (c *iterHub) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case closedMsg:
		c.sync()
	case tea.KeyPressMsg:
		c.key(msg)
	}
	return c, nil
}

// key handles the hub. Source and As open the same two-field form, focused on
// the row chosen: they are one edit in mapper's YAML.
func (c *iterHub) key(k tea.KeyPressMsg) {
	switch k.String() {
	case "up", "k":
		c.hub.Move(-1)
	case "down", "j":
		c.hub.Move(1)
	case "esc":
		c.s.post(doneMsg{})
	case "enter", " ":
		switch c.hub.Selected().Value {
		case "source":
			c.openForm(0)
		case "as":
			c.openForm(1)
		case "tuples":
			// Tuples hang off the iterator, so there has to be one to hang them
			// on. The row says so before it is chosen; this is the user choosing
			// it anyway.
			if r := c.s.rule(c.at.Rule); r == nil || r.Iterator == nil {
				c.s.post(toastMsg{text: "set a source first", err: true})
				return
			}
			c.s.post(openMsg{c: newTupleList(c.s, Focus{Rule: c.at.Rule, InIter: true})})
		}
	}
}

func (c *iterHub) openForm(focus int) {
	if f := newIterForm(c.s, c.at, focus); f != nil {
		c.s.post(openMsg{c: f})
	}
}

func (c *iterHub) View(width, height int) string { return pickerWindow(c.hub, width, height) }

func (c *iterHub) Keys() []string { return []string{"↑↓ move", "↵ select", "esc back", "? help"} }

// heading is not "repeat this rule's tuples": an iterator carries its own tuple
// list, and the rule's own tuples are written once regardless.
func (c *iterHub) heading() (string, string) {
	return "Iterator", "Write a separate set of tuples for each item in a list."
}

func (c *iterHub) help() (string, string) {
	return "Iterator", "Some events carry a list — a user's identities, the applications a " +
		"connection is enabled for — and one relationship per entry in it.\n\n" +
		"Source is the list. As names one entry, so the tuples below can read from it; it " +
		"cannot be input or variables, which it would hide. Those tuples are written once per " +
		"item; the rule's own are written once.\n\n" +
		"  source: input.data.object.identities\n" +
		"  as: identity\n" +
		"  user:{{ identity.user_id }}  identity  connection:{{ identity.connection }}\n\n" +
		fmt.Sprintf("Two limits bound the list, and no sample is big enough to show them: "+
			"mapper walks at most %d items, and one event may produce up to %d tuples by "+
			"default (the host can change it) — two per item runs out at %d. Either overrun fails the event outright.",
			mapping.MaxIteratorItems, mapping.MaxTuples, mapping.MaxTuples/2)
}

func (c *iterHub) focus() Focus { return c.at }

// buildIterForm builds the source-and-name form.
func buildIterForm() *field.Form {
	return field.NewForm(
		field.NewArea("Source (expression)", "input.data.object.identities"),
		field.New("As", "identity").WithValidate(vIdent),
	)
}

// iterForm edits the iterator's source and element name.
type iterForm struct {
	s         *Session
	at        Focus
	form      *field.Form
	pathField int
}

func newIterForm(s *Session, at Focus, focus int) *iterForm {
	r := s.rule(at.Rule)
	if r == nil {
		return nil
	}
	src, as := "", ""
	if r.Iterator != nil {
		src, as = r.Iterator.Source, r.Iterator.As
	}
	c := &iterForm{s: s, at: at, form: buildIterForm()}
	c.form.Reset() // clears values, so it must come first
	c.form.SetValues([]string{src, as})
	c.form.FocusIndex(focus)
	return c
}

func (c *iterForm) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return c, c.key(msg)
	case tea.PasteMsg:
		cmd := c.form.Update(msg)
		c.commitLive()
		return c, cmd
	case closedMsg:
		if p, ok := msg.result.(pathPick); ok {
			// The source is an expression, so the path goes in bare.
			insertPath(c.form, c.pathField, false, p.Expr)
			c.commitLive()
		}
		return c, nil
	}
	return c, c.form.Update(msg)
}

func (c *iterForm) key(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		c.commit()
		c.s.post(doneMsg{})
		return nil
	case "ctrl+p":
		// The source reads the event, not an item, so the picker is at rule
		// scope.
		p, why := newPathPicker(c.s, Focus{Rule: c.at.Rule})
		if p == nil {
			c.s.post(toastMsg{text: why, err: true})
			return nil
		}
		c.pathField = c.form.FocusedIndex()
		c.s.post(openMsg{c: p})
		return nil
	}
	cmd := c.form.Update(k)
	if c.form.Completed() {
		c.commit()
		c.s.post(doneMsg{})
		return nil
	}
	c.commitLive()
	return cmd
}

// commit writes the form back, dropping the iterator when the source is blank:
// an iterator with no source is the absence of one, and mapper rejects it.
func (c *iterForm) commit() {
	r := c.s.rule(c.at.Rule)
	if r == nil {
		return
	}
	v := c.form.Values()
	v[0] = c.form.Source(0) // as written until edited: the area shows it sanitized
	src, as := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
	switch {
	case src == "":
		if r.Iterator == nil {
			return
		}
		r.Iterator = nil
	// Trimmed, like the form's: a source from a block scalar ends in a newline.
	case r.Iterator != nil && strings.TrimSpace(r.Iterator.Source) == src && r.Iterator.As == as:
		return
	default:
		if r.Iterator == nil {
			r.Iterator = &mapping.Iterator{}
		}
		r.Iterator.Source, r.Iterator.As = src, as
	}
	c.s.post(docChangedMsg{})
}

// commitLive commits as the user types, but never the blank-source drop: that
// deletes the iterator with the tuples hanging off it, and a source is blank
// for a moment whenever it is retyped. Dropping stays an exit-time decision.
func (c *iterForm) commitLive() {
	if strings.TrimSpace(c.form.Values()[0]) != "" {
		c.commit()
	}
}

func (c *iterForm) View(width, height int) string {
	c.form.SetWidth(width)
	c.form.SetHeight(height)
	return c.form.View()
}

func (c *iterForm) Keys() []string {
	return []string{"tab next", "ctrl+p insert path", "alt+↵ new line", "esc done"}
}

func (c *iterForm) heading() (string, string) {
	return "Iterator", "What to walk, and what to call each item."
}

func (c *iterForm) typing() bool { return true }

func (c *iterForm) focus() Focus { return c.at }
