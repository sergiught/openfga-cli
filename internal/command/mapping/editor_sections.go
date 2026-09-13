package mapping

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// maxFilters caps tuple filters per rule, mirroring mapper's own limit:
// language/parser.go rejects a rule with more than three with "exceeds
// maximum of 3 filters". Capping here turns a validation failure into a
// message the user sees while they are still editing.
const maxFilters = 3

// --- action ---

func (m *wizardModel) openAction() {
	r := m.rule()
	if r == nil {
		return
	}
	for i := 0; i < m.actionPick.Len(); i++ {
		m.actionPick.SetCursor(i)
		if m.actionPick.Selected().Value == r.Action {
			break
		}
	}
	m.push(screenAction)
}

func (m *wizardModel) keyAction(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "up", "k":
		m.actionPick.Move(-1)
	case "down", "j":
		m.actionPick.Move(1)
	case "esc":
		m.pop()
	case "enter", " ":
		if r := m.rule(); r != nil {
			r.Action = m.actionPick.Selected().Value
			m.syncRules()
		}
		m.pop()
	}
	return nil
}

// --- variables ---

func (m *wizardModel) openVariables() {
	m.syncVariables()
	m.push(screenVariables)
}

func (m *wizardModel) syncVariables() {
	r := m.rule()
	if r == nil {
		return
	}
	items := make([]uilist.Item, 0, len(r.Variables))
	for i, v := range r.Variables {
		items = append(items, uilist.Item{
			TitleText: v.Name,
			DescText:  v.Expr,
			Filter:    v.Name + " " + v.Expr,
			ID:        fmt.Sprintf("var-%d", i),
			Index:     i,
		})
	}
	m.varList.SetItems(items)
	m.syncRules()
}

func (m *wizardModel) keyVariables(k tea.KeyPressMsg) tea.Cmd {
	if m.varList.SettingFilter() {
		return m.varList.Update(k)
	}
	r := m.rule()
	switch k.String() {
	case "a":
		r.Variables = append(r.Variables, mapping.Variable{})
		m.openVariable(len(r.Variables) - 1)
		return nil
	case "enter":
		if it, ok := m.varList.Selected(); ok {
			m.openVariable(it.Index)
		}
		return nil
	case "d":
		if it, ok := m.varList.Selected(); ok && it.Index < len(r.Variables) {
			r.Variables = append(r.Variables[:it.Index], r.Variables[it.Index+1:]...)
			m.syncVariables()
		}
		return nil
	case "esc":
		m.pop()
		return nil
	}
	return m.varList.Update(k)
}

func (m *wizardModel) openVariable(i int) {
	r := m.rule()
	if r == nil || i < 0 || i >= len(r.Variables) {
		return
	}
	m.varIdx = i
	m.varForm.Reset() // clears values, so it must come first
	m.varForm.SetValues([]string{r.Variables[i].Name, r.Variables[i].Expr})
	m.push(screenVariable)
}

func (m *wizardModel) keyVariable(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		r := m.rule()
		if r != nil && m.varIdx < len(r.Variables) {
			v := m.varForm.Values()
			r.Variables[m.varIdx] = mapping.Variable{
				Name: strings.TrimSpace(v[0]),
				Expr: strings.TrimSpace(v[1]),
			}
		}
		m.syncVariables()
		m.pop()
		return nil
	case "ctrl+p":
		// A variable's value is an expression, so the path goes in bare.
		m.openPathPick(m.varForm, m.varForm.FocusedIndex(), false)
		return nil
	}
	return m.varForm.Update(k)
}

// --- iterator ---

func (m *wizardModel) openIterator() {
	r := m.rule()
	if r == nil {
		return
	}
	src, as := "", ""
	if r.Iterator != nil {
		src, as = r.Iterator.Source, r.Iterator.As
	}
	m.iterForm.Reset() // clears values, so it must come first
	m.iterForm.SetValues([]string{src, as})
	m.push(screenIterator)
}

// commitIterator writes the form back, dropping the iterator entirely when the
// source is blank — an iterator with no source is not a partial iterator, it is
// the absence of one, and mapper would reject it.
func (m *wizardModel) commitIterator() {
	r := m.rule()
	if r == nil {
		return
	}
	v := m.iterForm.Values()
	src, as := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
	if src == "" {
		r.Iterator = nil
		m.syncRules()
		return
	}
	if r.Iterator == nil {
		r.Iterator = &mapping.Iterator{}
	}
	r.Iterator.Source, r.Iterator.As = src, as
	m.syncRules()
}

func (m *wizardModel) keyIterator(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.commitIterator()
		m.pop()
		return nil
	case "t":
		// Editing the iterator's tuples needs the iterator to exist first.
		m.commitIterator()
		if m.rule().Iterator == nil {
			m.errMsg = "set a source first"
			return nil
		}
		m.inIter = true
		m.openTuples()
		return nil
	case "ctrl+p":
		m.openPathPick(m.iterForm, m.iterForm.FocusedIndex(), false)
		return nil
	}
	return m.iterForm.Update(k)
}

// --- tuple filters ---

func (m *wizardModel) openFilters() {
	m.syncFilters()
	m.push(screenFilters)
}

func (m *wizardModel) syncFilters() {
	r := m.rule()
	if r == nil {
		return
	}
	items := make([]uilist.Item, 0, len(r.Filters))
	for i, f := range r.Filters {
		items = append(items, uilist.Item{
			TitleText: filterTitle(f),
			DescText:  f.Action,
			Filter:    f.User + " " + f.Relation + " " + f.Object,
			ID:        fmt.Sprintf("filter-%d", i),
			Index:     i,
		})
	}
	m.filterList.SetItems(items)
	m.syncRules()
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

func (m *wizardModel) keyFilters(k tea.KeyPressMsg) tea.Cmd {
	if m.filterList.SettingFilter() {
		return m.filterList.Update(k)
	}
	r := m.rule()
	switch k.String() {
	case "a":
		if len(r.Filters) >= maxFilters {
			m.errMsg = fmt.Sprintf("a rule takes at most %d tuple filters", maxFilters)
			return nil
		}
		r.Filters = append(r.Filters, mapping.TupleFilter{Action: "delete"})
		m.openFilter(len(r.Filters) - 1)
		return nil
	case "enter":
		if it, ok := m.filterList.Selected(); ok {
			m.openFilter(it.Index)
		}
		return nil
	case "d":
		if it, ok := m.filterList.Selected(); ok && it.Index < len(r.Filters) {
			r.Filters = append(r.Filters[:it.Index], r.Filters[it.Index+1:]...)
			m.syncFilters()
		}
		return nil
	case "esc":
		m.pop()
		return nil
	}
	return m.filterList.Update(k)
}

func (m *wizardModel) openFilter(i int) {
	r := m.rule()
	if r == nil || i < 0 || i >= len(r.Filters) {
		return
	}
	m.filterIdx = i
	f := r.Filters[i]
	m.filterForm.Reset() // clears values, so it must come first
	m.filterForm.SetValues([]string{f.User, f.Relation, f.Object, f.Action})
	m.push(screenFilter)
}

func (m *wizardModel) keyFilter(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		r := m.rule()
		if r != nil && m.filterIdx < len(r.Filters) {
			v := m.filterForm.Values()
			r.Filters[m.filterIdx] = mapping.TupleFilter{
				User:     strings.TrimSpace(v[0]),
				Relation: strings.TrimSpace(v[1]),
				Object:   strings.TrimSpace(v[2]),
				Action:   strings.TrimSpace(v[3]),
			}
		}
		m.syncFilters()
		m.pop()
		return nil
	case "ctrl+p":
		// Filter user/relation/object are templates; action is not, but the path
		// picker on it would be nonsense anyway and costs nothing to allow.
		m.openPathPick(m.filterForm, m.filterForm.FocusedIndex(), m.filterForm.FocusedIndex() < 3)
		return nil
	}
	return m.filterForm.Update(k)
}
