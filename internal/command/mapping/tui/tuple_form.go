package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// tupleField indexes the tuple form's fields, in the order Values returns.
type tupleField int

const (
	fieldUser tupleField = iota
	fieldRelation
	fieldObject
	fieldWhen
	fieldAction
	fieldCondition
	fieldContext
	fieldCount
)

// otherLabel is the escape row every model-backed picker offers, for typing a
// value the model does not know about.
const otherLabel = "Other…"

// tupleActions are the action select's choices. write is mapper's default, so
// it also stands for an unset action (see keepDefault).
var tupleActions = []string{"write", "delete"}

// buildTupleForm builds the form. Placeholders double as examples of the
// template shape, even before a model is loaded.
func buildTupleForm() *field.Form {
	return field.NewForm(
		field.New("User", "user:{{ fga_escape(input.data.object.user.user_id) }}").WithValidate(vUserRef),
		field.New("Relation", "member").WithValidate(vTemplate),
		field.New("Object", "organization:{{ input.data.object.organization.id }}").WithValidate(vObjectRef),
		field.NewArea("When (optional)", `input.data.object.user.user_id != ""`),
		field.NewSelect("Action", tupleActions, 0),
		field.New("Condition (optional)", "in_region").WithValidate(vIdent),
		field.New("Context (key=template, comma separated)", "region={{ input.data.object.region }}").WithValidate(vContextPairs),
	)
}

// tupleForm edits tuple idx of the slice at addresses. It commits on every
// change, so the document always equals the form.
type tupleForm struct {
	s         *Session
	at        Focus
	idx       int
	form      *field.Form
	pick      *picker.Picker // the ctrl+o model picker; nil while closed
	pickField tupleField
	pathField int // the field a ctrl+p path goes into
	ctxKeys   []string
}

func newTupleForm(s *Session, at Focus, i int) *tupleForm {
	ts := tuplesAt(s, at)
	if ts == nil || i < 0 || i >= len(*ts) {
		return nil
	}
	t := (*ts)[i]
	c := &tupleForm{s: s, at: at, idx: i, form: buildTupleForm()}
	// Reset before SetValues, never after: Reset clears the values along with
	// the errors and the focus.
	c.form.Reset()
	c.form.SetValues([]string{t.User, t.Relation, t.Object, t.When, t.Action, t.Condition, joinContext(t.Context)})
	c.syncVisibility()
	return c
}

func (c *tupleForm) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return c, c.key(msg)
	case tea.PasteMsg:
		// The picker owns the keyboard while it is open, so a paste must not
		// edit the form hidden behind it.
		if c.pick != nil {
			return c, nil
		}
		cmd := c.form.Update(msg)
		c.syncVisibility()
		c.commit()
		return c, cmd
	case closedMsg:
		if p, ok := msg.result.(pathPick); ok {
			insertPath(c.form, c.pathField, isTemplateField(tupleField(c.pathField)), p.Expr)
			c.syncVisibility()
			c.commit() // A3: the insert happened outside the key handler
		}
		return c, nil
	}
	return c, c.form.Update(msg)
}

func (c *tupleForm) key(k tea.KeyPressMsg) tea.Cmd {
	if c.pick != nil {
		c.keyPick(k)
		return nil
	}
	switch k.String() {
	case "esc":
		c.commit()
		c.s.post(doneMsg{})
		return nil
	case "ctrl+o":
		c.openPick(tupleField(c.form.FocusedIndex()))
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
	if c.form.Completed() {
		c.commit()
		c.s.post(doneMsg{})
		return nil
	}
	// Action and condition change which fields apply, so re-evaluate on every
	// keystroke; commit as the user types so the preview keeps up.
	c.syncVisibility()
	c.commit()
	return cmd
}

// commit writes the form back into the tuple. A hidden field keeps its value
// so unhiding can restore it, but it does not apply to this tuple.
func (c *tupleForm) commit() {
	ts := tuplesAt(c.s, c.at)
	if ts == nil || c.idx >= len(*ts) {
		return
	}
	vals := c.form.Values()
	vals[fieldWhen] = c.form.Source(int(fieldWhen)) // as written until edited: the area shows it sanitized
	v := func(f tupleField) string {
		if !c.form.Visible(int(f)) {
			return ""
		}
		return strings.TrimSpace(vals[f])
	}
	next := mapping.Tuple{
		Object:    v(fieldObject),
		Relation:  v(fieldRelation),
		User:      v(fieldUser),
		When:      v(fieldWhen),
		Action:    keepDefault(v(fieldAction), (*ts)[c.idx].Action, tupleActions[0]),
		Condition: v(fieldCondition),
		Context:   splitContext(v(fieldContext)),
	}
	// The form trims, and a when from a block scalar ends in a newline:
	// compare it trimmed, or leaving the form would rewrite it.
	had := (*ts)[c.idx]
	had.When = strings.TrimSpace(had.When)
	if sameTuple(had, next) {
		return
	}
	(*ts)[c.idx] = next
	c.s.post(docChangedMsg{})
}

// keepDefault turns an action select's value back into what the file says. The
// default choice stands for both an unset action and one spelled out, so it
// keeps whichever the file had and leaves an unset one unset; any other choice
// is written as chosen.
func keepDefault(chosen, had, def string) string {
	if chosen != def {
		return chosen
	}
	if had == def {
		return def
	}
	return ""
}

// sameTuple compares field by field; an absent context and an empty one are
// the same context.
func sameTuple(a, b mapping.Tuple) bool {
	return a.Object == b.Object && a.Relation == b.Relation && a.User == b.User && a.When == b.When &&
		a.Action == b.Action && a.Condition == b.Condition && slices.Equal(a.Context, b.Context)
}

// syncVisibility hides what cannot apply: a rule-level action makes the
// per-tuple one meaningless, and a delete carries no condition or context.
func (c *tupleForm) syncVisibility() {
	r := c.s.rule(c.at.Rule)
	ruleAction := r != nil && r.Action != ""
	c.form.SetVisible(int(fieldAction), !ruleAction)
	action := strings.TrimSpace(c.form.Values()[fieldAction])
	if ruleAction {
		action = r.Action
	}
	writes := action != "delete"
	c.form.SetVisible(int(fieldCondition), writes)
	cond := strings.TrimSpace(c.form.Values()[fieldCondition])
	c.form.SetVisible(int(fieldContext), writes && cond != "")
	c.ctxKeys = nil
	if cond != "" {
		c.ctxKeys = c.s.Index.ConditionParams(cond)
	}
}

// openPick opens the model picker for a field. Nothing to offer opens nothing,
// and a note says which of the two reasons it is.
func (c *tupleForm) openPick(f tupleField) {
	items := c.pickItems(f)
	if len(items) == 0 {
		note := "This model has nothing to suggest for that field — type it yourself."
		if c.s.Index.Empty() {
			note = "No model loaded, so there is nothing to pick from. Press m on the rules screen to load one."
		}
		c.s.post(toastMsg{text: note})
		return
	}
	c.pickField = f
	c.pick = picker.New(items)
}

func (c *tupleForm) keyPick(k tea.KeyPressMsg) {
	switch k.String() {
	case "up", "k":
		c.pick.Move(-1)
	case "down", "j":
		c.pick.Move(1)
	case "esc":
		c.pick = nil
	case "enter", " ":
		if v := c.pick.Selected().Value; v != "" {
			c.applyPick(c.pickField, v)
		}
		c.pick = nil
		c.syncVisibility()
		c.commit() // A3: the pick edits the form from outside its key handler
	}
}

func (c *tupleForm) View(width, height int) string {
	if c.pick != nil {
		return pickerWindow(c.pick, width, height)
	}
	c.form.SetWidth(width)
	if len(c.ctxKeys) == 0 {
		c.form.SetHeight(height)
		return c.form.View()
	}
	// The list is as long as the model makes it: wrap rather than stretch, and
	// give it no more than a third of the height so the form keeps the rest.
	params := strings.Split(style.Subtitle.Width(width).Render(
		"condition parameters: "+strings.Join(c.ctxKeys, ", ")), "\n")
	budget := min(len(params), max(1, height/3))
	c.form.SetHeight(max(1, height-budget))
	return c.form.View() + "\n" + windowAround(params, 0, budget)
}

// Keys offers ctrl+o only with a model to pick from: advertised without one it
// reads as the one affordance that is broken.
func (c *tupleForm) Keys() []string {
	if c.s.Index.Empty() {
		return []string{"tab next", "ctrl+p insert path", "alt+↵ new line", "esc done"}
	}
	return []string{"tab next", "ctrl+o pick from model", "ctrl+p insert path", "alt+↵ new line", "esc done"}
}

func (c *tupleForm) heading() (string, string) {
	return "Tuple", "Wrap an expression in {{ }} to read from the event."
}

func (c *tupleForm) typing() bool { return true }

func (c *tupleForm) focus() Focus { return c.at }

func (c *tupleForm) pickItems(f tupleField) []picker.Item {
	if c.s.Index.Empty() {
		return nil
	}
	var values []string
	switch f {
	case fieldObject:
		values = c.s.Index.TypeNames()
	case fieldRelation:
		// Before the object is filled in there is no type to narrow by, so offer
		// every relation the model has rather than nothing.
		if typ := typeOf(c.form.Values()[fieldObject]); typ != "" {
			values = c.s.Index.RelationsFor(typ)
		} else {
			values = c.s.Index.AllRelations()
		}
	case fieldUser:
		rel := strings.TrimSpace(c.form.Values()[fieldRelation])
		if typ := typeOf(c.form.Values()[fieldObject]); typ != "" {
			values = c.s.Index.UserTypesFor(typ, rel)
		} else {
			values = c.s.Index.UserTypesForRelation(rel)
		}
	case fieldCondition:
		values = c.s.Index.ConditionNames()
	default:
		return nil
	}
	if len(values) == 0 {
		return nil
	}
	items := make([]picker.Item, 0, len(values)+1)
	for _, v := range values {
		items = append(items, picker.Item{Title: v, Value: v})
	}
	return append(items, picker.Item{
		Title: otherLabel,
		Desc:  "type it yourself",
		Value: "",
	})
}

// applyPick writes the picked value into its field, prefilled per field.
func (c *tupleForm) applyPick(f tupleField, v string) {
	values := c.form.Values()
	switch f {
	case fieldObject:
		values[fieldObject] = v + ":{{  }}"
	case fieldUser:
		values[fieldUser] = userPrefill(v)
	default:
		values[f] = v
	}
	c.form.SetValues(values)
	// Land the cursor where the expression goes, so the user can type — or press
	// ctrl+p — straight away: inside fga_escape's parens where the prefill has
	// them, otherwise between the braces.
	if idx := strings.Index(values[f], "fga_escape()"); idx >= 0 {
		c.form.SetCursor(int(f), idx+len("fga_escape("))
		return
	}
	if idx := strings.Index(values[f], "{{  }}"); idx >= 0 {
		c.form.SetCursor(int(f), idx+3)
	}
}

// userPrefill renders a directly-related user type into its template. A
// userset keeps its relation suffix; a wildcard has no id to fill, so it is
// used literally.
//
// The plain case prefills fga_escape the way the field's own placeholder does:
// subject ids come from the identity provider and routinely carry characters
// FGA does not allow in an id — `auth0|507f…` is the common one — so leaving it
// out here would teach the shape that silently produces bad tuples. The userset
// and object prefills stay bare, matching their placeholders: those ids are
// FGA's own, not the provider's.
func userPrefill(v string) string {
	if strings.HasSuffix(v, ":*") {
		return v
	}
	if typ, rel, ok := strings.Cut(v, "#"); ok {
		return typ + ":{{  }}#" + rel
	}
	return v + ":{{ fga_escape() }}"
}

// typeOf reads the type half of an object template: "organization:{{ x }}" is
// organization. Returns "" when the type itself is templated, in which case
// there is no picker help to offer.
func typeOf(object string) string {
	typ, _, ok := strings.Cut(strings.TrimSpace(object), ":")
	if !ok || strings.Contains(typ, "{{") {
		return ""
	}
	return typ
}

// isTemplateField reports whether a field holds a template (so a picked path
// is inserted as `{{ path }}`) or a bare expression (inserted as-is).
func isTemplateField(f tupleField) bool {
	switch f {
	case fieldObject, fieldUser, fieldRelation, fieldContext:
		return true
	default:
		return false
	}
}

// joinContext and splitContext move context entries between the form's
// one-line field and the structured entries. One line keeps the form flat;
// conditions rarely carry more than two or three parameters.
func joinContext(entries []mapping.ContextEntry) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, e.Key+"="+e.Template)
	}
	return strings.Join(parts, ", ")
}

func splitContext(s string) []mapping.ContextEntry {
	var out []mapping.ContextEntry
	for _, part := range splitOutsideTemplates(s, ',') {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out = append(out, mapping.ContextEntry{Key: strings.TrimSpace(k), Template: strings.TrimSpace(v)})
	}
	return out
}
