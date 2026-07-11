package playground

import (
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/list"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

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
// active nav pill. Unlike every other timer in this package it re-arms
// forever: the drift is ambience by design (documented spec exception). It
// is never started on the mono rung.
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
	nm, cmd := m.dispatch(msg)
	return nm, tea.Batch(toastCmd, cmd)
}

func (m Model) dispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
			return m, m.toasts.Push(toast.Error, "apply model: "+m.editorErr)
		}
		m.connLost = false
		m.editorOpen = false
		m.editor.Blur()
		m.status = "model applied"
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status), loadModelCmd(m.ctx, m.client, m.storeID))

	case modelsListedMsg:
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
		m.populateTuples()
		return m, nil

	case changesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			return m, m.toastErr("changes", msg.err)
		}
		m.connLost = false
		m.changes = msg.changes
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
			m.assertErr = errStr(msg.err)
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

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// A blocking error dialog swallows input; esc or enter dismisses it.
	if m.assertErr != "" {
		switch msg.String() {
		case "esc", "enter":
			m.assertErr = ""
		}
		return m, nil
	}

	// The store delete-confirmation modal captures input until answered.
	if m.confirmStoreID != "" {
		switch msg.String() {
		case "enter", "y":
			id := m.confirmStoreID
			m.confirmStoreID, m.confirmStoreName = "", ""
			m.loading = true
			m.status = "deleting store…"
			return m, deleteStoreCmd(m.ctx, m.client, id)
		case "esc", "n":
			m.confirmStoreID, m.confirmStoreName = "", ""
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
	// Keys that work in either focus mode.
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "ctrl+k":
		m.paletteOpen = true
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
	case "down", "tab", "right":
		return m.gotoSection((m.section + 1) % n)
	case "up", "shift+tab", "left":
		return m.gotoSection((m.section + n - 1) % n)
	case "1", "2", "3", "4", "5", "6", "7":
		return m.gotoSection(section(key[0] - '1'))
	case "enter":
		m.focus = shell.FocusPanel
		m.fading = true
		nm, cmd := m.onEnterSection()
		return nm, tea.Batch(cmd, fadeTick())
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

func (m Model) handleSectionKey(key string, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.section {
	case secProfiles:
		switch key {
		case "enter":
			if it, ok := m.profilesList.Selected(); ok {
				return m, m.switchProfile(it.ID)
			}
		case "n":
			return m.enterForm(formAddProfile)
		case "e":
			if it, ok := m.profilesList.Selected(); ok {
				p, _ := m.app.Config.Get(it.ID)
				auth := p.ResolvedAuth()
				m.profileEditName = it.ID
				m.profileAuthMethod = auth.Method
				if m.profileAuthMethod == "" {
					m.profileAuthMethod = config.AuthNone
				}
				nm, cmd := m.enterForm(formEditProfile)
				mm := nm.(Model)
				mm.form.SetValues(profileFormValues(false, p.APIURL, auth))
				return mm, cmd
			}
			return m, nil
		case "d":
			if it, ok := m.profilesList.Selected(); ok {
				if err := m.app.Config.Remove(it.ID); err != nil {
					return m, m.toastErr("profile", err)
				}
				m.saveConfig()
				m.populateProfiles()
				m.status = "removed profile " + it.ID
				return m, m.toasts.Push(toast.Success, m.status)
			}
			return m, nil
		}
		cmd := m.profilesList.Update(msg)
		return m, cmd

	case secStores:
		switch key {
		case "enter":
			if it, ok := m.storesList.Selected(); ok && it.Index < len(m.stores) {
				return m, m.selectStore(m.stores[it.Index])
			}
		case "n":
			return m.enterForm(formCreateStore)
		case "d":
			if it, ok := m.storesList.Selected(); ok && it.Index < len(m.stores) {
				s := m.stores[it.Index]
				m.confirmStoreID, m.confirmStoreName = s.ID, s.Name
			}
			return m, nil
		case "r":
			m.loading = true
			return m, loadStoresCmd(m.ctx, m.client)
		}
		cmd := m.storesList.Update(msg)
		return m, cmd

	case secModel:
		switch key {
		case "e":
			if m.storeID == "" {
				m.status = "select a store first"
				return m, nil
			}
			m.editorOpen = true
			m.editorErr = ""
			if m.modelDSL != "" {
				m.editor.SetValue(m.modelDSL)
			} else {
				m.editor.SetValue(modelTemplate)
			}
			return m, m.editor.Focus()
		case "m":
			if m.storeID == "" {
				m.status = "select a store first"
				return m, nil
			}
			m.modelPicking = true
			return m, loadModelsCmd(m.ctx, m.client, m.storeID)
		case "r":
			if m.storeID != "" {
				return m, loadModelCmd(m.ctx, m.client, m.storeID)
			}
		case "up", "k", "shift+up":
			return m.scrollGraph(-graphLineStep)
		case "down", "j", "shift+down":
			return m.scrollGraph(graphLineStep)
		case "shift+left", "h":
			return m.panGraph(-graphColStep)
		case "shift+right", "l":
			return m.panGraph(graphColStep)
		case "pgup", "b":
			return m.scrollGraph(-m.graphVP.Height())
		case "pgdown", "f", " ":
			return m.scrollGraph(m.graphVP.Height())
		case "home", "g":
			return m.scrollGraphTo(0)
		case "end", "G":
			return m.scrollGraphTo(float64(m.graphMaxOffset()))
		}
		return m, nil

	case secTuples:
		switch key {
		case "a":
			if m.storeID == "" {
				m.status = "select a store first"
				return m, nil
			}
			return m.enterForm(formWriteTuple)
		case "d":
			if it, ok := m.tuplesList.Selected(); ok && it.Index < len(m.tuples) {
				k := m.tuples[it.Index].Key
				m.status = "deleting " + fga.FormatTuple(k) + "…"
				return m, writeTupleCmd(m.ctx, m.client, m.storeID, m.modelID, k, true)
			}
		case "r":
			if m.storeID != "" {
				return m, loadTuplesCmd(m.ctx, m.client, m.storeID)
			}
		}
		cmd := m.tuplesList.Update(msg)
		return m, cmd

	case secChanges:
		switch key {
		case "r":
			if m.storeID != "" {
				m.loading = true
				return m, loadChangesCmd(m.ctx, m.client, m.storeID)
			}
		}
		cmd := m.changesList.Update(msg)
		return m, cmd

	case secQuery:
		// While the resolution tree is open it captures navigation.
		if m.showRes {
			switch key {
			case "r":
				m.showRes = false
			case "p":
				m.resPathOnly = !m.resPathOnly
				m.refreshResVP()
				m.resVP.SetYOffset(0)
			case "left", "h":
				m.resVP.ScrollLeft(4)
			case "right", "l":
				m.resVP.ScrollRight(4)
			default:
				var cmd tea.Cmd
				m.resVP, cmd = m.resVP.Update(msg)
				return m, cmd
			}
			return m, nil
		}
		switch key {
		case "i", "enter":
			return m, m.enterQueryEdit()
		case "tab":
			// Switch to the next mode and land in its first field, ready to type.
			m.cycleQueryMode(1)
			return m, m.enterQueryEdit()
		case "shift+tab":
			m.cycleQueryMode(-1)
			return m, m.enterQueryEdit()
		case "m":
			// Browse modes without entering the form.
			m.cycleQueryMode(1)
		case "r":
			// Show the Check resolution tree for the last check.
			if m.hasResult && m.result.badge {
				m.loading = true
				return m, expandCmd(m.ctx, m.client, m.storeID, m.modelID,
					m.result.vals[0], m.result.vals[1], m.result.vals[2])
			}
			m.status = "run a check first (r shows its resolution)"
		case "1", "2", "3", "4", "5", "6":
			// A digit addressing an existing history slot reruns it; "6"
			// never matches since history is capped at 5.
			if n := int(key[0] - '1'); n < len(m.history) {
				return m.rerunHistory(n)
			}
		}
		return m, nil

	case secAssertions:
		switch key {
		case "a":
			if m.storeID == "" {
				m.status = "select a store first"
				return m, nil
			}
			m.assertEditIdx = -1
			return m.enterForm(formWriteAssertion)
		case "e":
			if it, ok := m.assertionsList.Selected(); ok && it.Index < len(m.assertions) {
				m.assertEditIdx = it.Index
				nm, cmd := m.enterForm(formWriteAssertion)
				mm := nm.(Model)
				a := m.assertions[it.Index]
				mm.form.SetValues([]string{a.TupleKey.User, a.TupleKey.Relation, a.TupleKey.Object, strconv.FormatBool(a.Expectation), formatContextualTuples(a.ContextualTuples), formatContextJSON(a.Context)})
				return mm, cmd
			}
			return m, nil
		case "d":
			if it, ok := m.assertionsList.Selected(); ok && it.Index < len(m.assertions) {
				list := append([]openfga.Assertion{}, m.assertions...)
				list = append(list[:it.Index], list[it.Index+1:]...)
				m.status = "deleting assertion…"
				return m, writeAssertionsCmd(m.ctx, m.client, m.storeID, m.assertModelID, list)
			}
			return m, nil
		case "enter":
			if it, ok := m.assertionsList.Selected(); ok && it.Index < len(m.assertions) {
				a := m.assertions[it.Index]
				u, rel, obj := a.TupleKey.User, a.TupleKey.Relation, a.TupleKey.Object
				// Run the assertion (updates its badge) and open its resolution
				// tree in the Query panel.
				m.section = secQuery
				m.result = queryResultMsg{badge: true, vals: [3]string{u, rel, obj}, mode: "check"}
				m.hasResult = true
				m.loading = true
				m.status = "resolving assertion…"
				return m, tea.Batch(
					runOneAssertionCmd(m.ctx, m.client, m.storeID, m.assertModelID, it.Index, a),
					expandCmd(m.ctx, m.client, m.storeID, m.assertModelID, u, rel, obj),
				)
			}
			return m, nil
		case "t":
			if len(m.assertions) == 0 {
				m.status = "no assertions to run"
				return m, nil
			}
			m.loading = true
			m.status = "running assertions…"
			return m, runAssertionsCmd(m.ctx, m.client, m.storeID, m.assertModelID, m.assertions)
		case "r":
			if m.storeID != "" {
				m.loading = true
				return m, loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID)
			}
		}
		cmd := m.assertionsList.Update(msg)
		return m, cmd

	}
	return m, nil
}

// --- forms ---

func (m Model) enterForm(kind formKind) (tea.Model, tea.Cmd) {
	dw, _ := m.sh.DialogSize()
	m.formKind = kind
	switch kind {
	case formCreateStore:
		m.form = buildCreateStoreForm(dw)
	case formWriteTuple:
		m.form = buildWriteTupleForm(dw)
	case formWriteAssertion:
		m.form = buildWriteAssertionForm(dw)
	case formAddProfile:
		m.profileAuthMethod = config.AuthNone
		m.form = buildProfileForm(true, m.profileAuthMethod, dw)
	case formEditProfile:
		// m.profileAuthMethod is set by the caller from the profile being edited.
		m.form = buildProfileForm(false, m.profileAuthMethod, dw)
	}
	// Emphasize the active field with the theme highlight; others stay plain.
	m.form.SetHighlight(style.FieldHighlight())
	return m, m.form.Init()
}

// profileMethodIndex is the form-field index of the auth-method selector:
// after name+api_url when adding, after api_url when editing.
func (m Model) profileMethodIndex() int {
	if m.formKind == formAddProfile {
		return 2
	}
	return 1
}

// profileFormMethod reads the auth method currently selected in the profile form.
func (m Model) profileFormMethod() string {
	vals := m.form.Values()
	if i := m.profileMethodIndex(); i < len(vals) {
		return vals[i]
	}
	return ""
}

// rebuildProfileForm rebuilds the profile form for the newly-selected auth
// method, preserving name/api_url/method and keeping focus on the selector.
func (m *Model) rebuildProfileForm() tea.Cmd {
	add := m.formKind == formAddProfile
	vals := m.form.Values()
	method := m.profileFormMethod()
	dw, _ := m.sh.DialogSize()
	m.form = buildProfileForm(add, method, dw)
	m.form.SetHighlight(style.FieldHighlight())
	var pre []string
	idx := 0
	if add {
		pre = append(pre, vals[0])
		idx = 1
	}
	pre = append(pre, vals[idx], method)
	m.form.SetValues(pre)
	m.profileAuthMethod = method
	return m.form.FocusIndex(m.profileMethodIndex())
}

func (m Model) handleTakeoverForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.formKind = formNone
		return m, nil
	}
	return m.advanceTakeoverForm(msg)
}

// advanceTakeoverForm feeds any message to the takeover form and dispatches
// the resulting action once the form completes.
func (m Model) advanceTakeoverForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.form.Update(msg)
	// The profile form shows fields for the selected auth method; when the
	// method selector changes, rebuild the form for the new method.
	if (m.formKind == formAddProfile || m.formKind == formEditProfile) && !m.form.Completed() {
		if method := m.profileFormMethod(); method != m.profileAuthMethod {
			return m, m.rebuildProfileForm()
		}
	}
	if m.form.Completed() {
		vals := m.form.Values()
		kind := m.formKind
		m.formKind = formNone
		switch kind {
		case formCreateStore:
			name := strings.TrimSpace(vals[0])
			if name == "" {
				m.status = "store name required"
				return m, nil
			}
			m.status = "creating store " + name + "…"
			return m, createStoreCmd(m.ctx, m.client, name)
		case formWriteTuple:
			key, err := fga.ParseTuple(vals[0], vals[1], vals[2])
			if err != nil {
				m.status = err.Error()
				return m, nil
			}
			cond, err := parseCondition(vals[3], vals[4])
			if err != nil {
				m.status = err.Error()
				return m, nil
			}
			key.Condition = cond
			m.status = "writing " + fga.FormatTuple(key) + "…"
			return m, writeTupleCmd(m.ctx, m.client, m.storeID, m.modelID, key, false)
		case formWriteAssertion:
			key, err := fga.ParseTuple(vals[0], vals[1], vals[2])
			if err != nil {
				// Surface any failure adding an assertion in the modal, not the footer.
				m.assertErr = err.Error()
				return m, nil
			}
			ctxTuples, err := parseContextualTuples(vals[4])
			if err != nil {
				m.assertErr = err.Error()
				return m, nil
			}
			ctxMap, err := parseContextJSON(vals[5])
			if err != nil {
				m.assertErr = err.Error()
				return m, nil
			}
			a := openfga.Assertion{
				TupleKey:         openfga.CheckRequestTupleKey{User: key.User, Relation: key.Relation, Object: key.Object},
				Expectation:      vals[3] == "true",
				ContextualTuples: ctxTuples,
				Context:          ctxMap,
			}
			list := append([]openfga.Assertion{}, m.assertions...)
			if m.assertEditIdx >= 0 && m.assertEditIdx < len(list) {
				list[m.assertEditIdx] = a
			} else {
				list = append(list, a)
			}
			m.status = "writing assertions…"
			return m, writeAssertionsCmd(m.ctx, m.client, m.storeID, m.assertModelID, list)
		case formAddProfile:
			name, p := profileFromForm(true, vals)
			if name == "" {
				m.status = "profile name required"
				return m, nil
			}
			if _, exists := m.app.Config.Get(name); exists {
				m.status = "profile " + name + " already exists"
				return m, nil
			}
			m.app.Config.Set(name, p)
			m.saveConfig()
			m.populateProfiles()
			m.status = "created profile " + name
			return m, m.toasts.Push(toast.Success, m.status)
		case formEditProfile:
			name := m.profileEditName
			existing, ok := m.app.Config.Get(name)
			if !ok {
				m.status = "profile " + name + " no longer exists"
				return m, nil
			}
			_, p := profileFromForm(false, vals)
			// Keep the auto-managed store/model; replace connection + auth, and
			// migrate any legacy top-level token into the auth block.
			existing.APIURL, existing.Auth, existing.APIToken = p.APIURL, p.Auth, ""
			m.app.Config.Set(name, existing)
			m.saveConfig()
			m.populateProfiles()
			// Editing the active profile changes the live connection — reconnect.
			if name == m.app.Config.Active {
				return m, m.reloadActive("updated profile " + name)
			}
			m.status = "updated profile " + name
			return m, m.toasts.Push(toast.Success, m.status)
		}
	}
	return m, cmd
}

func (m Model) handleQueryForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// First esc drops to the (non-editing) panel layer, where r / history
		// digits / resolution live; a second esc returns to the tab selection.
		m.editing = false
		return m, nil
	case "tab":
		// tab keeps shifting modes even mid-edit, landing in the new mode's
		// first field. Field navigation stays on the arrows and enter.
		m.cycleQueryMode(1)
		return m, m.qform.Init()
	case "shift+tab":
		m.cycleQueryMode(-1)
		return m, m.qform.Init()
	}
	return m.advanceQueryForm(msg)
}

// advanceQueryForm feeds any message to the query form and runs the selected
// query once the form completes.
func (m Model) advanceQueryForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.qform.Update(msg)
	// The context toggle (field 3) reveals/hides the extra fields; rebuild the
	// form when it flips, preserving the three main fields and staying on it.
	if !m.qform.Completed() {
		if show := m.qform.Values()[3] == "true"; show != m.qShowContext {
			m.qShowContext = show
			vals := m.qform.Values()
			m.rebuildQueryForm()
			m.qform.SetValues(vals[:3])
			return m, m.qform.FocusIndex(3)
		}
	}
	if m.qform.Completed() {
		vals := m.qform.Values()
		a := strings.TrimSpace(vals[0])
		b := strings.TrimSpace(vals[1])
		c := strings.TrimSpace(vals[2])
		var qctx queryCtx
		var cerr error
		if m.qShowContext && len(vals) >= 6 {
			qctx, cerr = parseQueryCtx(vals[4], vals[5])
		}
		// Stay in editing with a fresh, focused form so the next query can be
		// typed immediately (esc drops to the panel for r / history / resolution).
		m.rebuildQueryForm()
		if cerr != nil {
			m.setQueryError(cerr.Error())
			return m, nil
		}
		if a == "" || b == "" || c == "" {
			m.setQueryError("user, relation and object are required")
			return m, nil
		}
		m.loading = true
		m.status = "running " + queryModes[m.qmode] + "…"
		switch queryModes[m.qmode] {
		case "check":
			return m, checkCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx)
		case "list-objects":
			return m, listObjectsCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx)
		case "list-users":
			return m, listUsersCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx)
		}
	}
	return m, cmd
}

// rerunHistory replays history entry idx: switches to its mode, refills the
// query form with its values, and dispatches the same run command enter
// uses on form completion.
func (m Model) rerunHistory(idx int) (tea.Model, tea.Cmd) {
	h := m.history[idx]
	m.qmode = queryModeIndex(h.mode)
	m.rebuildQueryForm()
	m.qform.SetValues(h.vals[:])
	m.editing = false
	a, b, c := h.vals[0], h.vals[1], h.vals[2]
	m.loading = true
	m.status = "running " + queryModes[m.qmode] + "…"
	switch queryModes[m.qmode] {
	case "check":
		return m, checkCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, queryCtx{})
	case "list-objects":
		return m, listObjectsCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, queryCtx{})
	case "list-users":
		return m, listUsersCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, queryCtx{})
	}
	return m, nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
