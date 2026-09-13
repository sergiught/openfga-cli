package mapping

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// tupleField indexes the tuple form's fields. The order here is the form's
// field order and the order Values() returns.
type tupleField int

const (
	fieldObject tupleField = iota
	fieldRelation
	fieldUser
	fieldWhen
	fieldAction
	fieldCondition
	fieldContext
	fieldCount
)

// otherLabel is the escape row every model-backed picker offers, for typing a
// value the model does not know about.
const otherLabel = "Other…"

// newTupleForm builds the tuple form. Placeholders double as examples: they
// show the template shape even before a model is loaded.
func newTupleForm() *field.Form {
	return field.NewForm(
		field.New("Object", "organization:{{ input.data.object.organization.id }}"),
		field.New("Relation", "member"),
		field.New("User", "user:{{ fga_escape(input.data.object.user.user_id) }}"),
		field.New("When (optional)", `input.data.object.user.user_id != ""`),
		field.New("Action (optional)", "write | delete"),
		field.New("Condition (optional)", "in_region"),
		field.New("Context (key=template, comma separated)", "region={{ input.data.object.region }}"),
	)
}

// --- tuple list ---

func (m *wizardModel) openTuples() {
	m.syncTuples()
	m.push(screenTuples)
}

func (m *wizardModel) syncTuples() {
	ts := m.tuples()
	if ts == nil {
		m.tupleList.SetItems(nil)
		return
	}
	items := make([]uilist.Item, 0, len(*ts))
	for i, t := range *ts {
		action := t.Action
		if action == "" {
			action = "write"
		}
		items = append(items, uilist.Item{
			TitleText: fmt.Sprintf("%s %s %s", t.User, t.Relation, t.Object),
			DescText:  action,
			Filter:    t.User + " " + t.Relation + " " + t.Object,
			ID:        fmt.Sprintf("tuple-%d", i),
			Index:     i,
		})
	}
	m.tupleList.SetItems(items)
	m.syncRules()
}

func (m *wizardModel) keyTuples(k tea.KeyPressMsg) tea.Cmd {
	if m.tupleList.SettingFilter() {
		return m.tupleList.Update(k)
	}
	ts := m.tuples()
	switch k.String() {
	case "a":
		if ts != nil {
			*ts = append(*ts, mapping.Tuple{})
			m.openTuple(len(*ts) - 1)
		}
		return nil
	case "enter":
		if it, ok := m.tupleList.Selected(); ok {
			m.openTuple(it.Index)
		}
		return nil
	case "d":
		if it, ok := m.tupleList.Selected(); ok && ts != nil && it.Index < len(*ts) {
			*ts = append((*ts)[:it.Index], (*ts)[it.Index+1:]...)
			m.syncTuples()
		}
		return nil
	case "esc":
		m.pop()
		m.syncRules()
		return nil
	}
	return m.tupleList.Update(k)
}

// openTuple loads tuple i into the form and shows it.
func (m *wizardModel) openTuple(i int) {
	ts := m.tuples()
	if ts == nil || i < 0 || i >= len(*ts) {
		return
	}
	m.tupleIdx = i
	t := (*ts)[i]
	// Reset before SetValues, never after: Reset clears the values along with
	// the errors and the focus.
	m.tupleForm.Reset()
	m.tupleForm.SetValues([]string{
		t.Object,
		t.Relation,
		t.User,
		t.When,
		t.Action,
		t.Condition,
		joinContext(t.Context),
	})
	m.syncTupleVisibility()
	m.push(screenTuple)
}

// commitTuple writes the form back into the tuple. Called on every exit, so
// nothing typed is ever lost to a stray esc.
func (m *wizardModel) commitTuple() {
	ts := m.tuples()
	if ts == nil || m.tupleIdx < 0 || m.tupleIdx >= len(*ts) {
		return
	}
	v := m.tupleForm.Values()
	(*ts)[m.tupleIdx] = mapping.Tuple{
		Object:    strings.TrimSpace(v[fieldObject]),
		Relation:  strings.TrimSpace(v[fieldRelation]),
		User:      strings.TrimSpace(v[fieldUser]),
		When:      strings.TrimSpace(v[fieldWhen]),
		Action:    strings.TrimSpace(v[fieldAction]),
		Condition: strings.TrimSpace(v[fieldCondition]),
		Context:   splitContext(v[fieldContext]),
	}
	m.syncTuples()
}

func (m *wizardModel) keyTuple(k tea.KeyPressMsg) tea.Cmd {
	// The overlay picker owns the keyboard while it is open.
	if m.fieldPick != nil {
		return m.keyFieldPick(k)
	}
	switch k.String() {
	case "esc":
		m.commitTuple()
		m.pop()
		return nil
	case "ctrl+o":
		m.openFieldPick(tupleField(m.tupleForm.FocusedIndex()))
		return nil
	case "ctrl+p":
		m.openPathPick(m.tupleForm, m.tupleForm.FocusedIndex(), isTemplateField(tupleField(m.tupleForm.FocusedIndex())))
		return nil
	}
	cmd := m.tupleForm.Update(k)
	// Action and condition changes make other fields' visibility change, so
	// re-evaluate every keystroke rather than only on blur.
	m.syncTupleVisibility()
	return cmd
}

// syncTupleVisibility hides fields that cannot apply: a rule-level action
// makes the per-tuple one meaningless, and a delete carries no condition or
// context.
func (m *wizardModel) syncTupleVisibility() {
	r := m.rule()
	ruleAction := r != nil && r.Action != ""
	m.tupleForm.SetVisible(int(fieldAction), !ruleAction)

	action := strings.TrimSpace(m.tupleForm.Values()[fieldAction])
	if ruleAction {
		action = r.Action
	}
	writes := action != "delete"
	m.tupleForm.SetVisible(int(fieldCondition), writes)

	cond := strings.TrimSpace(m.tupleForm.Values()[fieldCondition])
	m.tupleForm.SetVisible(int(fieldContext), writes && cond != "")
	m.ctxKeys = m.contextKeys()
}

// contextKeys returns the condition's parameter names, for the hint shown
// under the context field.
func (m *wizardModel) contextKeys() []string {
	cond := strings.TrimSpace(m.tupleForm.Values()[fieldCondition])
	if cond == "" {
		return nil
	}
	return m.index.ConditionParams(cond)
}

// openFieldPick opens the overlay picker for a model-backed field. No model —
// or nothing the model says about a field — means nothing opens: the user
// just types.
func (m *wizardModel) openFieldPick(f tupleField) {
	items := m.tupleFieldItems(f)
	if len(items) == 0 {
		return
	}
	m.pickField = f
	m.fieldPick = picker.New(items)
}

func (m *wizardModel) tupleFieldItems(f tupleField) []picker.Item {
	if m.index.Empty() {
		return nil
	}
	var values []string
	switch f {
	case fieldObject:
		values = m.index.TypeNames()
	case fieldRelation:
		values = m.index.RelationsFor(typeOf(m.tupleForm.Values()[fieldObject]))
	case fieldUser:
		values = m.index.UserTypesFor(
			typeOf(m.tupleForm.Values()[fieldObject]),
			strings.TrimSpace(m.tupleForm.Values()[fieldRelation]),
		)
	case fieldCondition:
		values = m.index.ConditionNames()
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

func (m *wizardModel) keyFieldPick(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "up", "k":
		m.fieldPick.Move(-1)
	case "down", "j":
		m.fieldPick.Move(1)
	case "esc":
		m.fieldPick = nil
	case "enter", " ":
		if v := m.fieldPick.Selected().Value; v != "" {
			m.applyFieldPick(m.pickField, v)
		}
		m.fieldPick = nil
		m.syncTupleVisibility()
	}
	return nil
}

// applyFieldPick writes the picked value into its field, prefilled per field.
func (m *wizardModel) applyFieldPick(f tupleField, v string) {
	values := m.tupleForm.Values()
	switch f {
	case fieldObject:
		values[fieldObject] = v + ":{{  }}"
	case fieldUser:
		values[fieldUser] = userPrefill(v)
	default:
		values[f] = v
	}
	m.tupleForm.SetValues(values)
	// Land the cursor between the braces of a freshly prefilled template so the
	// user can type the expression straight away.
	if idx := strings.Index(values[f], "{{  }}"); idx >= 0 {
		m.tupleForm.SetCursor(int(f), idx+3)
	}
}

// userPrefill renders a directly-related user type into its template. A
// userset keeps its relation suffix; a wildcard has no id to fill, so it is
// used literally.
func userPrefill(v string) string {
	if strings.HasSuffix(v, ":*") {
		return v
	}
	if typ, rel, ok := strings.Cut(v, "#"); ok {
		return typ + ":{{  }}#" + rel
	}
	return v + ":{{  }}"
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
	for _, part := range strings.Split(s, ",") {
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
