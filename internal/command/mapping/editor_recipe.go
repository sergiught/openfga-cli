package mapping

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

// openPayloadKind asks what the user is mapping. It is the first fork in the
// flow and the only place the Auth0 catalog is advertised, so adding a rule
// always passes through here.
func (m *wizardModel) openPayloadKind() {
	m.kindPick.SetCursor(0)
	m.push(screenPayloadKind)
}

func (m *wizardModel) keyPayloadKind(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "up", "k":
		m.kindPick.Move(-1)
	case "down", "j":
		m.kindPick.Move(1)
	case "esc":
		m.pop()
	case "enter", " ":
		if m.kindPick.Selected().Value == "auth0" {
			m.openEventPick()
			return nil
		}
		m.paste.SetValue("")
		m.push(screenEventPaste)
	}
	return nil
}

// openRecipe shows the worked mapping for an event before anything is
// committed, so the user sees what they are about to get and what their
// model must have. Only reached from the add-rule fork; ctrl+e on an
// existing rule attaches the event directly instead (see fromFork).
func (m *wizardModel) openRecipe(e auth0.Event) {
	m.recipeEvent = e
	m.recipe = e.Recipe
	m.push(screenRecipe)
}

func (m *wizardModel) keyRecipe(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.pop()
	case "m":
		m.push(screenModelSource)
	case "enter":
		m.useRecipe()
	}
	return nil
}

// useRecipe appends the recipe's rule and drops the user on the hub. The
// stack is reset rather than popped: the hub is the base of navigation, and
// unwinding the setup screens with esc is not what the user means by "go
// back" once they have a rule in hand. The setup screens stay reachable by
// key — m for the model, a for another rule.
func (m *wizardModel) useRecipe() {
	r := m.recipe.Rule
	r.Sample = &mapping.Sample{Label: m.recipeEvent.Type, Event: m.recipeEvent.Sample}
	m.doc.Rules = append(m.doc.Rules, r)
	m.ruleIdx = len(m.doc.Rules) - 1
	m.stack = []screen{screenRules}
	m.syncRules()
}

// sourcePath names where a template's value came from, for the column beside
// the resolved value. It unwraps one enclosing call so fga_escape(...) does not
// hide the path, and gives up honestly when a field interpolates more than once:
// no single path describes such a value, and inventing one would teach a lie.
func sourcePath(template string) string {
	open := strings.Index(template, "{{")
	if open < 0 {
		return "(fixed)"
	}
	if strings.Count(template, "{{") > 1 {
		return template
	}
	end := strings.Index(template, "}}")
	if end < open {
		return template
	}
	expr := strings.TrimSpace(template[open+2 : end])
	if i := strings.Index(expr, "("); i >= 0 && strings.HasSuffix(expr, ")") {
		expr = strings.TrimSpace(expr[i+1 : len(expr)-1])
	}
	return strings.TrimPrefix(expr, "input.")
}
