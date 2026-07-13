package playground

import (
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/dsl"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/list"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// reducedMotion reports whether ambient animation (the gradient drift) should be
// suppressed: an explicit opt-out, or an environment that makes constant
// repaints costly or ugly (dumb terminal, NO_COLOR). This lets users on SSH or
// battery stop the perpetual repaint the drift would otherwise cause.
func reducedMotion() bool {
	return os.Getenv("OFGA_REDUCED_MOTION") != "" ||
		os.Getenv("NO_COLOR") != "" ||
		os.Getenv("TERM") == "dumb"
}

type pendingAction struct{ runAssertions bool }

var pending pendingAction

// entranceTickMsg drives the launch animation: the sidebar springs in from
// the left while the main pane materializes. Stops when settled.
type entranceTickMsg struct{}

func entranceTick() tea.Cmd {
	return tea.Tick(time.Millisecond*33, func(time.Time) tea.Msg {
		return entranceTickMsg{}
	})
}

// driftTickMsg advances the ambient gradient drift on the wordmark and the
// active nav pill. It re-arms continuously (the drift is ambience by design) on
// capable, motion-friendly terminals; it never starts on the mono rung and
// stops when reducedMotion() is set, so SSH/battery users can opt out.
type driftTickMsg struct{}

func driftTick() tea.Cmd {
	return tea.Tick(time.Millisecond*200, func(time.Time) tea.Msg {
		return driftTickMsg{}
	})
}

// fadeMsg fires after a section change to materialize the incoming frame from
// its ghost preview. Does not re-arm — fires exactly once per section switch.
type fadeMsg struct{}

func fadeTick() tea.Cmd {
	return tea.Tick(70*time.Millisecond, func(time.Time) tea.Msg {
		return fadeMsg{}
	})
}

// flashMsg ends the one-frame verdict-color flash on the Result
// section-header rule. Does not re-arm — fires exactly once per badge result.
type flashMsg struct{}

func flashTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
		return flashMsg{}
	})
}

// Update is the central dispatcher. It forwards every message to the toast
// model first (so its expiry timer advances regardless of which branch below
// handles the message), then dispatches as before.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	toastCmd := m.toasts.Update(msg)
	wasLoading := m.loading
	nm, cmd := m.dispatch(msg)
	m2 := nm.(Model)
	// The spinner only animates while loading. Restart its tick loop once when a
	// load begins; the loop stops itself when loading ends (see spinner.TickMsg),
	// so the UI isn't redrawn forever by a hidden spinner.
	if m2.loading && !wasLoading && !m2.spinnerRunning {
		m2.spinnerRunning = true
		cmd = tea.Batch(cmd, m2.spinner.Tick)
	}
	return m2, tea.Batch(toastCmd, cmd)
}

func (m Model) dispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		return m.handleWheel(msg)
	case tea.MouseClickMsg:
		return m.handleClick(msg)
	case tea.WindowSizeMsg:
		// bubbletea sends the initial size report at startup, before Init()
		// runs — that message also flips m.ready (which gates all
		// rendering), so snapping the entrance here unconditionally would
		// kill it before the first renderable frame. Only a genuine
		// mid-flight resize (m.ready already true) snaps it.
		if m.ready {
			m.entering = false
			m.entranceFrac = 0
		}
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		m.ready = true
		return m, nil

	case spinner.TickMsg:
		if !m.loading {
			// Nothing to animate — let the tick loop end so the UI can idle.
			m.spinnerRunning = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case storesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			return m, m.toastErr("", msg.err)
		}
		m.connLost = false
		m.stores = msg.stores
		for _, s := range m.stores {
			if s.ID == m.storeID {
				m.storeName = s.Name
			}
		}
		m.populateStores()
		m.status = plural(len(msg.stores), "store")
		// First run with nothing selected yet: adopt the first store (and persist
		// it) so the playground opens on a live store and the config records it.
		if m.storeID == "" && len(m.stores) > 0 {
			return m, m.selectStore(m.stores[0])
		}
		return m, nil

	case modelLoadedMsg:
		m.loading = false
		if msg.err != nil {
			cmd := m.toastErr("model", msg.err)
			if !m.connLost {
				m.graph = fga.Graph{}
				m.graphVP.SetContent(style.Faint.Render("no model: " + errStr(msg.err)))
			}
			return m, cmd
		}
		m.connLost = false
		m.modelID = msg.modelID
		// ReadLatest flags it directly; a picked model is latest only if it is
		// the newest in the (already loaded) models list.
		m.modelIsLatest = msg.latest || (len(m.models) > 0 && msg.modelID == m.models[0].ID)
		m.graph = msg.graph
		m.modelDSL = msg.dsl
		m.graphVP.SetContent(m.graph.RenderDiagram())
		m.resetGraphScroll()
		m.persistModel()
		m.status = "model " + short(msg.modelID) + " · " + m.graph.Summary()
		return m, nil

	case modelAppliedMsg:
		if msg.err != nil {
			m.connLost = isConnErr(msg.err)
			m.editorErr = msg.err.Error()
			// While the editor is open the footer already shows this error; a
			// toast would duplicate it. Toast only when the error would otherwise
			// be invisible (editor closed).
			if m.editorOpen {
				return m, nil
			}
			return m, m.toasts.Push(toast.Error, "apply model: "+m.editorErr)
		}
		m.connLost = false
		m.editorOpen = false
		m.editor.Blur()
		m.status = "model applied"
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status), loadModelCmd(m.ctx, m.client, m.storeID))

	case modelsListedMsg:
		m.loading = false
		if msg.err != nil {
			return m, m.toastErr("models", msg.err)
		}
		m.connLost = false
		m.models = msg.models
		m.populateModels()
		return m, nil

	case tuplesLoadedMsg:
		if msg.err != nil {
			return m, m.toastErr("tuples", msg.err)
		}
		m.connLost = false
		m.tuples = msg.tuples
		m.tuplesCapped = msg.capped
		m.populateTuples()
		return m, nil

	case changesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			return m, m.toastErr("changes", msg.err)
		}
		m.connLost = false
		m.changes = msg.changes
		m.changesCapped = msg.capped
		m.populateChanges()
		m.status = plural(len(msg.changes), "change")
		return m, nil

	case assertionsLoadedMsg:
		m.loading = false
		m.assertModelID = msg.modelID
		if msg.err != nil {
			cmd := m.toastErr("assertions", msg.err)
			if !m.connLost {
				m.assertions = nil
			}
			return m, cmd
		}
		m.connLost = false
		m.assertions = msg.assertions
		m.assertResults = nil
		m.assertSummary = ""
		m.populateAssertions()
		m.resize()
		m.status = plural(len(msg.assertions), "assertion")
		if pending.runAssertions && len(m.assertions) > 0 {
			pending.runAssertions = false
			m.loading = true
			m.status = "running assertions…"
			return m, runAssertionsCmd(m.ctx, m.client, m.storeID, m.assertModelID, m.assertions)
		}
		return m, nil

	case assertTestMsg:
		m.loading = false
		if msg.err != nil {
			return m, m.toastErr("assertion test", msg.err)
		}
		m.connLost = false
		m.assertResults = msg.results
		m.assertSummary = strconv.Itoa(msg.passed) + "/" + strconv.Itoa(msg.total) + " passed"
		m.populateAssertions()
		m.resize()
		m.status = m.assertSummary
		return m, m.toasts.Push(toast.Success, m.status)

	case assertOneMsg:
		if msg.err != nil {
			return m, m.toastErr("assertion", msg.err)
		}
		m.connLost = false
		if len(m.assertResults) != len(m.assertions) {
			m.assertResults = make([]assertResult, len(m.assertions))
		}
		if msg.idx < len(m.assertResults) {
			m.assertResults[msg.idx] = msg.result
		}
		m.populateAssertions()
		m.resize()
		m.status = assertResultWord(msg.result)
		return m, nil

	case assertionsWrittenMsg:
		if msg.err != nil {
			// Surface the API error as a centered modal (dismissed with
			// enter/esc), not in the footer.
			m.connLost = isConnErr(msg.err)
			m.status = ""
			m.formErr = errStr(msg.err)
			return m, nil
		}
		m.connLost = false
		m.status = "assertions saved"
		// Reload to confirm the write and reset the per-row badges.
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status),
			loadAssertionsCmd(m.ctx, m.client, m.storeID, msg.modelID))

	case resolutionMsg:
		m.loading = false
		if msg.err != nil {
			return m, m.toastErr("resolution", msg.err)
		}
		m.connLost = false
		m.resTree = msg.root
		m.showRes = true
		m.refreshResVP()
		m.resVP.SetYOffset(0)
		m.status = "resolution tree"
		return m, nil

	case storeCreatedMsg:
		if msg.err != nil {
			return m, m.toastErr("create store", msg.err)
		}
		m.connLost = false
		m.status = "created store " + msg.store.Name
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status), m.selectStore(msg.store), loadStoresCmd(m.ctx, m.client))

	case storeDeletedMsg:
		m.loading = false
		if msg.err != nil {
			return m, m.toastErr("delete store", msg.err)
		}
		m.connLost = false
		m.status = "store deleted"
		// If the active store was deleted, clear it (a reload then auto-selects
		// the first remaining store, or leaves the playground store-less).
		if msg.id == m.storeID {
			m.storeID, m.storeName, m.modelID = "", "", ""
			m.modelIsLatest = false
			m.graph = fga.Graph{}
			m.models, m.tuples, m.changes, m.assertions, m.assertResults = nil, nil, nil, nil, nil
			m.history, m.hasResult = nil, false
			m.persistStore()
		}
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status), loadStoresCmd(m.ctx, m.client))

	case tupleWrittenMsg:
		if msg.err != nil {
			return m, m.toastErr("tuple", msg.err)
		}
		m.connLost = false
		verb := "wrote"
		if msg.deleted {
			verb = "deleted"
		}
		m.status = verb + " " + msg.label
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status), loadTuplesCmd(m.ctx, m.client, m.storeID))

	case queryResultMsg:
		m.loading = false
		m.hasResult = true
		m.result = msg
		// A fresh result invalidates any open resolution tree.
		m.showRes = false
		m.resTree = nil
		if msg.err != nil {
			m.connLost = isConnErr(msg.err)
			// The panel body shows the full error persistently (until the next
			// query); no transient toast, so every query error looks the same.
			m.status = "query failed"
			return m, nil
		}
		m.connLost = false
		m.status = "query complete"
		cmds := []tea.Cmd{m.toasts.Push(toast.Success, m.status)}
		// Record every query — check, list-objects and list-users — so all of
		// them are rerunnable from the Recent strip.
		m.pushHistory(histEntry{mode: msg.mode, vals: msg.vals, ok: msg.ok, ms: msg.ms})
		// Only a check carries an allow/deny verdict, so only it flashes.
		if msg.badge {
			m.flash = true
			cmds = append(cmds, flashTick())
		}
		return m, tea.Batch(cmds...)

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case graphTickMsg:
		return m.advanceGraphScroll()

	case entranceTickMsg:
		if !m.entering {
			return m, nil
		}
		m.entranceFrac, m.entranceVel = m.entranceSpring.Update(m.entranceFrac, m.entranceVel, 0)
		if m.entranceFrac < 0.01 {
			m.entranceFrac = 0
			m.entering = false
			return m, nil
		}
		return m, entranceTick()

	case driftTickMsg:
		m.drift += 0.02
		if m.drift >= 1 {
			m.drift -= 1
		}
		if reducedMotion() {
			return m, nil
		}
		return m, driftTick()

	case fadeMsg:
		m.fading = false
		return m, nil

	case flashMsg:
		m.flash = false
		return m, nil

	default:
		// Field cursors blink via their own (non-key) messages. An active form
		// must see every message, not just key presses, or the focused input's
		// cursor never blinks.
		if m.editorOpen {
			var cmd tea.Cmd
			m.editor, cmd = m.editor.Update(msg)
			m.refreshEditorDiagnostics()
			m.reflowEditorScroll()
			return m, cmd
		}
		if m.formKind != formNone {
			return m.advanceTakeoverForm(msg)
		}
		if m.section == secQuery && m.editing {
			return m.advanceQueryForm(msg)
		}
	}
	return m, nil
}

// toastErr surfaces a failed API call as a transient toast (and flags a
// possible connection loss), deliberately keeping the raw error out of the
// footer status line.
func (m *Model) toastErr(label string, err error) tea.Cmd {
	m.connLost = isConnErr(err)
	m.status = ""
	detail := errStr(err)
	if label != "" {
		detail = label + ": " + detail
	}
	return m.toasts.Push(toast.Error, detail)
}

// handleWheel routes mouse-wheel scrolling to the active scrollable pane: the
// model graph and the resolution tree. Other panes (short lists) ignore it.
// sectionList returns the current section's primary list (or nil) and the number
// of body rows rendered above it, for mouse row hit-testing.
func (m Model) sectionList() (*list.List, int) {
	switch m.section {
	case secProfiles:
		return m.profilesList, 0
	case secStores:
		return m.storesList, 0
	case secTuples:
		return m.tuplesList, 0
	case secChanges:
		return m.changesList, 0
	case secAssertions:
		off := 0
		if m.assertHasResults() {
			off = 1
		}
		return m.assertionsList, off
	}
	return nil, 0
}

// footerKeyToken extracts the single actionable key from a footer hint like
// "n new" or "↵ run", or "" when the hint isn't a single clickable key
// (e.g. "↑↓ move", "hjkl pan").
func footerKeyToken(hint string) string {
	fields := strings.Fields(hint)
	if len(fields) == 0 {
		return ""
	}
	switch fields[0] {
	case "esc":
		return "esc"
	case "tab":
		return "tab"
	case "ctrl+s":
		return "ctrl+s"
	case "↵":
		return "enter"
	case "?":
		return "?"
	}
	if r := []rune(fields[0]); len(r) == 1 {
		c := r[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return string(c)
		}
	}
	return ""
}

// keyMsg builds a KeyPressMsg for a token so a click can re-enter handleKey.
func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C is the universal quit. Honor it before any overlay, form, editor,
	// or list-filter branch below can swallow the key (in raw mode Ctrl+C
	// arrives as a key press, not a signal), so the user is never trapped.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	// The help overlay captures input until dismissed.
	if m.helpOpen {
		switch msg.String() {
		case "?", "esc", "q", "enter":
			m.helpOpen = false
		}
		return m, nil
	}

	// A blocking error dialog swallows input; esc or enter dismisses it.
	if m.formErr != "" {
		switch msg.String() {
		case "esc", "enter":
			m.formErr = ""
		}
		return m, nil
	}

	// The delete-confirmation modal captures input until answered. Every
	// destructive action (store, tuple, assertion, profile) routes through here.
	if m.confirm != nil {
		switch msg.String() {
		case "y":
			run := m.confirm.run
			m.confirm = nil
			return m, run(&m)
		case "esc", "n", "enter":
			// Enter cancels (matching the CLI's [y/N] default) so a reflexive
			// Enter can't permanently delete a store and all its data.
			m.confirm = nil
		}
		return m, nil
	}
	if m.paletteOpen {
		switch msg.String() {
		case "esc", "ctrl+k":
			m.paletteOpen = false
			return m, nil
		case "enter":
			if it, ok := m.paletteList.Selected(); ok {
				m.paletteOpen = false
				m.section = section(it.Index)
				m.focus = shell.FocusSidebar
				m.fading = true
				nm, cmd := m.onEnterSection()
				return nm, tea.Batch(cmd, fadeTick())
			}
		}
		cmd := m.paletteList.Update(msg)
		return m, cmd
	}

	// Editor open: route all keys to it (except esc/ctrl+s which we handle).
	if m.editorOpen {
		switch msg.String() {
		case "esc":
			// Guard against discarding unsaved DSL edits on a stray Esc: confirm
			// first when the buffer differs from what was loaded.
			baseline := m.modelDSL
			if baseline == "" {
				baseline = modelTemplate
			}
			if m.editor.Value() != baseline {
				m.confirm = &confirmAction{
					action:  "Discard model edits",
					subject: "unsaved changes to the model DSL",
					run: func(m *Model) tea.Cmd {
						m.editorOpen = false
						m.editorErr = ""
						m.editor.Blur()
						return nil
					},
				}
				return m, nil
			}
			m.editorOpen = false
			m.editorErr = ""
			m.editor.Blur()
			return m, nil
		case "ctrl+s":
			m.editorErr = ""
			m.status = "applying model…"
			return m, applyModelCmd(m.ctx, m.client, m.storeID, m.editor.Value())
		}
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		m.refreshEditorDiagnostics()
		m.reflowEditorScroll()
		return m, cmd
	}

	// Active form takeovers capture all keys.
	if m.formKind != formNone {
		return m.handleTakeoverForm(msg)
	}
	if m.section == secQuery && m.editing {
		return m.handleQueryForm(msg)
	}
	// The model-switcher overlay captures its own keys (including Esc, which
	// closes the overlay rather than the panel) so it must be handled before
	// the focus routing below.
	if m.section == secModel && m.modelPicking {
		return m.handleModelPicker(msg)
	}

	// While a list is filtering, route everything to it.
	if lst := m.activeList(); lst != nil && lst.SettingFilter() {
		cmd := lst.Update(msg)
		return m, cmd
	}

	key := msg.String()
	// Keys that work in either focus mode. (Ctrl+C is handled at the top of
	// handleKey so it can never be swallowed by an overlay.)
	switch key {
	case "ctrl+k":
		m.paletteOpen = true
		return m, nil
	case "?":
		m.helpOpen = true
		return m, nil
	}

	if m.focus == shell.FocusSidebar {
		return m.handleSidebarKey(key)
	}
	// Panel focus: Esc returns to the sidebar (tab selection); every other key
	// is section-specific logic. The query resolution view is a sub-mode, so Esc
	// closes it first (layered).
	if key == "esc" {
		if m.section == secQuery && m.showRes {
			m.showRes = false
			return m, nil
		}
		m.focus = shell.FocusSidebar
		return m, nil
	}
	return m.handleSectionKey(key, msg)
}

// handleSidebarKey handles keys while the sidebar (tab selection) owns focus:
// ↑↓ / tab / shift+tab / ←→ move between tabs, digits 1-6 jump, enter descends
// into the panel, q quits.
func (m Model) handleSidebarKey(key string) (tea.Model, tea.Cmd) {
	n := section(len(sectionNames))
	switch key {
	case "q":
		return m, tea.Quit
	case "down", "tab", "right", "j", "l":
		return m.gotoSection((m.section + 1) % n)
	case "up", "shift+tab", "left", "k", "h":
		return m.gotoSection((m.section + n - 1) % n)
	case "1", "2", "3", "4", "5", "6", "7":
		return m.gotoSection(section(key[0] - '1'))
	case "enter":
		m.focus = shell.FocusPanel
		m.fading = true
		nm, cmd := m.onEnterSection()
		return nm, tea.Batch(cmd, fadeTick())
	case "n", "a", "e", "d":
		// Empty-state call-to-action keys ("press n to create one") are panel
		// actions. Descend into the panel and replay the key so a new user's very
		// first keystroke works, instead of being a silent no-op on the sidebar.
		m.focus = shell.FocusPanel
		nm, cmd := m.onEnterSection()
		mm, ok := nm.(Model)
		if !ok {
			return nm, cmd
		}
		m2, cmd2 := mm.handleSectionKey(key, keyMsg(key))
		return m2, tea.Batch(cmd, cmd2)
	}
	return m, nil
}

// gotoSection moves the highlighted tab (staying in sidebar focus) and plays
// the section-change fade, lazy-loading the target section's data.
func (m Model) gotoSection(to section) (tea.Model, tea.Cmd) {
	m.section = to
	m.fading = true
	nm, cmd := m.onEnterSection()
	return nm, tea.Batch(cmd, fadeTick())
}

// handleModelPicker handles keys while the Model section's model-switcher
// overlay is open: enter loads the picked model, esc closes the overlay.
func (m Model) handleModelPicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if it, ok := m.modelsList.Selected(); ok && it.Index < len(m.models) {
			m.modelPicking = false
			id := m.models[it.Index].ID
			m.status = "loading model " + short(id) + "…"
			return m, loadModelByIDCmd(m.ctx, m.client, m.storeID, id)
		}
		return m, nil
	case "esc":
		m.modelPicking = false
		return m, nil
	}
	cmd := m.modelsList.Update(msg)
	return m, cmd
}

// onEnterSection lazy-loads data when first visiting a section.
func (m Model) onEnterSection() (tea.Model, tea.Cmd) {
	m.modelPicking = false
	switch m.section {
	case secChanges:
		if m.storeID != "" && len(m.changes) == 0 {
			m.loading = true
			return m, loadChangesCmd(m.ctx, m.client, m.storeID)
		}
	case secAssertions:
		// Assertions are stored per authorization model, so reload them when the
		// tab is first opened or the selected model has changed since they were
		// loaded — otherwise the tab would keep running the first model's set
		// against a now-different selection. (Skip the model check when no model
		// is resolved yet, to avoid reloading on every entry.)
		if m.storeID != "" && (m.assertions == nil || (m.modelID != "" && m.assertModelID != m.modelID)) {
			m.loading = true
			return m, loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID)
		}
	case secQuery:
		// Descending into the panel starts in the first field, ready to type
		// (same as arriving via tab). Browsing tabs in the sidebar keeps
		// FocusSidebar, so it must not begin editing.
		if m.focus == shell.FocusPanel {
			return m, m.enterQueryEdit()
		}
	}
	return m, nil
}

// activeList returns the list backing the current section, or nil.
func (m *Model) activeList() *list.List {
	switch m.section {
	case secProfiles:
		return m.profilesList
	case secStores:
		return m.storesList
	case secModel:
		if m.modelPicking {
			return m.modelsList
		}
	case secTuples:
		return m.tuplesList
	case secChanges:
		return m.changesList
	case secAssertions:
		return m.assertionsList
	}
	return nil
}

// --- forms ---

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// refreshEditorDiagnostics re-parses the editor buffer for diagnostics, but
// only when the buffer actually changed since the last parse (cursor-blink
// and other no-op updates should not trigger a re-parse).
// It first checks for syntax errors; if there are none, it runs the semantic
// check for undefined types. Syntax errors take precedence.
func (m *Model) refreshEditorDiagnostics() {
	v := m.editor.Value()
	if v == m.lastEditorDSL {
		return
	}
	m.lastEditorDSL = v
	m.editorDiags = dsl.Diagnostics(v)
	// Only run semantic checks if syntax is valid (token stream is reliable)
	if len(m.editorDiags) == 0 {
		m.editorDiags = dsl.UndefinedTypeDiagnostics(v)
	}
}

// reflowEditorScroll adjusts editorTop so the cursor's logical line stays
// within the visible window. We manage vertical scroll ourselves because the
// textarea's ScrollYOffset is not reliable under our no-wrap config.
func (m *Model) reflowEditorScroll() {
	h := m.editorViewportRows()
	if h < 1 {
		h = 1
	}
	row := m.editor.Line()
	switch {
	case row < m.editorTop:
		m.editorTop = row
	case row >= m.editorTop+h:
		m.editorTop = row - h + 1
	}
	if m.editorTop < 0 {
		m.editorTop = 0
	}
}
