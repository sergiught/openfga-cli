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
		return m.paste.Focus()
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
		// Maps() is the single test the recipe screen branches on.
		if m.recipe.Maps() {
			m.useRecipe()
			return nil
		}
		// An explain-only recipe has no rule to hand over — appending its zero
		// Rule would drop an unnamed, already blocking rule on the hub. But the
		// user picked this event for a reason, and the explanation is a reason to
		// write something different, not to go away: connection.updated says
		// outright which tuple its is_enabled field should delete. So take the
		// same path a pasted payload takes — a rule carrying this sample, its
		// trigger filled in, and no tuples yet — and leave the user in the editor
		// rather than on a screen whose only exits are backwards.
		m.acceptPick(m.recipeEvent.Type, m.recipeEvent.Sample)
	}
	return nil
}

// useRecipe appends the recipe's rule and opens it, unwinding the add-rule fork
// on the way out — the same landing as the explain-only path, so accepting a
// mapping and writing one from scratch leave the user in the same place.
//
// It opens the rule rather than the hub because the rule is what the user just
// acquired and has not yet seen in their own file: the recipe screen showed a
// promise, and this is the thing itself.
func (m *wizardModel) useRecipe() {
	r := m.recipe.Rule
	r.Sample = &mapping.Sample{Label: m.recipeEvent.Type, Event: m.recipeEvent.Sample}
	// The recipe filled Name and When, so record that as auto-fill: otherwise
	// applyEventType cannot tell "the recipe wrote this" from "the user typed
	// this", and a later ctrl+e leaves the old trigger beside the new sample.
	r.AutoName, r.AutoWhen = r.Name, r.When
	m.doc.Rules = append(m.doc.Rules, r)
	m.ruleIdx = len(m.doc.Rules) - 1
	m.landOnNewRule()
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
