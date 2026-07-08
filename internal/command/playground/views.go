package playground

import (
	"math"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	"github.com/sergiught/openfga-cli/internal/ui/logo"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// View implements tea.Model in v2, owning terminal background and alt-screen
// state.
func (m Model) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	v.BackgroundColor = style.BgBase
	return v
}

// viewString renders the whole screen via the shell frame.
func (m Model) viewString() string {
	if m.width < 40 || m.height < 10 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			style.Faint.Render("terminal too small — need at least 40×10"))
	}
	if !m.ready {
		return "\n  " + m.spinner.View() + " starting ofga…"
	}
	if m.splash {
		return m.splashView()
	}
	m.sh.SetSidebar(m.sidebarContext(), m.sidebarNav(), m.sidebarFooter())
	m.sh.SetMain(sectionNames[m.section], m.sectionBody())
	st := shell.Status{Store: m.storeName, Model: short(m.modelID), Left: m.status, Keys: m.statusKeys()}
	if m.section == secQuery {
		st.Mode = strings.ToUpper(queryModes[m.qmode])
	}
	if m.loading {
		st.Spinner = m.spinner.View()
	}
	m.sh.SetStatus(st)

	if title, body := m.dialogContent(); body != "" {
		m.sh.SetDialog(title, body)
	} else {
		m.sh.SetDialog("", "")
	}
	m.sh.SetToast(m.toasts.View())
	return m.sh.View()
}

// dialogContent returns the title and body for the current modal state, or
// ("", "") when no dialog is open. The shell draws the box.
func (m Model) dialogContent() (string, string) {
	switch {
	case m.paletteOpen:
		return "Command palette", m.paletteList.View() + "\n" + style.Faint.Render("↑↓ choose · enter go · esc close")
	case m.formKind == formCreateStore:
		return "Create Store", m.form.View() + "\n" + style.Faint.Render("enter submit · esc cancel")
	case m.formKind == formWriteTuple:
		return "Write Tuple", m.form.View() + "\n" + style.Faint.Render("enter submit · esc cancel")
	case m.section == secModel && m.modelPicking:
		return "Switch model", m.modelsList.View() + "\n" + style.Faint.Render("↑↓ choose · enter load · esc cancel")
	}
	return "", ""
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
	ic := icons.I()
	sectionIcons := []string{ic.Store, ic.Model, ic.Tuple, ic.Change, ic.Query, ic.Assert}
	items := make([]shell.NavItem, len(sectionNames))
	for i, name := range sectionNames {
		it := shell.NavItem{Label: name, Icon: sectionIcons[i], Active: section(i) == m.section}
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
	if m.connLost {
		return style.Dot(style.DotError) + " " + style.Failure.Render("connection lost")
	}
	k := 0.5 + 0.5*math.Sin(m.pulse)
	dot := lipgloss.NewStyle().Foreground(style.Blend(style.Green, style.Faintc, k)).Render(style.IconDot)
	return dot + " " + style.Faint.Render("connected")
}

func (m Model) splashView() string {
	art := style.GradientBlockShimmer(logo.Word("ofga"), min(m.splashPhase, 1.0))
	w := lipgloss.Width(art)
	field := lipgloss.NewStyle().Foreground(style.Faintc).Render(strings.Repeat("╱", w))
	hero := lipgloss.JoinVertical(lipgloss.Center, field, art, field)
	hero = strings.Repeat("\n", int(m.splashY+0.5)) + hero
	tag := style.Faint.Render("a modern playground for OpenFGA")
	hint := style.Faint.Render("press any key to continue · q quit")
	block := lipgloss.JoinVertical(lipgloss.Center, hero, "", tag, "", hint)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}

func (m Model) sectionBody() string {
	switch m.section {
	case secStores:
		return m.listOrHint(m.storesList.View(), len(m.stores), "No stores yet — press n to create one")
	case secModel:
		if m.editorOpen {
			return m.editorBody()
		}
		if m.storeID == "" {
			return m.centerHint("Select a store first — press 1")
		}
		if len(m.graph.Types) == 0 {
			return m.centerHint("No authorization model in this store")
		}
		return m.graphVP.View()
	case secTuples:
		return m.listOrHint(m.tuplesList.View(), len(m.tuples), tupleHint(m.storeID))
	case secChanges:
		return m.listOrHint(m.changesList.View(), len(m.changes), changeHint(m.storeID))
	case secQuery:
		return m.queryBody()
	case secAssertions:
		return m.assertionsBody()
	}
	return ""
}

// centerHint renders a muted hint centered in the main content area, so empty
// sections read as intentional rather than blank.
func (m Model) centerHint(text string) string {
	w, h := m.contentSize()
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, style.Faint.Render(text))
}

// listOrHint shows the list when it has rows, otherwise a centered empty hint.
func (m Model) listOrHint(view string, count int, hint string) string {
	if count == 0 {
		return m.centerHint(hint)
	}
	return view
}

func (m Model) editorBody() string {
	help := style.Faint.Render("ctrl+s apply · esc cancel")
	if m.editorErr != "" {
		help = style.Failure.Render("error: "+m.editorErr) + "  " + help
	}
	return m.editor.View() + "\n" + help
}

func (m Model) queryBody() string {
	if m.storeID == "" {
		return m.centerHint("Select a store first — press 1")
	}

	// Header: mode chip + key hints. The huh fields already carry their own
	// focus accents, so no extra box is drawn around them (the main panel frames
	// the whole section).
	chip := lipgloss.NewStyle().Background(style.BgRaised).Foreground(style.Secondary).
		Bold(true).Padding(0, 1).Render(queryModes[m.qmode])
	var b strings.Builder
	b.WriteString(chip + "  " + style.Faint.Render("m mode · i edit · enter run") + "\n\n")
	b.WriteString(m.qform.View())
	if m.loading {
		b.WriteString("\n\n" + m.spinner.View() + " running…")
	} else if m.hasResult {
		b.WriteString("\n\n" + m.renderResult())
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

// statusKeys returns the right-aligned keycap hints for the current state.
// Quit ("q") and section-switch ("tab") are only listed where those keys
// actually work: takeover forms, the model editor, and the query form all
// capture every keypress, so those states omit them.
func (m Model) statusKeys() []string {
	switch {
	case m.formKind != formNone:
		return []string{"↵", "esc"}
	case m.section == secStores:
		return []string{"↑↓", "/", "↵", "n", "r", "tab", "q"}
	case m.section == secModel && m.editorOpen:
		return []string{"ctrl+s", "esc"}
	case m.section == secModel && m.modelPicking:
		return []string{"↑↓", "↵", "esc", "tab", "q"}
	case m.section == secModel:
		return []string{"↑↓←→", "e", "m", "r", "tab", "q"}
	case m.section == secTuples:
		return []string{"↑↓", "/", "a", "d", "r", "tab", "q"}
	case m.section == secChanges:
		return []string{"↑↓", "/", "r", "tab", "q"}
	case m.section == secQuery && m.editing:
		return []string{"tab", "↵", "esc"}
	case m.section == secQuery:
		return []string{"i", "m", "↵", "tab", "q"}
	case m.section == secAssertions:
		return []string{"↑↓", "t", "r", "tab", "q"}
	}
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// --- helpers ---

func tupleHint(storeID string) string {
	if storeID == "" {
		return "Select a store first — press 1"
	}
	return "No tuples yet — press a to add one"
}

func changeHint(storeID string) string {
	if storeID == "" {
		return "Select a store first — press 1"
	}
	return "No changes recorded yet"
}
