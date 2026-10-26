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

// maxFilters caps tuple filters per rule, mirroring mapper's own limit:
// language/parser.go rejects a rule with more than three with "exceeds
// maximum of 3 filters". Capping here turns a validation failure into a
// message the user sees while they are still editing.
const maxFilters = 3

// filterAction names what a filter does, spelling out the default rather than
// leaving the row blank: an unset action is a patch, and a blank row reads like
// a field the user forgot to fill in.
func filterAction(f mapping.TupleFilter) string {
	if f.Action == "" {
		return "patch"
	}
	return f.Action
}

// filterTitle renders the set pattern, using * for the parts left open — which
// is what the filter actually means.
func filterTitle(f mapping.TupleFilter) string {
	part := func(s string) string {
		if s == "" {
			return "*"
		}
		return s
	}
	return fmt.Sprintf("%s %s %s", part(f.User), part(f.Relation), part(f.Object))
}

// filterList lists a rule's tuple filters.
type filterList struct {
	s    *Session
	at   Focus
	list *uilist.List
}

func newFilterList(s *Session, at Focus) *filterList {
	c := &filterList{s: s, at: at, list: uilist.New()}
	c.sync()
	return c
}

func (c *filterList) sync() {
	r := c.s.rule(c.at.Rule)
	if r == nil {
		c.list.SetItems(nil)
		return
	}
	items := make([]uilist.Item, 0, len(r.Filters))
	for i, f := range r.Filters {
		// The object leads: filters that differ only in object stay apart
		// even when the description is cut to the row width.
		line := style.SanitizeTerminal(filterTitle(f))
		object := f.Object
		if object == "" {
			object = "*"
		}
		items = append(items, uilist.Item{
			TitleText: style.SanitizeTerminal(object),
			DescText:  style.SanitizeTerminal(line + "  " + filterAction(f)),
			Filter:    line,
			ID:        fmt.Sprintf("filter-%d", i),
			Index:     i,
		})
	}
	c.list.SetItems(items)
}

func (c *filterList) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case closedMsg:
		c.sync()
		return c, nil
	case tea.KeyPressMsg:
		return c, c.key(msg)
	}
	return c, c.list.Update(msg)
}

func (c *filterList) key(k tea.KeyPressMsg) tea.Cmd {
	if c.list.SettingFilter() {
		cmd := c.list.Update(k)
		c.list.ResyncFilter() // A2
		return cmd
	}
	r := c.s.rule(c.at.Rule)
	if r == nil {
		if k.String() == "esc" {
			c.s.post(doneMsg{})
		}
		return nil
	}
	switch k.String() {
	case "a":
		if len(r.Filters) >= maxFilters {
			c.s.post(toastMsg{text: fmt.Sprintf("a rule takes at most %d tuple filters", maxFilters), err: true})
			return nil
		}
		// The action a new filter should have is the one the rule can support.
		// A rule that writes tuples has a desired state, so the filter can
		// reconcile against it — mapper's own default, and the one that keeps a
		// list in step without a second rule. A rule that writes none has
		// nothing to reconcile, and a patch there fails every event, so the only
		// filter worth starting from is a delete.
		action := ""
		if !r.WritesTuples() {
			action = "delete"
		}
		r.Filters = append(r.Filters, mapping.TupleFilter{Action: action})
		c.sync()
		c.s.post(docChangedMsg{})
		c.open(len(r.Filters) - 1)
		return nil
	case "enter":
		if it, ok := c.list.Selected(); ok {
			c.open(it.Index)
		}
		return nil
	case "d":
		if it, ok := c.list.Selected(); ok && it.Index < len(r.Filters) {
			r.Filters = append(r.Filters[:it.Index], r.Filters[it.Index+1:]...)
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

func (c *filterList) open(i int) {
	if f := newFilterForm(c.s, c.at, i); f != nil {
		c.s.post(openMsg{c: f})
	}
}

func (c *filterList) View(width, height int) string {
	c.list.SetSize(width, height)
	return listOrEmpty(c.list, c.s.ReadOnly, "No tuple filters yet.", "a", "add one")
}

func (c *filterList) Keys() []string {
	return []string{"a add", "↵ edit", "d delete", "esc back", "? help"}
}

// heading counts the filters against the cap. Three per rule is mapper's
// limit and nothing else on screen says so; the count appears once a filter
// exists, because on an empty list it is a budget for something the user has
// not started spending, and the empty state already has a sentence to say.
func (c *filterList) heading() (string, string) {
	sub := "Reconcile what this rule writes, or delete outright."
	if r := c.s.rule(c.at.Rule); r != nil && len(r.Filters) > 0 {
		sub = fmt.Sprintf("%d of %d used. %s", len(r.Filters), maxFilters, sub)
	}
	return "Tuple filters", sub
}

func (c *filterList) help() (string, string) {
	return "Tuple filters", "A filter names existing tuples this rule owns. The object always " +
		"names a type, alone or with an id; user and relation may be blank, and a blank one " +
		"matches anything.\n\n" +
		"patch, the default, keeps a list in step: apply reads every tuple the filter matches, " +
		"writes the rule's tuples that are new, and deletes the matches the rule's tuples leave " +
		"out. Re-writing the whole list makes the removals happen by themselves.\n\n" +
		"delete removes every match and needs no tuples, which suits a rule that only cleans up. " +
		"A patch there would fail the event: mapper reads an empty list as a mistake, not as " +
		"\"delete everything\".\n\n" +
		"Key a filter on the object to reach everything on it, or on the user to reach what the " +
		"event cannot name, such as every organization a deleted user belonged to:\n\n" +
		"  object: organization:{{ input.data.object.id }}\n" +
		"  action: delete\n\n" +
		"  user:   user:{{ fga_escape(input.data.object.user_id) }}\n" +
		"  object: organization:\n" +
		"  action: delete\n\n" +
		fmt.Sprintf("A rule has at most %d filters. A filter that fixes all three fields names one "+
			"tuple rather than a range: use a tuple with action delete for that.", maxFilters)
}

func (c *filterList) focus() Focus    { return c.at }
func (c *filterList) filtering() bool { return c.list.SettingFilter() }

// filterActions are the action select's choices. patch is mapper's default, so
// it also stands for an unset action (see keepDefault).
var filterActions = []string{"patch", "delete"}

// buildFilterForm builds the four-field filter form.
func buildFilterForm() *field.Form {
	return field.NewForm(
		field.New("User (optional)", "user:{{ fga_escape(input.data.object.user.user_id) }}").WithValidate(vUserRef),
		field.New("Relation (optional)", "member").WithValidate(vTemplate),
		// Not optional, unlike the two above it: mapper rejects a filter with no
		// object and Lint blocks the save on one.
		field.New("Object", "organization:{{ input.data.object.organization.id }}").WithValidate(vFilterObject),
		field.NewSelect("Action", filterActions, 0),
	)
}

// filterForm edits filter idx of the rule at at, committing on every change.
type filterForm struct {
	s         *Session
	at        Focus
	idx       int
	form      *field.Form
	pathField int
}

func newFilterForm(s *Session, at Focus, i int) *filterForm {
	r := s.rule(at.Rule)
	if r == nil || i < 0 || i >= len(r.Filters) {
		return nil
	}
	f := r.Filters[i]
	c := &filterForm{s: s, at: at, idx: i, form: buildFilterForm()}
	c.form.Reset() // clears values, so it must come first
	c.form.SetValues([]string{f.User, f.Relation, f.Object, f.Action})
	return c
}

func (c *filterForm) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return c, c.key(msg)
	case tea.PasteMsg:
		cmd := c.form.Update(msg)
		c.commit()
		return c, cmd
	case closedMsg:
		if p, ok := msg.result.(pathPick); ok {
			// User, relation and object are templates; the action is not.
			insertPath(c.form, c.pathField, c.pathField < 3, p.Expr)
			c.commit()
		}
		return c, nil
	}
	return c, c.form.Update(msg)
}

func (c *filterForm) key(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		c.commit()
		c.s.post(doneMsg{})
		return nil
	case "ctrl+p":
		p, why := newPathPicker(c.s, c.at)
		if p == nil {
			c.s.post(toastMsg{text: why, err: true})
			return nil
		}
		c.pathField = c.form.FocusedIndex()
		c.s.post(openMsg{c: p})
		return nil
	}
	cmd := c.form.Update(k)
	c.commit()
	if c.form.Completed() {
		c.s.post(doneMsg{})
		return nil
	}
	return cmd
}

func (c *filterForm) commit() {
	r := c.s.rule(c.at.Rule)
	if r == nil || c.idx >= len(r.Filters) {
		return
	}
	v := c.form.Values()
	next := mapping.TupleFilter{
		User:     strings.TrimSpace(v[0]),
		Relation: strings.TrimSpace(v[1]),
		Object:   strings.TrimSpace(v[2]),
		Action:   keepDefault(v[3], r.Filters[c.idx].Action, filterActions[0]),
	}
	if r.Filters[c.idx] == next {
		return
	}
	r.Filters[c.idx] = next
	c.s.post(docChangedMsg{})
}

func (c *filterForm) View(width, height int) string {
	c.form.SetWidth(width)
	c.form.SetHeight(height)
	return c.form.View()
}

func (c *filterForm) Keys() []string { return []string{"tab next", "ctrl+p insert path", "esc done"} }

func (c *filterForm) heading() (string, string) {
	return "Tuple filter", "Blank user or relation matches anything; object needs a type."
}

func (c *filterForm) typing() bool { return true }

func (c *filterForm) focus() Focus { return c.at }
