package mapping

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/style"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// sideBySide reports whether the preview pane sits beside the editor.
func (m *wizardModel) sideBySide() bool { return m.width >= sideBySideMin }

// contentWidth is the editor column's width: the full width when stacked, a bit
// under half when side by side, clamped so neither pane collapses.
func (m *wizardModel) contentWidth() int {
	w := m.width - 4
	if m.sideBySide() {
		w = m.width/2 - 4
	}
	if w < 32 {
		w = 32
	}
	if w > 72 {
		w = 72
	}
	return w
}

// applySize propagates the current size to the widgets that need it.
func (m *wizardModel) applySize() {
	cw := m.contentWidth()
	m.modelPath.SetWidth(cw)
	m.rules.SetSize(cw, m.listHeight())
	m.events.SetSize(cw, m.listHeight())
	m.paste.SetWidth(cw)
	m.paste.SetHeight(m.listHeight())
	m.eventPath.SetWidth(cw)
	m.tupleList.SetSize(cw, m.listHeight())
	m.tupleForm.SetWidth(cw)
	m.tupleForm.SetHeight(m.listHeight())
	m.paths.SetSize(cw, m.listHeight())
	m.varList.SetSize(cw, m.listHeight())
	m.varForm.SetWidth(cw)
	m.iterForm.SetWidth(cw)
	m.filterList.SetSize(cw, m.listHeight())
	m.filterForm.SetWidth(cw)
}

func (m *wizardModel) listHeight() int {
	h := m.height - 10
	if h < 5 {
		h = 5
	}
	return h
}

func (m *wizardModel) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	v.BackgroundColor = style.BgBase
	return v
}

func (m *wizardModel) viewString() string {
	body := m.editor()
	if !m.sideBySide() {
		return body + "\n\n" + m.previewPane(m.width-4)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(m.width/2).Render(body),
		m.previewPane(m.width/2-4),
	)
}

func (m *wizardModel) editor() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n\n")

	switch m.top() {
	case screenWelcome:
		b.WriteString(m.welcomeBody())
	case screenModelSource:
		b.WriteString(m.sourcePick.View(m.contentWidth()))
	case screenModelFile:
		b.WriteString(m.modelPath.View())
	case screenRules:
		b.WriteString(m.rulesBody())
	case screenRule:
		b.WriteString(m.sections.View(m.contentWidth()))
	case screenTrigger:
		b.WriteString(m.trigger.View())
	case screenEventPick:
		b.WriteString(m.events.View())
	case screenEventPaste:
		b.WriteString(m.paste.View())
	case screenEventFile:
		b.WriteString(m.eventPath.View())
	case screenTuples:
		if len(*m.tuplesOrEmpty()) == 0 {
			b.WriteString(lipgloss.NewStyle().Foreground(style.Muted).Render(
				"No tuples yet.\n\nPress " +
					lipgloss.NewStyle().Bold(true).Foreground(style.Fg).Render("a") +
					" to add one."))
		} else {
			b.WriteString(m.tupleList.View())
		}
	case screenTuple:
		if m.fieldPick != nil {
			b.WriteString(m.fieldPick.View(m.contentWidth()))
			break
		}
		b.WriteString(m.tupleForm.View())
		if len(m.ctxKeys) > 0 {
			b.WriteString("\n" + lipgloss.NewStyle().Foreground(style.Muted).Render(
				"condition parameters: "+strings.Join(m.ctxKeys, ", ")))
		}
	case screenAction:
		b.WriteString(m.actionPick.View(m.contentWidth()))
	case screenVariables:
		b.WriteString(listOrEmpty(m.varList, "No variables yet.\n\nPress a to add one."))
	case screenVariable:
		b.WriteString(m.varForm.View())
	case screenIterator:
		b.WriteString(m.iterForm.View())
	case screenFilters:
		b.WriteString(listOrEmpty(m.filterList, "No tuple filters yet.\n\nPress a to add one."))
	case screenFilter:
		b.WriteString(m.filterForm.View())
	case screenPathPick:
		b.WriteString(m.paths.View())
	case screenConfirmSave:
		b.WriteString(m.saveSummary())
	case screenConfirmDelete:
		b.WriteString(m.confirmMsg + "\n\n" + "y delete · n cancel")
	default:
		b.WriteString("")
	}

	if m.errMsg != "" {
		b.WriteString("\n\n" + lipgloss.NewStyle().Foreground(style.Red).Render("✗ "+m.errMsg))
	}
	if m.noteMsg != "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(style.Muted).Render(m.noteMsg))
	}
	b.WriteString("\n\n" + m.footer())
	return b.String()
}

// listOrEmpty renders a list, or its empty state when it has no items. Every
// list screen in the wizard needs this, so it lives here rather than in six
// copies.
func listOrEmpty(l *uilist.List, empty string) string {
	if len(l.Model.Items()) == 0 {
		return lipgloss.NewStyle().Foreground(style.Muted).Render(empty)
	}
	return l.View()
}

func (m *wizardModel) header() string {
	titles := map[screen]string{
		screenWelcome:       "Create a mapping",
		screenModelSource:   "Authorization model",
		screenModelFile:     "Load a model file",
		screenRules:         "Rules",
		screenRule:          "Rule",
		screenTrigger:       "Trigger",
		screenEventPick:     "Pick an event",
		screenEventPaste:    "Paste an event",
		screenEventFile:     "Load an event",
		screenTuples:        "Tuples",
		screenTuple:         "Tuple",
		screenAction:        "Rule action",
		screenVariables:     "Variables",
		screenVariable:      "Variable",
		screenIterator:      "Iterator",
		screenFilters:       "Tuple filters",
		screenFilter:        "Tuple filter",
		screenPathPick:      "Insert a path",
		screenConfirmSave:   "Save the mapping",
		screenConfirmDelete: "Delete rule",
	}
	t := titles[m.top()]
	if t == "" {
		t = "Create a mapping"
	}
	return lipgloss.NewStyle().Bold(true).Foreground(style.Primary).Render(t)
}

func (m *wizardModel) footer() string {
	keys := map[screen]string{
		screenWelcome:       "enter continue · esc cancel",
		screenModelSource:   "↑/↓ move · enter select · esc back",
		screenModelFile:     "enter load · esc back",
		screenRules:         "a add · enter open · d delete · ctrl+s save · esc quit",
		screenRule:          "↑/↓ move · enter open · ctrl+s save · esc back",
		screenTrigger:       "tab next · ctrl+e pick event · ctrl+p insert path · esc back",
		screenEventPick:     "/ filter · enter select · esc back",
		screenEventPaste:    "ctrl+d accept · esc cancel",
		screenEventFile:     "enter load · esc back",
		screenTuples:        "a add · enter edit · d delete · esc back",
		screenTuple:         "tab next · ctrl+o pick from model · ctrl+p insert path · esc back",
		screenAction:        "↑/↓ move · enter select · esc back",
		screenVariables:     "a add · enter edit · d delete · esc back",
		screenVariable:      "tab next · ctrl+p insert path · esc back",
		screenIterator:      "tab next · ctrl+t edit tuples · ctrl+p insert path · esc back",
		screenFilters:       "a add · enter edit · d delete · esc back",
		screenFilter:        "tab next · ctrl+p insert path · esc back",
		screenPathPick:      "/ filter · enter insert · esc cancel",
		screenConfirmDelete: "y delete · n cancel",
	}
	return lipgloss.NewStyle().Foreground(style.Faintc).Render(keys[m.top()])
}

func (m *wizardModel) welcomeBody() string {
	model := "no authorization model yet"
	if !m.index.Empty() {
		model = fmt.Sprintf("%d types loaded", len(m.index.TypeNames()))
	}
	return strings.Join([]string{
		"Turn identity-provider events into OpenFGA tuples.",
		"",
		"You'll pick an event, describe the tuples it should produce, and watch",
		"the mapping and its output update as you go.",
		"",
		lipgloss.NewStyle().Foreground(style.Muted).Render("file:  ") + m.path,
		lipgloss.NewStyle().Foreground(style.Muted).Render("model: ") + model,
	}, "\n")
}

func (m *wizardModel) rulesBody() string {
	if len(m.doc.Rules) == 0 {
		return lipgloss.NewStyle().Foreground(style.Muted).Render(
			"No rules yet.\n\nPress " +
				lipgloss.NewStyle().Bold(true).Foreground(style.Fg).Render("a") +
				" to add the first one.")
	}
	return m.rules.View()
}

// previewPane renders the file being built and what the current sample turns
// into. It is the whole point of the hub-and-spoke design: every edit is
// visible immediately.
func (m *wizardModel) previewPane(w int) string {
	if w < 20 {
		return ""
	}
	var b strings.Builder
	b.WriteString(paneTitle(m.path, w))
	b.WriteString("\n")
	b.WriteString(clampLines(string(m.preview.YAML), w, m.yamlLines()))

	b.WriteString("\n\n")
	b.WriteString(paneTitle("preview", w))
	b.WriteString("\n")
	b.WriteString(m.evaluationLines(w))
	return b.String()
}

// yamlLines gives the YAML half whatever is left after the evaluation half; on
// a short terminal the evaluation wins, since it is what the user is reacting to.
func (m *wizardModel) yamlLines() int {
	if m.height < 40 {
		return 0
	}
	return m.height - 20
}

func (m *wizardModel) evaluationLines(w int) string {
	var out []string
	for _, d := range m.preview.Diagnostics {
		line := fmt.Sprintf("✗ line %d: %s", d.Position.StartLine, d.Message)
		if d.Field != "" {
			line = fmt.Sprintf("✗ line %d: %s: %s", d.Position.StartLine, d.Field, d.Message)
		}
		out = append(out, lipgloss.NewStyle().Foreground(style.Red).Render(clamp(sanitizeKeepingLines(line), w)))
	}
	if m.preview.EvalErr != nil {
		out = append(out, lipgloss.NewStyle().Foreground(style.Red).Render(
			clamp(sanitizeKeepingLines("✗ "+m.preview.EvalErr.Error()), w)))
	}
	for _, t := range m.preview.Tuples {
		action := string(t.Action)
		if action == "" {
			action = "write"
		}
		// An evaluated tuple is built from the user's own event payload.
		line := fmt.Sprintf("✓ %-6s %s  %s  %s", action, t.User, t.Relation, t.Object)
		out = append(out, lipgloss.NewStyle().Foreground(style.Green).Render(
			clamp(style.SanitizeTerminal(line), w)))
	}
	for _, op := range m.preview.Filters {
		for _, f := range op.Filters {
			out = append(out, lipgloss.NewStyle().Foreground(style.Primary).Render(
				clamp(style.SanitizeTerminal(fmt.Sprintf("⟳ %-6s %s", f.Action, filterSummary(f))), w)))
		}
	}
	for _, r := range m.preview.Rules {
		if r.Status == "skipped" {
			out = append(out, lipgloss.NewStyle().Foreground(style.Faintc).Render(
				clamp(style.SanitizeTerminal(fmt.Sprintf("– %s: skipped", r.Name)), w)))
		}
	}
	if len(out) == 0 {
		return lipgloss.NewStyle().Foreground(style.Faintc).Render("no sample event yet")
	}
	return strings.Join(out, "\n")
}

func filterSummary(f language.TupleFilter) string {
	var parts []string
	for _, p := range [][2]string{{"user", f.User}, {"relation", f.Relation}, {"object", f.Object}} {
		if p[1] != "" {
			parts = append(parts, p[0]+"="+p[1])
		}
	}
	return strings.Join(parts, " ")
}

func paneTitle(s string, w int) string {
	line := s + " "
	if pad := w - len(line); pad > 0 {
		line += strings.Repeat("─", pad)
	}
	return lipgloss.NewStyle().Foreground(style.Muted).Render(line)
}

// clampLines truncates a block to n lines and each line to w cells. n <= 0 hides
// the block entirely, which is how the YAML half yields space on short screens.
func clampLines(s string, w, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], lipgloss.NewStyle().Foreground(style.Faintc).Render("…"))
	}
	for i, l := range lines {
		lines[i] = clamp(l, w)
	}
	return strings.Join(lines, "\n")
}

// sanitizeKeepingLines strips terminal control sequences the way
// style.SanitizeTerminal does, but per line, so a message laid out across
// several lines keeps its shape. mapper's compile and evaluation errors use
// that layout to point a caret at the offending column, which collapses into
// nonsense if the newlines are dropped along with the escapes.
func sanitizeKeepingLines(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = style.SanitizeTerminal(lines[i])
	}
	return strings.Join(lines, "\n")
}

func clamp(s string, w int) string {
	r := []rune(s)
	if w < 1 || len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}
