package playground

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sergiught/openfga-cli/internal/style"
)

// View renders the whole screen via the single bordered panel + help row.
func (m Model) View() string {
	if !m.ready {
		return "\n  " + m.spinner.View() + " starting ofga…"
	}
	m.lay.Column.SetTitle(m.tabBar())
	m.lay.Column.SetContent(m.sectionBody())
	m.lay.SetHelp(m.helpLine())
	return m.lay.View()
}

func (m Model) tabBar() string {
	logo := lipgloss.NewStyle().Bold(true).Foreground(style.Primary).Render(style.IconModel + " ofga")
	ctx := ""
	if m.storeName != "" {
		ctx = style.Faint.Render(" · " + m.storeName)
	} else if m.storeID != "" {
		ctx = style.Faint.Render(" · " + short(m.storeID))
	}

	tabs := make([]string, len(sectionNames))
	for i, name := range sectionNames {
		if section(i) == m.section {
			tabs[i] = lipgloss.NewStyle().Bold(true).
				Foreground(style.OnAccent).Background(style.Primary).
				Padding(0, 1).Render(name)
		} else {
			tabs[i] = lipgloss.NewStyle().Foreground(style.Muted).Padding(0, 1).Render(name)
		}
	}
	return logo + ctx + "   " + strings.Join(tabs, "")
}

func (m Model) sectionBody() string {
	if m.formKind != formNone {
		return m.formBody()
	}
	switch m.section {
	case secStores:
		return listOrHint(m.storesList.View(), len(m.stores), "no stores — press n to create one")
	case secModel:
		if m.modelPicking {
			return style.Subtitle.Render("Switch model — ↑↓ choose · enter load · esc cancel") + "\n\n" + m.modelsList.View()
		}
		if m.storeID == "" {
			return style.Faint.Render("select a store first (press 1)")
		}
		if len(m.graph.Types) == 0 {
			return style.Faint.Render("no authorization model in this store")
		}
		return m.graphVP.View()
	case secTuples:
		return listOrHint(m.tuplesList.View(), len(m.tuples), tupleHint(m.storeID))
	case secChanges:
		return listOrHint(m.changesList.View(), len(m.changes), changeHint(m.storeID))
	case secQuery:
		return m.queryBody()
	case secAssertions:
		return m.assertionsBody()
	case secSettings:
		return m.themesList.View()
	}
	return ""
}

func (m Model) formBody() string {
	title := "Create Store"
	if m.formKind == formWriteTuple {
		title = "Write Tuple"
	}
	return style.Heading.Render(title) + "\n\n" + m.form.View() + "\n" + style.Faint.Render("esc cancel")
}

func (m Model) queryBody() string {
	if m.storeID == "" {
		return style.Faint.Render("select a store first (press 1)")
	}
	mode := style.Key.Render("mode ") + lipgloss.NewStyle().Bold(true).Foreground(style.Keyword).Render(queryModes[m.qmode]) + style.Faint.Render("   (m to change)")
	var b strings.Builder
	b.WriteString(mode + "\n\n")
	b.WriteString(m.qform.View())
	b.WriteString("\n")
	if m.loading {
		b.WriteString("\n" + m.spinner.View() + " running…")
	} else if m.hasResult {
		b.WriteString("\n" + m.renderResult())
	}
	return b.String()
}

func (m Model) renderResult() string {
	msg := m.result
	if msg.err != nil {
		return style.Failure.Render("error: ") + msg.err.Error()
	}
	var b strings.Builder
	if msg.badge {
		b.WriteString(style.Allowed(msg.ok) + "  " + style.Faint.Render(msg.lines[0]))
		for _, l := range msg.lines[1:] {
			b.WriteString("\n" + style.Faint.Render(l))
		}
		return b.String()
	}
	b.WriteString(style.Heading.Render(msg.title))
	for _, l := range msg.lines {
		b.WriteString("\n" + style.Bullet() + " " + style.Value.Render(l))
	}
	return b.String()
}

func (m Model) assertionsBody() string {
	if m.storeID == "" {
		return style.Faint.Render("select a store first (press 1)")
	}
	if m.loading && len(m.assertions) == 0 {
		return m.spinner.View() + " loading…"
	}
	if len(m.assertions) == 0 {
		return style.Faint.Render("no assertions defined for this model")
	}
	body := m.assertionsList.View()
	if m.assertSummary != "" {
		sumSt := style.Success
		if parts := strings.SplitN(strings.TrimSuffix(m.assertSummary, " passed"), "/", 2); len(parts) == 2 && parts[0] != parts[1] {
			sumSt = style.Failure
		}
		body += "\n" + sumSt.Render(m.assertSummary)
	} else {
		body += "\n" + style.Faint.Render("press t to run the test-suite")
	}
	return body
}

func (m Model) helpLine() string {
	var keys string
	switch {
	case m.formKind != formNone:
		keys = "type to fill · enter submit · esc cancel"
	case m.section == secStores:
		keys = "↑↓ move · / filter · enter select · n new · r reload"
	case m.section == secModel && m.modelPicking:
		keys = "↑↓ move · enter load · esc cancel"
	case m.section == secModel:
		keys = "↑↓←→ pan · m switch model · r reload"
	case m.section == secTuples:
		keys = "↑↓ move · / filter · a add · d delete · r reload"
	case m.section == secChanges:
		keys = "↑↓ move · / filter · r reload"
	case m.section == secQuery && m.editing:
		keys = "tab next · enter run · esc cancel"
	case m.section == secQuery:
		keys = "i edit · m mode · enter run"
	case m.section == secAssertions:
		keys = "↑↓ move · t run · r reload"
	case m.section == secSettings:
		keys = "↑↓ preview · enter save"
	}
	nav := style.Faint.Render("tab/1-7 sections · q quit")
	status := m.status
	if m.loading {
		status = m.spinner.View() + " " + status
	}
	return lipgloss.NewStyle().Foreground(style.Muted).Render(status) +
		style.Faint.Render("    ") + style.Faint.Render(keys) +
		style.Faint.Render("  ·  ") + nav
}

// --- helpers ---

func listOrHint(view string, count int, hint string) string {
	if count == 0 {
		return style.Faint.Render(hint)
	}
	return view
}

func tupleHint(storeID string) string {
	if storeID == "" {
		return "select a store first (press 1)"
	}
	return "no tuples — press a to add one"
}

func changeHint(storeID string) string {
	if storeID == "" {
		return "select a store first (press 1)"
	}
	return "no changes recorded yet"
}
