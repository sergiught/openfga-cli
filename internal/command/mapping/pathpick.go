package mapping

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
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
		m.errMsg = "pick a sample event first (ctrl+e on the Trigger screen) to browse its paths"
		return
	}
	m.pathTarget, m.pathIdx, m.pathTmpl = f, idx, template
	m.paths.SetItems(items)
	m.paths.ResetFilter()
	m.push(screenPathPick)
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
			desc = fmt.Sprintf("%s · %s", p.Kind, p.Example)
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

// insertPath splices the chosen path into the field at the cursor: wrapped in
// `{{ }}` for a template field, bare for an expression field.
func (m *wizardModel) insertPath(expr string) {
	if m.pathTarget == nil {
		return
	}
	text := expr
	if m.pathTmpl {
		text = "{{ " + expr + " }}"
	}
	m.pathTarget.Insert(m.pathIdx, text)
}
