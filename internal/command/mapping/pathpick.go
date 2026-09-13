package mapping

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// openPathPick shows the paths available in the current scope, remembering
// which field and offset the choice must land in.
//
// The paths come from the rule's sample event, which is why the wizard nudges
// the user to pick one first: without a sample there is nothing to read.
func (m *wizardModel) openPathPick(f *field.Form, idx int, template bool) {
	items := m.pathItems()
	if len(items) == 0 {
		m.errMsg = m.noPathsMessage()
		return
	}
	m.pathTarget, m.pathIdx, m.pathTmpl = f, idx, template
	m.paths.SetItems(items)
	m.paths.ResetFilter()
	m.push(screenPathPick)
}

// noPathsMessage says why there is nothing to browse. A missing sample is the
// usual reason, but inside an iterator the source has to resolve to a non-empty
// array in that sample too — and telling a user who has already picked an event
// to go and pick one sends them somewhere they cannot fix it.
func (m *wizardModel) noPathsMessage() string {
	r := m.rule()
	if r == nil || r.Sample == nil {
		return "pick a sample event first (ctrl+e on the Trigger screen) to browse its paths"
	}
	if m.inIter && r.Iterator != nil && r.Iterator.As != "" {
		return fmt.Sprintf("%s does not resolve to a list of objects in the sample event",
			r.Iterator.Source)
	}
	return "the sample event has no paths to browse"
}

// pathItems lists the sample's paths. Inside an iterator the paths are rewritten
// to the iterator's alias, because that is the name the expression must use:
// `identity.connection`, not `input.data.object.identities[0].connection`.
func (m *wizardModel) pathItems() []uilist.Item {
	r := m.rule()
	if r == nil || r.Sample == nil {
		return nil
	}

	root := "input"
	value := any(r.Sample.Event)
	if m.inIter && r.Iterator != nil && r.Iterator.As != "" {
		elem, ok := mapping.Lookup(r.Sample.Event, r.Iterator.Source+"[0]")
		if !ok {
			return nil
		}
		root, value = r.Iterator.As, elem
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
		items = append(items, uilist.Item{
			TitleText: p.Expr,
			DescText:  desc,
			Filter:    p.Expr,
			ID:        p.Expr,
			Index:     i,
		})
	}
	return items
}

func (m *wizardModel) keyPathPick(k tea.KeyPressMsg) tea.Cmd {
	if m.paths.SettingFilter() {
		cmd := m.paths.Update(k)
		m.paths.ResyncFilter()
		return cmd
	}
	switch k.String() {
	case "esc":
		m.pop()
		return nil
	case "enter":
		if it, ok := m.paths.Selected(); ok {
			m.insertPath(it.ID)
		}
		m.pop()
		return nil
	}
	return m.paths.Update(k)
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

// insertPath splices the chosen path into the field at the cursor: wrapped in
// `{{ }}` for a template field, bare for an expression field.
func (m *wizardModel) insertPath(expr string) {
	if m.pathTarget == nil {
		return
	}
	text := expr
	if m.pathTmpl && !insideTemplate(m.pathTarget.Values()[m.pathIdx], m.pathTarget.Cursor(m.pathIdx)) {
		text = "{{ " + expr + " }}"
	}
	m.pathTarget.Insert(m.pathIdx, text)
}
