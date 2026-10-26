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

// varList lists a rule's variables.
type varList struct {
	s    *Session
	at   Focus
	list *uilist.List
}

func newVarList(s *Session, at Focus) *varList {
	c := &varList{s: s, at: at, list: uilist.New()}
	c.sync()
	return c
}

func (c *varList) sync() {
	r := c.s.rule(c.at.Rule)
	if r == nil {
		c.list.SetItems(nil)
		return
	}
	items := make([]uilist.Item, 0, len(r.Variables))
	for i, v := range r.Variables {
		name, expr := style.SanitizeTerminal(v.Name), style.SanitizeTerminal(v.Expr)
		items = append(items, uilist.Item{
			TitleText: name,
			DescText:  expr,
			Filter:    name + " " + expr,
			ID:        fmt.Sprintf("var-%d", i),
			Index:     i,
		})
	}
	c.list.SetItems(items)
}

func (c *varList) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case closedMsg:
		c.sync()
		return c, nil
	case tea.KeyPressMsg:
		return c, c.key(msg)
	}
	return c, c.list.Update(msg)
}

func (c *varList) key(k tea.KeyPressMsg) tea.Cmd {
	if c.list.SettingFilter() {
		cmd := c.list.Update(k)
		c.list.ResyncFilter() // A2
		return cmd
	}
	r := c.s.rule(c.at.Rule)
	switch k.String() {
	case "a":
		if r != nil {
			r.Variables = append(r.Variables, mapping.Variable{})
			c.sync()
			c.s.post(docChangedMsg{})
			c.open(len(r.Variables) - 1)
		}
		return nil
	case "enter":
		if it, ok := c.list.Selected(); ok {
			c.open(it.Index)
		}
		return nil
	case "d":
		if it, ok := c.list.Selected(); ok && r != nil && it.Index < len(r.Variables) {
			r.Variables = append(r.Variables[:it.Index], r.Variables[it.Index+1:]...)
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

func (c *varList) open(i int) {
	if f := newVarForm(c.s, c.at, i); f != nil {
		c.s.post(openMsg{c: f})
	}
}

func (c *varList) View(width, height int) string {
	c.list.SetSize(width, height)
	return listOrEmpty(c.list, c.s.ReadOnly, "No variables yet.", "a", "add one")
}

func (c *varList) Keys() []string {
	return []string{"a add", "↵ edit", "d delete", "esc back", "? help"}
}

func (c *varList) heading() (string, string) {
	return "Variables", "Name an expression once, then reuse it in tuples."
}

func (c *varList) help() (string, string) {
	return "Variables", "Name an expression once, then reuse it as variables.<name> in tuples, " +
		"their whens, the iterator source and later variables. Order matters: a variable sees " +
		"only the ones declared before it. The rule's own when runs first, so it cannot use them.\n\n" +
		"  org: input.data.object.organization.id\n" +
		"  plan: lower(input.data.object.app_metadata?.plan ?? \"free\")"
}

func (c *varList) focus() Focus    { return c.at }
func (c *varList) filtering() bool { return c.list.SettingFilter() }

// buildVarForm builds the two-field variable form.
func buildVarForm() *field.Form {
	return field.NewForm(
		field.New("Name", "org").WithValidate(vIdent),
		field.NewArea("Expression", "input.data.object.organization.id"),
	)
}

// varForm edits variable idx of the rule at at, committing on every change.
type varForm struct {
	s         *Session
	at        Focus
	idx       int
	form      *field.Form
	pathField int
}

func newVarForm(s *Session, at Focus, i int) *varForm {
	r := s.rule(at.Rule)
	if r == nil || i < 0 || i >= len(r.Variables) {
		return nil
	}
	c := &varForm{s: s, at: at, idx: i, form: buildVarForm()}
	c.form.Reset() // clears values, so it must come first
	c.form.SetValues([]string{r.Variables[i].Name, r.Variables[i].Expr})
	return c
}

func (c *varForm) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return c, c.key(msg)
	case tea.PasteMsg:
		cmd := c.form.Update(msg)
		c.commit()
		return c, cmd
	case closedMsg:
		if p, ok := msg.result.(pathPick); ok {
			// A variable's value is an expression, so the path goes in bare.
			insertPath(c.form, c.pathField, false, p.Expr)
			c.commit()
		}
		return c, nil
	}
	return c, c.form.Update(msg)
}

func (c *varForm) key(k tea.KeyPressMsg) tea.Cmd {
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

func (c *varForm) commit() {
	r := c.s.rule(c.at.Rule)
	if r == nil || c.idx >= len(r.Variables) {
		return
	}
	v := c.form.Values()
	next := mapping.Variable{Name: strings.TrimSpace(v[0]), Expr: strings.TrimSpace(v[1])}
	// Compare trimmed: a block scalar ends in a newline the form trims, and
	// leaving the form must not rewrite what nobody edited.
	if cur := r.Variables[c.idx]; strings.TrimSpace(cur.Name) == next.Name && strings.TrimSpace(cur.Expr) == next.Expr {
		return
	}
	r.Variables[c.idx] = next
	c.s.post(docChangedMsg{})
}

func (c *varForm) View(width, height int) string {
	c.form.SetWidth(width)
	c.form.SetHeight(height)
	return c.form.View()
}

func (c *varForm) Keys() []string {
	return []string{"tab next", "ctrl+p insert path", "alt+↵ new line", "esc done"}
}

func (c *varForm) heading() (string, string) {
	return "Variable", "A name, and the expression it stands for."
}

func (c *varForm) typing() bool { return true }

func (c *varForm) focus() Focus { return c.at }
