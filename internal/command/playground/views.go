package playground

import (
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
	m.sh.SetMain(sectionNames[m.section], m.sectionBody())
	st := shell.Status{Left: m.sectionStatus(), Keys: m.statusKeys()}
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
		if m.assertErr != "" || m.confirmStoreID != "" {
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
func (m Model) dialogContent() (string, string) {
	switch {
	case m.assertErr != "":
		w, _ := m.sh.DialogSize()
		return "Error", style.Failure.Width(w).Render(m.assertErr) +
			"\n\n" + style.Faint.Render("enter or esc to dismiss")
	case m.confirmStoreID != "":
		body := style.Value.Render("Delete store ") + style.Bold.Render(m.confirmStoreName) + style.Value.Render("?") +
			"\n\n" + style.Failure.Render(style.IconCross+" This permanently deletes the store and all its data.") +
			"\n\n" + style.Faint.Render("enter / y confirm · esc / n cancel")
		return "Delete Store", body
	case m.paletteOpen:
		return "Command palette", m.paletteList.View() + "\n" + style.Faint.Render("↑↓ choose · enter go · esc close")
	case m.formKind == formCreateStore:
		return "Create Store", m.form.View() + "\n" + style.Faint.Render("enter submit · esc cancel")
	case m.formKind == formWriteTuple:
		return "Write Tuple", m.form.View() + "\n" + style.Faint.Render("enter submit · esc cancel")
	case m.formKind == formWriteAssertion:
		title := "Add Assertion"
		if m.assertEditIdx >= 0 {
			title = "Edit Assertion"
		}
		return title, m.form.View() + "\n\n" + style.Faint.Render("tab move · space toggle · enter save · esc cancel")
	case m.formKind == formAddProfile:
		return "Add Profile", m.form.View() + "\n" + style.Faint.Render("tab/↑↓ move · ←→ auth method · enter save · esc cancel")
	case m.formKind == formEditProfile:
		return "Edit Profile", m.form.View() + "\n" + style.Faint.Render("tab/↑↓ move · ←→ auth method · enter save · esc cancel")
	case m.section == secModel && m.modelPicking:
		return "Switch model", m.modelsList.View() + "\n" + style.Faint.Render("↑↓ choose · enter load · esc cancel")
	}
	return "", ""
}

// sidebarContext shows the connection status above the nav. The active store
// and model already appear in the bottom status bar, so they aren't repeated
// here.
func (m Model) sidebarContext() []string {
	if m.storeID == "" {
		return []string{style.Dot(style.DotOffline) + " " + style.Faint.Render("disconnected")}
	}
	if m.connLost {
		return []string{style.Dot(style.DotError) + " " + style.Failure.Render("connection lost")}
	}
	dot := lipgloss.NewStyle().Foreground(style.Green).Render(style.IconDot)
	return []string{dot + " " + style.Faint.Render("connected")}
}

func (m Model) sidebarNav() []shell.NavItem {
	ic := icons.I()
	sectionIcons := []string{ic.Profile, ic.Store, ic.Model, ic.Tuple, ic.Change, ic.Query, ic.Assert}
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
			body = style.Faint.Render("No stores yet — press n to create one")
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
		} else if len(m.graph.Types) == 0 {
			body = style.Faint.Render("No authorization model in this store")
		} else {
			body = m.graphVP.View()
		}
	case secTuples:
		if len(m.tuples) == 0 {
			body = style.Faint.Render(tupleHint(m.storeID))
		} else {
			w, h := m.contentSize()
			pt, pb := m.tuplePreview()
			body = masterDetail(m.tuplesList.View(), pt, pb, w, h)
		}
	case secChanges:
		if len(m.changes) == 0 {
			body = style.Faint.Render(changeHint(m.storeID))
		} else {
			w, h := m.contentSize()
			pt, pb := m.changePreview()
			body = masterDetail(m.changesList.View(), pt, pb, w, h)
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
	lw := splitListWidth(w)
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

// maskToken renders an API token with only its first and last few characters,
// or a dash when unset.
func maskToken(tok string) string {
	if tok == "" {
		return "—"
	}
	if len(tok) <= 8 {
		return "••••"
	}
	return tok[:3] + "…" + tok[len(tok)-3:]
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
	return "Tuple", keyValueCard([][2]string{
		{"User", t.Key.User},
		{"Relation", t.Key.Relation},
		{"Object", t.Key.Object},
		{"Tuple", fga.FormatTuple(t.Key)},
	})
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
	return "Change", keyValueCard([][2]string{
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
	// Trim to what actually fits, clipping the bottom-most content.
	_, h := m.contentSize()
	lines := strings.Split(b.String(), "\n")
	if len(lines) > h {
		lines = lines[:h]
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
	if h.mode == "check" {
		return h.vals[2] + "#" + h.vals[1] + "@" + h.vals[0]
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
	// Key hints live in the status bar like every other panel; here we show only
	// the pass/fail tally, and only once a run has produced one.
	if !m.assertHasResults() {
		return m.assertionsList.View()
	}
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
	tally := style.Success.Render(style.IconCheck+" "+itoa(pass)) + "   " +
		style.Failure.Render(style.IconCross+" "+itoa(fail))
	return tally + "\n" + m.assertionsList.View()
}

// statusKeys returns the right-aligned keycap hints for the current state.
// Quit ("q") and section-switch ("tab") are only listed where those keys
// actually work: takeover forms, the model editor, and the query form all
// capture every keypress, so those states omit them.
func (m Model) statusKeys() []string {
	// Sub-editors that capture every key advertise only their own bindings.
	switch {
	case m.assertErr != "":
		return []string{"↵ dismiss", "esc"}
	case m.formKind != formNone:
		return []string{"↵ save", "esc cancel"}
	case m.section == secModel && m.editorOpen:
		return []string{"ctrl+s apply", "esc cancel"}
	case m.section == secModel && m.modelPicking:
		return []string{"↑↓ browse", "↵ select", "esc"}
	case m.section == secQuery && m.editing:
		return []string{"↑↓ field", "tab mode", "↵ run", "esc"}
	case m.section == secQuery && m.showRes:
		return []string{"↑↓←→ scroll", "p ACL path", "r close", "esc"}
	}
	// Sidebar (tab selection) focus: browse tabs, enter to descend.
	if m.focus == shell.FocusSidebar {
		return []string{"↑↓/tab", "↵ open", "1-7 jump", "ctrl+k palette", "q quit"}
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
		return []string{"↑↓", "/ filter", "a add", "d delete", "r reload", "esc"}
	case secChanges:
		return []string{"↑↓", "/ filter", "r reload", "esc"}
	case secQuery:
		return []string{"i/↵ edit", "tab mode", "1-5 rerun", "r resolve", "esc"}
	case secAssertions:
		return []string{"↑↓", "↵ run", "a add", "e edit", "d delete", "t run all", "esc"}
	}
	return nil
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
		return plural(len(m.tuples), "tuple")
	case secChanges:
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
