package playground

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sergiught/openfga-cli/internal/style"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// View renders the whole screen via the shell frame.
func (m Model) View() string {
	if !m.ready {
		return "\n  " + m.spinner.View() + " starting ofga…"
	}
	if m.splash {
		return m.splashView()
	}
	m.sh.SetSidebar(style.Gradient("ofga"), m.sidebarContext(), m.sidebarNav(), m.sidebarFooter())
	m.sh.SetMain(sectionNames[m.section], m.sectionBody())
	statusLeft := m.status
	if m.loading {
		statusLeft = m.spinner.View() + " " + m.status
	}
	m.sh.SetStatus(statusLeft, m.helpKeys())
	return m.sh.View()
}

func (m Model) sidebarContext() []string {
	if m.storeID == "" {
		return []string{style.Faint.Render("no store selected")}
	}
	name := m.storeName
	if name == "" {
		name = short(m.storeID)
	}
	lines := []string{lipgloss.NewStyle().Foreground(style.Accent).Render(style.IconStore) + " " + name}
	if m.modelID != "" {
		lines = append(lines, style.Faint.Render(style.IconModel+" "+short(m.modelID)))
	}
	return lines
}

func (m Model) sidebarNav() []shell.NavItem {
	items := make([]shell.NavItem, len(sectionNames))
	for i, name := range sectionNames {
		it := shell.NavItem{Label: name, Active: section(i) == m.section}
		switch section(i) {
		case secTuples:
			if len(m.tuples) > 0 {
				it.Badge = itoa(len(m.tuples))
			}
		case secChanges:
			if len(m.changes) > 0 {
				it.Badge = itoa(len(m.changes))
			}
		}
		items[i] = it
	}
	return items
}

func (m Model) sidebarFooter() string {
	if m.storeID == "" {
		return style.Dot(style.DotOffline) + " " + style.Faint.Render("disconnected")
	}
	return style.Dot(style.DotOnline) + " " + style.Faint.Render("connected")
}

func (m Model) helpKeys() string { return m.helpLine() }

func (m Model) splashView() string {
	logo := lipgloss.NewStyle().Bold(true).Render(style.Gradient("ofga"))
	tag := style.Faint.Render("a modern playground for OpenFGA")
	conn := style.Dot(style.DotBusy) + " " + style.Faint.Render("connecting…")
	hint := style.Faint.Render("press any key to continue · q quit")
	block := lipgloss.JoinVertical(lipgloss.Center, logo, tag, "", conn, "", hint)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
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
	return style.Faint.Render(keys) + style.Faint.Render("  ·  tab/1-7 sections · q quit")
}

func itoa(n int) string { return strconv.Itoa(n) }

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
