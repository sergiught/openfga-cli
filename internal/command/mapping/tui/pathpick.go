package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// pathPick is what the path picker closes with: the expression chosen. The
// form that opened the picker inserts it, because only the form knows its
// caret and whether the field holds a template.
type pathPick struct{ Expr string }

// pathPicker lists the paths in the focused rule's sample, each with an
// example value, and closes with the one chosen.
type pathPicker struct {
	s    *Session
	at   Focus
	list *uilist.List
}

// newPathPicker builds the picker, or says why there is nothing to pick.
func newPathPicker(s *Session, at Focus) (*pathPicker, string) {
	items := pathItems(s, at)
	if len(items) == 0 {
		return nil, noPathsMessage(s, at)
	}
	l := uilist.New()
	l.SetFilterPlaceholder("filter paths")
	l.SetItems(items)
	return &pathPicker{s: s, at: at, list: l}, ""
}

// noPathsMessage says why there is nothing to browse. A missing sample is the
// usual reason; inside an iterator the source must also resolve to a non-empty
// list, and telling a user who has picked an event to go and pick one sends
// them somewhere that cannot fix it.
func noPathsMessage(s *Session, at Focus) string {
	r := s.rule(at.Rule)
	if r == nil || r.Sample == nil {
		return "pick a sample event first (ctrl+e on the Trigger screen) to browse its paths"
	}
	if at.InIter && r.Iterator != nil && r.Iterator.As != "" {
		return fmt.Sprintf("%s does not resolve to a list of objects in the sample event", r.Iterator.Source)
	}
	return "the sample event has no paths to browse"
}

// pathItems lists the sample's paths. Inside an iterator they start at the
// alias, the name an expression there must use. The item is the one mapper's
// own evaluation of the source yields.
func pathItems(s *Session, at Focus) []uilist.Item {
	r := s.rule(at.Rule)
	if r == nil || r.Sample == nil {
		return nil
	}
	root, value := "input", any(r.Sample.Event)
	if at.InIter && r.Iterator != nil && r.Iterator.As != "" {
		item, ok := mapping.IteratorItem(s.Ctx, *r, r.Sample.Event)
		if !ok {
			return nil
		}
		root, value = r.Iterator.As, item
	}
	ps := mapping.Paths(root, value)
	items := make([]uilist.Item, 0, len(ps))
	for i, p := range ps {
		desc := p.Kind
		if p.Example != "" {
			// The example is a value out of the user's own event payload.
			desc = fmt.Sprintf("%s · %s", p.Kind, style.SanitizeTerminal(p.Example))
		}
		if p.IsArray {
			desc = "array · " + desc
		}
		// An iterator's alias comes from the file and starts every path; the ID
		// keeps it as written, to insert.
		shown := style.SanitizeTerminal(p.Expr)
		items = append(items, uilist.Item{TitleText: shown, DescText: desc, Filter: shown, ID: p.Expr, Index: i})
	}
	return items
}

func (c *pathPicker) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, c.list.Update(msg)
	}
	if c.list.SettingFilter() {
		// The runtime's FilterMatchesMsg arrives through Update too, but a host
		// driving keys alone never runs it; resync so enter sees the matches the
		// user is looking at.
		cmd := c.list.Update(k)
		c.list.ResyncFilter()
		return c, cmd
	}
	switch k.String() {
	case "esc":
		c.s.post(doneMsg{})
		return c, nil
	case "enter":
		var result any
		if it, ok := c.list.Selected(); ok {
			result = pathPick{Expr: it.ID}
		}
		c.s.post(doneMsg{result: result})
		return c, nil
	}
	return c, c.list.Update(k)
}

func (c *pathPicker) View(width, height int) string {
	c.list.SetSize(width, height)
	return c.list.View()
}

func (c *pathPicker) Keys() []string { return []string{"/ filter", "↵ insert", "esc cancel"} }

func (c *pathPicker) heading() (string, string) {
	return "Insert a path", "Pick a value from the sample event."
}

func (c *pathPicker) focus() Focus    { return c.at }
func (c *pathPicker) filtering() bool { return c.list.SettingFilter() }

// insertPath splices expr into field idx of f at its caret: wrapped in `{{ }}`
// when the field holds a template and the caret is not already inside one,
// bare otherwise.
func insertPath(f *field.Form, idx int, template bool, expr string) {
	text := expr
	if template && !insideTemplate(f.Values()[idx], f.Cursor(idx)) {
		text = "{{ " + expr + " }}"
	}
	f.Insert(idx, text)
}

// insideTemplate reports whether the caret at pos already sits between a `{{`
// and its closing `}}`. Picking a type from the model (ctrl+o) leaves the caret
// inside an empty `{{  }}`, and the two shortcuts sit next to each other in the
// same footer, so wrapping the path again there is the likely path, not the
// exotic one — it would yield `organization:{{ {{ input.x }} }}`.
func insideTemplate(s string, pos int) bool {
	r := []rune(s)
	if pos > len(r) {
		pos = len(r)
	}
	if pos < 0 {
		return false
	}
	before := string(r[:pos])
	open := strings.LastIndex(before, "{{")
	return open >= 0 && !strings.Contains(before[open:], "}}")
}
