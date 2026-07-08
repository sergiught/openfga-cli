package playground

import (
	"math"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/sergiught/openfga-cli/internal/fga"
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
	if !m.ready {
		return "\n  " + m.spinner.View() + " starting ofga…"
	}
	if m.width < 40 || m.height < 10 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			style.Faint.Render("terminal too small — need at least 40×10"))
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
	hero = strings.Repeat("\n", max(0, int(m.splashY+0.5))) + hero
	tag := style.Faint.Render("a modern playground for OpenFGA")
	hint := style.Faint.Render("press any key to continue · q quit")
	block := lipgloss.JoinVertical(lipgloss.Center, hero, "", tag, "", hint)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}

func (m Model) sectionBody() string {
	var body string
	switch m.section {
	case secStores:
		if len(m.stores) == 0 {
			body = m.centerHint("No stores yet — press n to create one")
		} else {
			w, h := m.contentSize()
			body = masterDetail(m.storesList.View(), m.storePreview(), w, h)
		}
	case secModel:
		if m.editorOpen {
			body = m.editorBody()
		} else if m.storeID == "" {
			body = m.centerHint("Select a store first — press 1")
		} else if len(m.graph.Types) == 0 {
			body = m.centerHint("No authorization model in this store")
		} else {
			body = m.graphVP.View()
		}
	case secTuples:
		if len(m.tuples) == 0 {
			body = m.centerHint(tupleHint(m.storeID))
		} else {
			w, h := m.contentSize()
			body = masterDetail(m.tuplesList.View(), m.tuplePreview(), w, h)
		}
	case secChanges:
		if len(m.changes) == 0 {
			body = m.centerHint(changeHint(m.storeID))
		} else {
			w, h := m.contentSize()
			body = masterDetail(m.changesList.View(), m.changePreview(), w, h)
		}
	case secQuery:
		body = m.queryBody()
	case secAssertions:
		body = m.assertionsBody()
	}
	if m.fading {
		return style.Faint.Render(ansi.Strip(body))
	}
	return body
}

// centerHint renders a muted hint centered in the main content area, so empty
// sections read as intentional rather than blank.
func (m Model) centerHint(text string) string {
	w, h := m.contentSize()
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, style.Faint.Render(text))
}

// splitListWidth returns the width of the list pane in masterDetail's
// list/card split. It is the single source of truth for that 40% share —
// resize() must size the section lists (storesList/tuplesList/changesList)
// to this same width so the list's rendered content matches the box
// masterDetail wraps it in; otherwise lipgloss word-wraps the over-wide
// rows to fit.
func splitListWidth(w int) int { return w * 2 / 5 }

// masterDetail joins a list (40%) and a raised preview card (60%) into a
// single row. In lipgloss v2, Width/Height are border-inclusive (the border
// size is subtracted from the requested width before the content is laid
// out), so Width(cw).Height(h) alone already produces a card whose total
// footprint is cw×h — no -2 compensation needed as it would be in v1.
func masterDetail(list, card string, w, h int) string {
	lw := splitListWidth(w)
	cw := w - lw - 2
	if cw < 10 {
		return list // too narrow: list only
	}
	left := lipgloss.NewStyle().Width(lw).Height(h).Render(list)
	right := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(style.Faintc).
		Background(style.BgRaised).Padding(0, 1).
		Width(cw).Height(h).
		Render(card)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

// keyValueCard renders aligned key/value lines using style.Key/style.Value,
// mirroring output.KeyValues's alignment but returning a string for use
// inside a masterDetail preview card instead of writing to an io.Writer.
func keyValueCard(pairs [][2]string) string {
	width := 0
	for _, p := range pairs {
		if w := lipgloss.Width(p[0]); w > width {
			width = w
		}
	}
	lines := make([]string, len(pairs))
	for i, p := range pairs {
		pad := strings.Repeat(" ", width-lipgloss.Width(p[0]))
		lines[i] = style.Key.Render(p[0]) + pad + "  " + style.Value.Render(p[1])
	}
	return strings.Join(lines, "\n")
}

// storePreview renders the selected store's details for the stores
// master-detail split, or "" when nothing is selected.
func (m Model) storePreview() string {
	it, ok := m.storesList.Selected()
	if !ok || it.Index < 0 || it.Index >= len(m.stores) {
		return ""
	}
	s := m.stores[it.Index]
	return keyValueCard([][2]string{
		{"Name", s.Name},
		{"ID", s.ID},
		{"Created", s.CreatedAt.Format("2006-01-02 15:04:05")},
	})
}

// tuplePreview renders the selected tuple's details for the tuples
// master-detail split, or "" when nothing is selected.
func (m Model) tuplePreview() string {
	it, ok := m.tuplesList.Selected()
	if !ok || it.Index < 0 || it.Index >= len(m.tuples) {
		return ""
	}
	t := m.tuples[it.Index]
	return keyValueCard([][2]string{
		{"User", t.Key.User},
		{"Relation", t.Key.Relation},
		{"Object", t.Key.Object},
		{"Tuple", fga.FormatTuple(t.Key)},
	})
}

// changePreview renders the selected change's details for the changes
// master-detail split, or "" when nothing is selected.
func (m Model) changePreview() string {
	it, ok := m.changesList.Selected()
	if !ok || it.Index < 0 || it.Index >= len(m.changes) {
		return ""
	}
	c := m.changes[it.Index]
	op := "write"
	if c.Operation == "TUPLE_OPERATION_DELETE" {
		op = "delete"
	}
	return keyValueCard([][2]string{
		{"Operation", op},
		{"Timestamp", c.Timestamp.Format("2006-01-02 15:04:05")},
		{"Tuple", fga.FormatTuple(c.TupleKey)},
	})
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
	sections := []string{
		chip + "  " + style.Faint.Render("m mode · i edit · enter run"),
		m.qform.View(),
	}

	switch {
	case m.loading:
		sections = append(sections, m.spinner.View()+" running…")
	case m.hasResult && m.result.err != nil:
		sections = append(sections, style.Failure.Render("error: ")+m.result.err.Error())
	case m.hasResult:
		sections = append(sections, m.renderResultCard())
	case len(m.history) == 0:
		sections = append(sections, style.Keycap("i")+" edit  "+style.Keycap("↵")+" run")
	}

	if hs := m.historyStrip(); hs != "" {
		sections = append(sections, hs)
	}
	body := strings.Join(sections, "\n\n")

	// Chip + form + result card + history can add up to more rows than short
	// terminals have available; renderMain doesn't cap its content height, so
	// an over-tall body pushes the status bar off the bottom of the frame.
	// Trim to what actually fits, clipping the bottom-most content.
	_, h := m.contentSize()
	lines := strings.Split(body, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// renderResultCard frames the current query result in a rounded card on the
// raised surface. Badge results (check) show a big ALLOWED/DENIED chip plus
// latency; list-objects/list-users results show the title+bullets layout
// inside the same card frame. While m.flash is true — for one frame right
// after a badge result lands — the border uses the verdict color instead of
// the default faint one.
func (m Model) renderResultCard() string {
	msg := m.result
	border := style.Faintc
	var body string
	if msg.badge {
		verdict := style.Chip(" "+icons.I().Cross+" DENIED ", style.OnAccent, style.Red)
		flashColor := style.Red
		if msg.ok {
			verdict = style.Chip(" "+icons.I().Check+" ALLOWED ", style.OnAccent, style.Green)
			flashColor = style.Green
		}
		if m.flash {
			border = flashColor
		}
		meta := style.Faint.Render(itoa(int(msg.ms)) + "ms")
		body = verdict + "  " + meta
		for _, l := range msg.lines {
			body += "\n" + style.Faint.Render(l)
		}
	} else {
		body = style.Heading.Render(msg.title)
		for _, l := range msg.lines {
			body += "\n" + style.Bullet() + " " + style.Value.Render(l)
		}
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Background(style.BgRaised).Padding(0, 2).
		Render(body)
}

// historyStrip renders up to 5 numbered chips for recent query results,
// newest first: a colored check/cross plus the first field value. Returns ""
// when there is no history yet.
func (m Model) historyStrip() string {
	if len(m.history) == 0 {
		return ""
	}
	chips := make([]string, len(m.history))
	for i, h := range m.history {
		ic, c := icons.I().Cross, style.Red
		if h.ok {
			ic, c = icons.I().Check, style.Green
		}
		label := itoa(i+1) + " " + lipgloss.NewStyle().Foreground(c).Background(style.BgHighlight).Render(ic)
		chips[i] = style.Chip(label+" "+h.vals[0], style.Muted, style.BgHighlight)
	}
	return style.Faint.Render("recent  ") + strings.Join(chips, " ")
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
