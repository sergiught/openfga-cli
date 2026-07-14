package playground

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// View implements tea.Model in v2, owning terminal background and alt-screen
// state.
func (m Model) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	v.BackgroundColor = style.BgBase
	// Enable wheel events so the scrollable panes (model graph, resolution tree)
	// respond to the mouse. Note: this captures the mouse, so native terminal
	// text selection then needs Shift held.
	v.MouseMode = tea.MouseModeCellMotion
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
	m.sh.SetSidebar(m.sidebarContext(), m.sidebarNav(), m.sidebarFooter())
	m.sh.SetBrand("", m.version)
	m.sh.SetMain(m.mainTitle(), m.sectionBody())
	// Always advertise the help overlay so every binding is discoverable.
	st := shell.Status{Left: m.sectionStatus(), Keys: append(m.statusKeys(), "? help")}
	// The active profile leads the footer as the connection identity.
	st.Profile = "Profile: " + m.cli.Config.Active
	// Show the selected store's name and the full (untruncated) model id, tagged
	// "(latest)" when it is the store's newest model.
	if name := m.currentStoreName(); name != "" {
		st.Store = "Store: " + name
	}
	if m.modelID != "" {
		st.Model = "Model ID: " + m.modelID
		if m.modelIsLatest {
			st.Model += " (latest)"
		}
	}
	if m.section == secQuery {
		st.Mode = strings.ToUpper(queryModes[m.qmode])
	}
	if m.loading {
		st.Spinner = m.spinner.View()
	}
	m.sh.SetStatus(st)

	if title, body := m.dialogContent(); body != "" {
		if m.formErr != "" || m.confirm != nil {
			m.sh.SetDialog(title, body, style.Red) // error / destructive: red title + border
		} else {
			m.sh.SetDialog(title, body)
		}
	} else {
		m.sh.SetDialog("", "")
	}
	m.sh.SetToast(m.toasts.View())
	m.sh.SetEntrance(m.entranceFrac, m.entering && m.entranceFrac > 0.55)
	m.sh.SetDrift(m.drift)
	m.sh.SetFocus(m.focus)
	return m.sh.View()
}

// dialogContent returns the title and body for the current modal state, or
// ("", "") when no dialog is open. The shell draws the box.
// helpBody renders the ? overlay: global keys plus the keys for the current
// section, formatted as an aligned two-column reference.
func (m Model) helpBody() string {
	global := [][2]string{
		{"tab / ↑↓", "move between tabs (from the tab bar)"},
		{"↵ / esc", "enter the panel / return to the tabs"},
		{"1–8", "jump to a section (1–5 rerun history in Tuple Queries)"},
		{"ctrl+k", "command palette"},
		{"?", "toggle this help"},
		{"q", "quit (from the tab bar)"},
		{"ctrl+c", "quit"},
	}
	var section [][2]string
	switch m.section {
	case secProfiles:
		section = [][2]string{{"↑↓", "move"}, {"/", "filter"}, {"↵", "switch to profile"}, {"n", "add"}, {"e", "edit"}, {"d", "delete"}}
	case secStores:
		section = [][2]string{{"↑↓", "move"}, {"/", "filter"}, {"↵", "select store"}, {"n", "new"}, {"d", "delete"}, {"r", "reload"}}
	case secModel:
		section = [][2]string{{"↑↓ k/j", "scroll"}, {"←→ h/l", "pan"}, {"pgup/pgdn b/f/space", "page"}, {"g/G home/end", "top/bottom"}, {"e", "edit DSL"}, {"m", "switch model"}, {"r", "reload"}}
	case secTuples:
		section = [][2]string{{"↑↓", "move"}, {"/", "filter"}, {"a", "add"}, {"d", "delete"}, {"r", "reload"}}
	case secChanges:
		section = [][2]string{{"↑↓", "move"}, {"/", "filter"}, {"r", "reload"}}
	case secQuery:
		section = [][2]string{{"i / ↵", "edit query"}, {"tab", "cycle mode"}, {"1–5", "rerun recent"}, {"r", "resolve"}}
	case secAssertions:
		section = [][2]string{{"↑↓", "move"}, {"/", "filter"}, {"↵", "run + resolve"}, {"a", "add"}, {"e", "edit"}, {"d", "delete"}, {"t", "run all"}}
	case secAPILogs:
		section = [][2]string{
			{"↑↓", "select request"},
			{"tab / shift+tab", "cycle detail section"},
			{"j / k", "scroll section up / down"},
			{"pgup/pgdn b/f/space", "page the section"},
			{"←→", "scroll the URL"},
			{"c", "readable / compact bodies"},
			{"x", "clear the log"},
			{"wheel", "scroll list or body"},
		}
	}
	render := func(rows [][2]string) string {
		width := 0
		for _, r := range rows {
			if w := lipgloss.Width(r[0]); w > width {
				width = w
			}
		}
		var b strings.Builder
		for _, r := range rows {
			gap := strings.Repeat(" ", width-lipgloss.Width(r[0])+2)
			b.WriteString(style.Key.Render(r[0]) + gap + style.Subtitle.Render(r[1]) + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}
	return style.Faint.Render("GLOBAL") + "\n" + render(global) +
		"\n\n" + style.Faint.Render(strings.ToUpper(sectionNames[m.section])) + "\n" + render(section) +
		"\n\n" + style.Faint.Render("? or esc to close")
}

// mainTitle is the panel header: the section name, with a "▸ sub-mode"
// breadcrumb appended when a layered sub-mode (DSL editor, model picker,
// resolution tree) is active, so it's clear where you are.
func (m Model) mainTitle() string {
	base := sectionNames[m.section]
	switch {
	case m.section == secModel && m.editorOpen:
		return base + " ▸ Edit DSL"
	case m.section == secModel && m.modelPicking:
		return base + " ▸ Switch model"
	case m.section == secQuery && m.showRes:
		return base + " ▸ Resolution"
	}
	return base
}

func (m Model) dialogContent() (string, string) {
	switch {
	case m.helpOpen:
		return "Keybindings", m.helpBody()
	case m.formErr != "":
		w, _ := m.sh.DialogSize()
		return "Error", style.Failure.Width(w).Render(m.formErr) +
			"\n\n" + style.Faint.Render("enter or esc to dismiss")
	case m.confirm != nil:
		c := m.confirm
		body := style.Value.Render(c.action+" ") +
			style.Warn.Render(c.subject) +
			style.Value.Render("?")
		if c.detail != "" {
			body += "\n\n" + style.Faint.Render(c.detail)
		}
		body += "\n\n" + style.Faint.Render("y confirm · n / esc / enter cancel")
		return "Confirm", body
	case m.paletteOpen:
		return "Command palette", m.paletteList.View() + "\n" + style.Faint.Render("↑↓ choose · enter go · esc close")
	case m.formKind == formCreateStore:
		return "Create Store", m.form.View() + "\n" + style.Faint.Render("↵ create · esc cancel")
	case m.formKind == formWriteTuple:
		return "Write Tuple", m.form.View() + "\n" + style.Faint.Render("tab move · ctrl+s submit · esc cancel")
	case m.formKind == formWriteAssertion:
		title := "Add Assertion"
		if m.assertEditIdx >= 0 {
			title = "Edit Assertion"
		}
		return title, m.form.View() + "\n\n" + style.Faint.Render("tab move · space toggle · ctrl+s save · esc cancel")
	case m.formKind == formAddProfile:
		return "Add Profile", m.form.View() + "\n" + style.Faint.Render("tab/↑↓ move · ←→ auth method · ctrl+s save · esc cancel")
	case m.formKind == formEditProfile:
		return "Edit Profile", m.form.View() + "\n" + style.Faint.Render("tab/↑↓ move · ←→ auth method · ctrl+s save · esc cancel")
	case m.section == secModel && m.modelPicking:
		inner := m.modelsList.View()
		if m.loading && len(m.models) == 0 {
			inner = m.spinner.View() + " loading models…"
		}
		return "Switch model", inner + "\n" + style.Faint.Render("↑↓ choose · enter load · esc cancel")
	}
	return "", ""
}

// sidebarContext shows the connection status above the nav. The active store
// and model already appear in the bottom status bar, so they aren't repeated
// here.
func (m Model) sidebarContext() []string {
	// A connection failure takes precedence over the store state: it must be
	// visible even when no store is selected (e.g. the server was unreachable on
	// the very first stores load, so nothing could be selected).
	if m.connLost {
		return []string{style.Dot(style.DotError) + " " + style.Failure.Render("connection lost")}
	}
	if m.storeID == "" {
		return []string{style.Dot(style.DotOffline) + " " + style.Faint.Render("disconnected")}
	}
	dot := lipgloss.NewStyle().Foreground(style.Green).Render(style.IconDot)
	return []string{dot + " " + style.Faint.Render("connected")}
}

func (m Model) sidebarNav() []shell.NavItem {
	ic := icons.I()
	sectionIcons := []string{ic.Profile, ic.Store, ic.Model, ic.Tuple, ic.Change, ic.Query, ic.Assert, ic.APILog}
	items := make([]shell.NavItem, len(sectionNames))
	for i, name := range sectionNames {
		it := shell.NavItem{Label: name, Icon: sectionIcons[i], Active: section(i) == m.section}
		switch section(i) {
		case secProfiles:
			if n := len(m.cli.Config.Profiles); n > 0 {
				it.Badge = itoa(n)
			}
		case secStores:
			if len(m.stores) > 0 {
				it.Badge = itoa(len(m.stores))
			}
		case secTuples:
			if len(m.tuples) > 0 {
				it.Badge = itoa(len(m.tuples))
			}
		case secChanges:
			if len(m.changes) > 0 {
				it.Badge = itoa(len(m.changes))
			}
		case secAssertions:
			// Always shown (including 0) — assertions are loaded up front, so 0
			// genuinely means none rather than "not loaded yet".
			it.Badge = itoa(len(m.assertions))
		case secAPILogs:
			if m.recorder != nil {
				if n := m.recorder.Len(); n > 0 {
					it.Badge = itoa(n)
				}
			}
		}
		items[i] = it
	}
	return items
}

// sidebarFooter is intentionally empty: the connection status now sits above
// the nav (sidebarContext) and store/model live in the bottom status bar.
func (m Model) sidebarFooter() string { return "" }

func (m Model) sectionBody() string {
	var body string
	switch m.section {
	case secProfiles:
		w, h := m.contentSize()
		pt, pb := m.profilePreview()
		body = masterDetail(m.profilesList.View(), pt, pb, w, h)
	case secStores:
		if len(m.stores) == 0 {
			switch {
			case m.loading:
				body = m.spinner.View() + " loading stores…"
			case m.connLost:
				// The server is unreachable — don't invite a create that will
				// just fail; point at retry (r) instead.
				body = style.Failure.Render("Can't reach " + m.activeAPIURL() + " — press r to retry")
			default:
				body = style.Faint.Render("No stores yet — press n to create one")
			}
		} else {
			w, h := m.contentSize()
			pt, pb := m.storePreview()
			body = masterDetail(m.storesList.View(), pt, pb, w, h)
		}
	case secModel:
		if m.editorOpen {
			body = m.editorBody()
		} else if m.storeID == "" {
			body = style.Faint.Render("Select a store first — press 2")
		} else if m.loading && len(m.graph.Types) == 0 {
			body = m.spinner.View() + " loading model…"
		} else if len(m.graph.Types) == 0 {
			body = style.Faint.Render("No authorization model in this store")
		} else {
			body = m.graphVP.View()
		}
	case secTuples:
		switch {
		case m.loading && m.storeID != "" && len(m.tuples) == 0:
			body = m.spinner.View() + " loading tuples…"
		case len(m.tuples) == 0:
			body = style.Faint.Render(tupleHint(m.storeID))
		case m.compact:
			body = m.tuplesList.View()
		default:
			w, h := m.contentSize()
			pt, pb := m.tuplePreview()
			body = masterDetail(m.tuplesList.View(), pt, pb, w, h)
		}
	case secChanges:
		switch {
		case m.loading && m.storeID != "" && len(m.changes) == 0:
			body = m.spinner.View() + " loading changes…"
		case len(m.changes) == 0:
			body = style.Faint.Render(changeHint(m.storeID))
		case m.compact:
			body = m.changesList.View()
		default:
			w, h := m.contentSize()
			pt, pb := m.changePreview()
			body = masterDetail(m.changesList.View(), pt, pb, w, h)
		}
	case secQuery:
		body = m.queryBody()
	case secAssertions:
		body = m.assertionsBody()
	case secAPILogs:
		body = m.apiLogsBody()
	}
	if m.fading {
		return style.Faint.Render(ansi.Strip(body))
	}
	return body
}

// splitListWidth returns the width of the list pane in masterDetail's
// list/card split. It is the single source of truth for that 40% share —
// resize() must size the section lists (storesList/tuplesList/changesList)
// to this same width so the list's rendered content matches the box
// masterDetail wraps it in; otherwise lipgloss word-wraps the over-wide
// rows to fit.
func splitListWidth(w int) int { return w * 2 / 5 }

// masterDetail joins a list (40%) and a flat preview pane (60%) into a
// single row. The preview's title sits under a style.SectionHeader rule
// instead of a bordered card; the whole split is borderless and
// background-free, matching the main pane's flat treatment.
func masterDetail(list, title, card string, w, h int) string {
	return masterDetailW(list, title, card, splitListWidth(w), w, h)
}

// masterDetailW is masterDetail with an explicit list-pane width lw, letting a
// section widen the list beyond the default split (the API Logs tab uses it to
// give long URLs more room).
func masterDetailW(list, title, card string, lw, w, h int) string {
	cw := w - lw - 2
	if cw < 10 {
		return list // too narrow: list only
	}
	left := lipgloss.NewStyle().Width(lw).Height(h).Render(list)
	right := card
	if title != "" {
		right = style.SectionHeader(title, cw) + "\n" + card
	}
	right = lipgloss.NewStyle().Width(cw).Height(h).Render(right)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

// keyValueCard renders aligned key/value lines using style.Key/style.Value,
// mirroring output.KeyValues's alignment but returning a string for use as a
// masterDetail preview body instead of writing to an io.Writer.
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

// profilePreview renders the selected profile's title and details for the
// profiles master-detail split, or ("", "") when nothing is selected. Store and
// model are tagged auto (they are managed for you); the token is masked.
func (m Model) profilePreview() (string, string) {
	it, ok := m.profilesList.Selected()
	if !ok {
		return "", ""
	}
	p, ok := m.cli.Config.Get(it.ID)
	if !ok {
		return "", ""
	}
	title := it.ID
	if it.ID == m.cli.Config.Active {
		title += "  · active"
	}
	rows := [][2]string{
		{"API URL", p.APIURL},
		{"Store", autoField(p.StoreID)},
		{"Model", autoField(p.ModelID)},
	}
	rows = append(rows, authPreviewRows(p.ResolvedAuth())...)
	return title, keyValueCard(rows)
}

// authPreviewRows renders a profile's auth for the master-detail preview, with
// secrets masked.
func authPreviewRows(a config.Auth) [][2]string {
	method := a.Method
	if method == "" {
		method = config.AuthNone
	}
	rows := [][2]string{{"Auth", method}}
	switch a.Method {
	case config.AuthAPIToken:
		rows = append(rows, [2]string{"Token", maskToken(a.Token)})
	case config.AuthClientCredentials:
		rows = append(rows,
			[2]string{"Client ID", orDash(a.ClientID)},
			[2]string{"Secret", maskToken(a.ClientSecret)},
			[2]string{"Token URL", orDash(a.TokenURL)},
		)
	case config.AuthPrivateKeyJWT:
		rows = append(rows,
			[2]string{"Client ID", orDash(a.ClientID)},
			[2]string{"Key file", orDash(a.KeyFile)},
			[2]string{"Signing", orDash(a.SigningMethod)},
		)
	}
	return rows
}

// orDash renders a value or an em dash when empty.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// autoField renders an auto-managed profile value (store/model id): the value
// tagged "auto", or a dash when unset.
func autoField(s string) string {
	if s == "" {
		return "— (auto)"
	}
	return s + " (auto)"
}

// maskToken renders a secret as a fixed dot mask (never a plaintext fragment,
// matching the CLI's mask policy), or a dash when unset.
func maskToken(tok string) string {
	if tok == "" {
		return "—"
	}
	return "••••••••"
}

// storePreview renders the selected store's title and details for the
// stores master-detail split, or ("", "") when nothing is selected.
func (m Model) storePreview() (string, string) {
	it, ok := m.storesList.Selected()
	if !ok || it.Index < 0 || it.Index >= len(m.stores) {
		return "", ""
	}
	s := m.stores[it.Index]
	title := s.Name
	if title == "" {
		title = "Store"
	}
	return title, keyValueCard([][2]string{
		{"Name", s.Name},
		{"ID", s.ID},
		{"Created", s.CreatedAt.Format("2006-01-02 15:04:05")},
	})
}

// tuplePreview renders the selected tuple's title and details for the
// tuples master-detail split, or ("", "") when nothing is selected.
func (m Model) tuplePreview() (string, string) {
	it, ok := m.tuplesList.Selected()
	if !ok || it.Index < 0 || it.Index >= len(m.tuples) {
		return "", ""
	}
	t := m.tuples[it.Index]
	rows := [][2]string{
		{"User", t.Key.User},
		{"Relation", t.Key.Relation},
		{"Object", t.Key.Object},
		{"Tuple", fga.FormatUserset(t.Key.Object, t.Key.Relation, t.Key.User)},
	}
	return "Tuple", keyValueCard(append(rows, fga.ConditionRows(t.Key.Condition)...))
}

// changePreview renders the selected change's title and details for the
// changes master-detail split, or ("", "") when nothing is selected.
func (m Model) changePreview() (string, string) {
	it, ok := m.changesList.Selected()
	if !ok || it.Index < 0 || it.Index >= len(m.changes) {
		return "", ""
	}
	c := m.changes[it.Index]
	op := "write"
	if c.Operation == "TUPLE_OPERATION_DELETE" {
		op = "delete"
	}
	rows := [][2]string{
		{"Operation", op},
		{"Timestamp", c.Timestamp.Format("2006-01-02 15:04:05")},
		{"Tuple", fga.FormatUserset(c.TupleKey.Object, c.TupleKey.Relation, c.TupleKey.User)},
	}
	return "Change", keyValueCard(append(rows, fga.ConditionRows(c.TupleKey.Condition)...))
}

func (m Model) editorBody() string {
	w, h := m.contentSize()
	footer := m.cappedFooter(w, h)
	rows := m.editorViewportRows()
	return m.editorPane(w, rows) + "\n" + strings.Join(footer, "\n")
}

// editorFooterLines builds the footer: a wrapped error (a live diagnostic, or
// the apply-time editorErr) followed by the help hint, or just the help hint
// when there is no error. Wrapping to w keeps each line within the content
// width so the shell's per-line truncation never clips it.
func (m Model) editorFooterLines(w int) []string {
	help := style.Faint.Render("ctrl+s apply · esc cancel")
	var errMsg string
	switch {
	case len(m.editorDiags) > 0:
		d := m.editorDiags[0]
		errMsg = fmt.Sprintf("error line %d, col %d: %s", d.Line+1, d.Col+1, d.Msg)
		if len(m.editorDiags) > 1 {
			errMsg += fmt.Sprintf("  (+%d more)", len(m.editorDiags)-1)
		}
	case m.editorErr != "":
		errMsg = "error: " + m.editorErr
	default:
		return []string{help}
	}
	wrapped := style.Failure.Width(w).Render(errMsg)
	return append(strings.Split(wrapped, "\n"), help)
}

// cappedFooter returns the footer lines capped so the editor keeps at least a
// few rows (h-3) even for a very long error.
func (m Model) cappedFooter(w, h int) []string {
	lines := m.editorFooterLines(w)
	maxLines := h - 3
	if maxLines < 1 {
		maxLines = 1
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return lines
}

// editorViewportRows is the number of editor rows rendered: main-area height
// minus footer height. Single source of truth for the pane render and reflow.
func (m Model) editorViewportRows() int {
	w, h := m.contentSize()
	rows := h - len(m.cappedFooter(w, h))
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (m Model) queryBody() string {
	if m.storeID == "" {
		return style.Faint.Render("Select a store first — press 2")
	}

	// Resolution tree takes over the panel when open.
	if m.showRes && m.resTree != nil {
		w, _ := m.contentSize()
		full, path := style.Faint.Render("full tree"), style.Faint.Render("ACL path")
		if m.resPathOnly {
			path = style.Heading.Render("ACL path")
		} else {
			full = style.Heading.Render("full tree")
		}
		head := style.Heading.Render("Resolution") + "  " +
			style.Faint.Render(m.result.vals[0]+" "+m.result.vals[1]+" "+m.result.vals[2]) +
			"   " + full + " " + style.Faint.Render("·") + " " + path +
			"   " + style.Faint.Render("p toggle · ↑↓←→ scroll · r/esc close")
		return head + "\n" + style.SectionHeader("", w) + "\n" + m.resVP.View()
	}

	// Mode selector: every query mode is shown as a segmented strip with the
	// active one filled and the rest faint; `m` cycles between them (the keys
	// live in the status bar, not here). The fields carry their own focus
	// accents, so no extra box is drawn around them — the main panel frames the
	// whole section.
	segs := make([]string, len(queryModes))
	for i, name := range queryModes {
		if i == m.qmode {
			segs[i] = style.Chip(name, style.Secondary, style.BgRaised)
			continue
		}
		segs[i] = lipgloss.NewStyle().Padding(0, 1).Foreground(style.Faintc).Render(name)
	}
	var b strings.Builder
	b.WriteString(strings.Join(segs, " "))
	b.WriteString("\n\n" + m.qform.View())

	w, _ := m.contentSize()
	switch {
	case m.loading:
		b.WriteString("\n\n" + m.spinner.View() + " running…")
	case m.hasResult && m.result.err != nil:
		// Wrap a long error so it stays fully readable instead of running off
		// the right edge. On a wide section it sits in the left half; once the
		// section is too narrow for that to be legible, it wraps full width.
		ew := w
		if w >= 135 {
			ew = w / 2
		}
		b.WriteString("\n\n" + style.Failure.Width(ew).Render("error: "+m.result.err.Error()))
	case m.hasResult:
		tint := style.Faintc
		if r := m.result; m.flash && r.badge {
			tint = style.Green
			if !r.ok {
				tint = style.Red
			}
		}
		b.WriteString("\n\n" + style.SectionHeaderTinted("Result", w, tint) + "\n" + m.renderResult())
	}

	if len(m.history) > 0 {
		b.WriteString("\n\n" + style.SectionHeader("Recent", w) + "\n" + m.historyStrip(w))
	}

	// Chip + form + result + history can add up to more rows than short
	// terminals have available; renderMain doesn't cap its content height, so
	// an over-tall body pushes the status bar off the bottom of the frame.
	// Trim to what actually fits, and flag that content was cut rather than
	// dropping the bottom-most rows silently.
	_, h := m.contentSize()
	lines := strings.Split(b.String(), "\n")
	if len(lines) > h {
		if h > 1 {
			lines = lines[:h-1]
			lines = append(lines, style.Faint.Render("  ⋯ more — enlarge the window to see it"))
		} else {
			lines = lines[:h]
		}
	}
	return strings.Join(lines, "\n")
}

// renderResult renders the current query result inline, with no box. Badge
// results (check) show the ALLOWED/DENIED chip plus latency on one line,
// then the raw detail lines in a faint tone; list-objects/list-users
// results show the title+bullets layout instead. The verdict/flash tint
// that used to color this card's border now colors the "Result" section
// header in queryBody.
func (m Model) renderResult() string {
	msg := m.result
	var body string
	if msg.badge {
		verdict := style.Chip(" "+icons.I().Cross+" DENIED ", style.OnAccent, style.Red)
		if msg.ok {
			verdict = style.Chip(" "+icons.I().Check+" ALLOWED ", style.OnAccent, style.Green)
		}
		meta := style.Faint.Render(itoa(int(msg.ms)) + "ms")
		body = verdict + "  " + meta
		for _, l := range msg.lines {
			body += "\n" + style.Faint.Render(l)
		}
	} else {
		body = lipgloss.NewStyle().Bold(true).Foreground(style.Muted).Render(msg.title)
		for _, l := range msg.lines {
			body += "\n" + style.Bullet() + " " + style.Value.Render(l)
		}
	}
	return body
}

// historyStrip renders up to 5 numbered chips for recent query results,
// newest first: a colored check/cross plus the first field value. Returns ""
// when there is no history yet.
func (m Model) historyStrip(maxW int) string {
	if len(m.history) == 0 {
		return ""
	}
	var chips []string
	used := 0
	for i, h := range m.history {
		// Checks carry an allow/deny verdict (green ✓ / red ✗); list-objects and
		// list-users have no verdict, so they get a neutral marker.
		ic, c := icons.I().Dot, style.Muted
		if h.mode == "check" {
			ic, c = icons.I().Cross, style.Red
			if h.ok {
				ic, c = icons.I().Check, style.Green
			}
		}
		label := itoa(i+1) + " " + lipgloss.NewStyle().Foreground(c).Background(style.BgHighlight).Render(ic)
		chip := style.Chip(label+" "+histNotation(h), style.Muted, style.BgHighlight)
		// Keep only the (newest-first) chips that fit on the one-line strip;
		// otherwise the panel's fitLines hard-truncates it with a stray ellipsis.
		sep := 0
		if len(chips) > 0 {
			sep = 1
		}
		if maxW > 0 && used+sep+lipgloss.Width(chip) > maxW {
			break
		}
		used += sep + lipgloss.Width(chip)
		chips = append(chips, chip)
	}
	return strings.Join(chips, " ")
}

// histNotation renders a recorded query as object#relation@user shorthand. Only
// check queries are recorded (see pushHistory), whose fields are ordered
// [user, relation, object]; the list-mode ordering (object/type first) is
// handled too so the label stays correct if that ever changes.
func histNotation(h histEntry) string {
	switch h.mode {
	case "check":
		return h.vals[2] + "#" + h.vals[1] + "@" + h.vals[0]
	case "list-relations":
		// No single relation to show — it tests every relation on the object.
		return h.vals[0] + " → " + h.vals[1]
	}
	return h.vals[0] + "#" + h.vals[1] + "@" + h.vals[2]
}

func (m Model) assertionsBody() string {
	if m.storeID == "" {
		return style.Faint.Render("select a store first (press 2)")
	}
	if m.loading && len(m.assertions) == 0 {
		return m.spinner.View() + " loading…"
	}
	if len(m.assertions) == 0 {
		return style.Faint.Render("no assertions yet — press a to add one")
	}
	w, h := m.contentSize()
	// Key hints live in the status bar like every other panel; here we show only
	// the pass/fail tally above the list/detail split, and only once a run has
	// produced one (the list is sized one line shorter to make room — see resize).
	var tally string
	if m.assertHasResults() {
		pass, fail := 0, 0
		for _, r := range m.assertResults {
			if !r.ran {
				continue
			}
			if r.pass {
				pass++
			} else {
				fail++
			}
		}
		tally = style.Success.Render(style.IconCheck+" "+itoa(pass)) + "   " +
			style.Failure.Render(style.IconCross+" "+itoa(fail)) + "\n"
		h--
	}
	if m.compact {
		return tally + m.assertionsList.View()
	}
	at, ab := m.assertionPreview()
	return tally + masterDetail(m.assertionsList.View(), at, ab, w, h)
}

// assertionPreview renders the selected assertion's title and detail card for
// the assertions master-detail split, or ("", "") when nothing is selected. The
// contextual tuples and context are surfaced here — they appear nowhere else.
func (m Model) assertionPreview() (string, string) {
	it, ok := m.assertionsList.Selected()
	if !ok || it.Index < 0 || it.Index >= len(m.assertions) {
		return "", ""
	}
	a := m.assertions[it.Index]
	exp := "deny"
	if a.Expectation {
		exp = "allow"
	}
	rows := [][2]string{
		{"Tuple", fga.FormatUserset(a.TupleKey.Object, a.TupleKey.Relation, a.TupleKey.User)},
		{"Expectation", exp},
	}
	if it.Index < len(m.assertResults) && m.assertResults[it.Index].ran {
		r := m.assertResults[it.Index]
		if r.pass {
			rows = append(rows, [2]string{"Result", style.Success.Render(style.IconCheck + " PASS")})
		} else {
			rows = append(rows, [2]string{"Result", style.Failure.Render(style.IconCross+" FAIL") + style.Faint.Render(" · got "+boolWord(r.got))})
		}
	}
	// Each contextual tuple on its own line; only the first carries the label so
	// the rest align beneath it.
	for i, ct := range a.ContextualTuples {
		label := ""
		if i == 0 {
			label = "Contextual Tuples"
		}
		rows = append(rows, [2]string{label, fga.FormatContextualTuple(ct)})
	}
	if ctx := fga.FormatContextJSON(a.Context); ctx != "" {
		rows = append(rows, [2]string{"Context", ctx})
	}
	return "Assertion", keyValueCard(rows)
}

// statusKeys returns the right-aligned keycap hints for the current state.
// Quit ("q") and section-switch ("tab") are only listed where those keys
// actually work: takeover forms, the model editor, and the query form all
// capture every keypress, so those states omit them.
func (m Model) statusKeys() []string {
	// Sub-editors that capture every key advertise only their own bindings.
	switch {
	case m.formErr != "":
		return []string{"↵ dismiss", "esc"}
	case m.formKind == formCreateStore:
		// Single-field form: Enter submits (ctrl+s also works); match the dialog hint.
		return []string{"↵ create", "esc cancel"}
	case m.formKind != formNone:
		return []string{"ctrl+s save", "esc cancel"}
	case m.section == secModel && m.editorOpen:
		return []string{"ctrl+s apply", "esc cancel"}
	case m.section == secModel && m.modelPicking:
		return []string{"↑↓ browse", "↵ select", "esc"}
	case m.section == secQuery && m.editing:
		// Enter advances between fields and only runs on the last one; ctrl+s
		// runs from anywhere. Spell both out so the hint matches reality.
		return []string{"↑↓ field", "tab mode", "↵ next/run", "ctrl+s run", "esc"}
	case m.section == secQuery && m.showRes:
		return []string{"↑↓←→ scroll", "p ACL path", "r close", "esc"}
	}
	// Sidebar (tab selection) focus: browse tabs, enter to descend.
	if m.focus == shell.FocusSidebar {
		return []string{"↑↓/tab", "↵ open", "1-8 jump", "ctrl+k palette", "q quit"}
	}
	// Panel focus: section-specific keys, esc back to the tabs.
	switch m.section {
	case secProfiles:
		return []string{"↑↓", "↵ switch", "n add", "e edit", "d delete", "esc"}
	case secStores:
		return []string{"↑↓", "/ filter", "↵ select", "n new", "d delete", "r reload", "esc"}
	case secModel:
		return []string{"↑↓/hjkl pan", "e edit DSL", "m switch", "r reload", "esc"}
	case secTuples:
		return []string{"↑↓", "/ filter", "a add", "d delete", "r reload", m.compactHint(), "esc"}
	case secChanges:
		return []string{"↑↓", "/ filter", "r reload", m.compactHint(), "esc"}
	case secQuery:
		return []string{"i/↵ edit", "tab mode", "1-5 rerun", "r resolve", "esc"}
	case secAssertions:
		return []string{"↑↓", "↵ run", "a add", "e edit", "d delete", "t run all", m.compactHint(), "esc"}
	case secAPILogs:
		return []string{"↑↓ list", "tab section", "j/k scroll", "←→ url", "c fmt", "x clear", "esc"}
	}
	return nil
}

// compactHint labels the "v" view toggle for the status bar: it offers the
// mode you would switch to, not the one you are in.
func (m Model) compactHint() string {
	if m.compact {
		return "v detail"
	}
	return "v compact"
}

// sectionStatus is the footer's left-hand message: a count of the current
// section's items. Profiles, Model and Tuple Queries have nothing to count and
// show no message. Transient feedback (errors, successes) rides on toasts, and
// the spinner marks in-flight loads.
func (m Model) sectionStatus() string {
	switch m.section {
	case secStores:
		return plural(len(m.stores), "store")
	case secTuples:
		if m.tuplesCapped {
			return fmt.Sprintf("first %d tuples (more exist)", len(m.tuples))
		}
		return plural(len(m.tuples), "tuple")
	case secChanges:
		if m.changesCapped {
			return fmt.Sprintf("first %d changes (more exist)", len(m.changes))
		}
		return plural(len(m.changes), "change")
	case secAssertions:
		return plural(len(m.assertions), "assertion")
	}
	return ""
}

func itoa(n int) string { return strconv.Itoa(n) }

// --- helpers ---

func tupleHint(storeID string) string {
	if storeID == "" {
		return "Select a store first — press 2"
	}
	return "No tuples yet — press a to add one"
}

func changeHint(storeID string) string {
	if storeID == "" {
		return "Select a store first — press 2"
	}
	return "No changes recorded yet"
}
