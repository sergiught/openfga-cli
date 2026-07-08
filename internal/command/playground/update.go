package playground

import (
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/list"
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

// pulseTickMsg drives the sidebar's breathing connection dot. It re-arms
// itself only while a store is selected; the initial tick is kicked off once
// (from Init or selectStore, whichever first sees a non-empty storeID).
type pulseTickMsg struct{}

func pulseTick() tea.Cmd {
	return tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
		return pulseTickMsg{}
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
			m.connLost = isConnErr(msg.err)
			m.status = errStr(msg.err) + staleSuffix(m.connLost)
			return m, m.toasts.Push(toast.Error, m.status)
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
		return m, nil

	case modelLoadedMsg:
		if msg.err != nil {
			m.connLost = isConnErr(msg.err)
			m.status = "model: " + errStr(msg.err) + staleSuffix(m.connLost)
			if !m.connLost {
				m.graph = fga.Graph{}
				m.graphVP.SetContent(style.Faint.Render("no model: " + errStr(msg.err)))
			}
			return m, m.toasts.Push(toast.Error, m.status)
		}
		m.connLost = false
		m.modelID = msg.modelID
		m.graph = msg.graph
		m.modelDSL = msg.dsl
		m.graphVP.SetContent(m.graph.RenderDiagram())
		m.resetGraphScroll()
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
			m.connLost = isConnErr(msg.err)
			m.status = "models: " + errStr(msg.err) + staleSuffix(m.connLost)
			return m, m.toasts.Push(toast.Error, m.status)
		}
		m.connLost = false
		m.models = msg.models
		m.populateModels()
		return m, nil

	case tuplesLoadedMsg:
		if msg.err != nil {
			m.connLost = isConnErr(msg.err)
			m.status = "tuples: " + errStr(msg.err) + staleSuffix(m.connLost)
			return m, m.toasts.Push(toast.Error, m.status)
		}
		m.connLost = false
		m.tuples = msg.tuples
		m.populateTuples()
		return m, nil

	case changesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.connLost = isConnErr(msg.err)
			m.status = "changes: " + errStr(msg.err) + staleSuffix(m.connLost)
			return m, m.toasts.Push(toast.Error, m.status)
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
			m.connLost = isConnErr(msg.err)
			m.status = "assertions: " + errStr(msg.err) + staleSuffix(m.connLost)
			if !m.connLost {
				m.assertions = nil
			}
			return m, m.toasts.Push(toast.Error, m.status)
		}
		m.connLost = false
		m.assertions = msg.assertions
		m.assertResults = nil
		m.assertSummary = ""
		m.populateAssertions()
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
			m.connLost = isConnErr(msg.err)
			m.status = "assertion test: " + errStr(msg.err)
			return m, m.toasts.Push(toast.Error, m.status)
		}
		m.connLost = false
		m.assertResults = msg.results
		m.assertSummary = strconv.Itoa(msg.passed) + "/" + strconv.Itoa(msg.total) + " passed"
		m.populateAssertions()
		m.status = m.assertSummary
		return m, m.toasts.Push(toast.Success, m.status)

	case storeCreatedMsg:
		if msg.err != nil {
			m.connLost = isConnErr(msg.err)
			m.status = "create store: " + errStr(msg.err)
			return m, m.toasts.Push(toast.Error, m.status)
		}
		m.connLost = false
		m.status = "created store " + msg.store.Name
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status), m.selectStore(msg.store), loadStoresCmd(m.ctx, m.client))

	case tupleWrittenMsg:
		if msg.err != nil {
			m.connLost = isConnErr(msg.err)
			m.status = "tuple: " + errStr(msg.err)
			return m, m.toasts.Push(toast.Error, m.status)
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
		if msg.err != nil {
			m.connLost = isConnErr(msg.err)
			m.status = "query: " + errStr(msg.err)
			return m, m.toasts.Push(toast.Error, m.status)
		}
		m.connLost = false
		m.status = "query complete"
		cmds := []tea.Cmd{m.toasts.Push(toast.Success, m.status)}
		if msg.badge {
			m.pushHistory(histEntry{mode: msg.mode, vals: msg.vals, ok: msg.ok, ms: msg.ms})
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

	case pulseTickMsg:
		m.pulse += 0.6
		if m.storeID != "" {
			return m, pulseTick()
		}
		return m, nil

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

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.paletteOpen {
		switch msg.String() {
		case "esc", "ctrl+k":
			m.paletteOpen = false
			return m, nil
		case "enter":
			if it, ok := m.paletteList.Selected(); ok {
				m.paletteOpen = false
				m.section = section(it.Index)
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

	// While a list is filtering, route everything to it.
	if lst := m.activeList(); lst != nil && lst.SettingFilter() {
		cmd := lst.Update(msg)
		return m, cmd
	}

	key := msg.String()
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		return m, tea.Quit
	case "tab":
		m.section = (m.section + 1) % section(len(sectionNames))
		m.fading = true
		nm, cmd := m.onEnterSection()
		return nm, tea.Batch(cmd, fadeTick())
	case "shift+tab":
		m.section = (m.section + section(len(sectionNames)) - 1) % section(len(sectionNames))
		m.fading = true
		nm, cmd := m.onEnterSection()
		return nm, tea.Batch(cmd, fadeTick())
	case "right":
		m.section = (m.section + 1) % section(len(sectionNames))
		m.fading = true
		nm, cmd := m.onEnterSection()
		return nm, tea.Batch(cmd, fadeTick())
	case "left":
		m.section = (m.section + section(len(sectionNames)) - 1) % section(len(sectionNames))
		m.fading = true
		nm, cmd := m.onEnterSection()
		return nm, tea.Batch(cmd, fadeTick())
	case "1", "2", "3", "4", "5", "6":
		// In the Query section, a digit that addresses an existing history
		// slot reruns it instead of switching sections. (m.editing is
		// always false here: the secQuery-and-editing case above returns
		// before this switch, so digits typed into the form never reach
		// this branch.) Digits with no matching entry — including "6",
		// since history never grows past 5 — fall through to the normal
		// section switch below.
		if m.section == secQuery {
			if n := int(key[0] - '1'); n < len(m.history) {
				return m.rerunHistory(n)
			}
		}
		m.section = section(key[0] - '1')
		m.fading = true
		nm, cmd := m.onEnterSection()
		return nm, tea.Batch(cmd, fadeTick())
	case "ctrl+k":
		m.paletteOpen = true
		return m, nil
	}
	return m.handleSectionKey(key, msg)
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
		if m.storeID != "" && m.assertions == nil {
			m.loading = true
			return m, loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID)
		}
	}
	return m, nil
}

// activeList returns the list backing the current section, or nil.
func (m *Model) activeList() *list.List {
	switch m.section {
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
	case secStores:
		switch key {
		case "enter":
			if it, ok := m.storesList.Selected(); ok && it.Index < len(m.stores) {
				return m, m.selectStore(m.stores[it.Index])
			}
		case "n":
			return m.enterForm(formCreateStore)
		case "r":
			m.loading = true
			return m, loadStoresCmd(m.ctx, m.client)
		}
		cmd := m.storesList.Update(msg)
		return m, cmd

	case secModel:
		if m.modelPicking {
			switch key {
			case "enter":
				if it, ok := m.modelsList.Selected(); ok && it.Index < len(m.models) {
					m.modelPicking = false
					id := m.models[it.Index].ID
					m.status = "loading model " + short(id) + "…"
					return m, loadModelByIDCmd(m.ctx, m.client, m.storeID, id)
				}
			case "esc":
				m.modelPicking = false
				return m, nil
			}
			cmd := m.modelsList.Update(msg)
			return m, cmd
		}
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
				return m, writeTupleCmd(m.ctx, m.client, m.storeID, k, true)
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
		switch key {
		case "i", "enter":
			if m.storeID == "" {
				m.status = "select a store first"
				return m, nil
			}
			m.editing = true
			return m, m.qform.Init()
		case "m":
			m.qmode = (m.qmode + 1) % len(queryModes)
			m.rebuildQueryForm()
			m.hasResult = false
		}
		return m, nil

	case secAssertions:
		switch key {
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
	}
	return m, m.form.Init()
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
			m.status = "writing " + fga.FormatTuple(key) + "…"
			return m, writeTupleCmd(m.ctx, m.client, m.storeID, key, false)
		}
	}
	return m, cmd
}

func (m Model) handleQueryForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.editing = false
		return m, nil
	}
	return m.advanceQueryForm(msg)
}

// advanceQueryForm feeds any message to the query form and runs the selected
// query once the form completes.
func (m Model) advanceQueryForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.qform.Update(msg)
	if m.qform.Completed() {
		m.editing = false
		vals := m.qform.Values()
		a := strings.TrimSpace(vals[0])
		b := strings.TrimSpace(vals[1])
		c := strings.TrimSpace(vals[2])
		m.rebuildQueryForm()
		if a == "" || b == "" || c == "" {
			m.status = "all three fields are required"
			return m, nil
		}
		m.loading = true
		m.status = "running " + queryModes[m.qmode] + "…"
		switch queryModes[m.qmode] {
		case "check":
			return m, checkCmd(m.ctx, m.client, m.storeID, a, b, c)
		case "list-objects":
			return m, listObjectsCmd(m.ctx, m.client, m.storeID, a, b, c)
		case "list-users":
			return m, listUsersCmd(m.ctx, m.client, m.storeID, a, b, c)
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
		return m, checkCmd(m.ctx, m.client, m.storeID, a, b, c)
	case "list-objects":
		return m, listObjectsCmd(m.ctx, m.client, m.storeID, a, b, c)
	case "list-users":
		return m, listUsersCmd(m.ctx, m.client, m.storeID, a, b, c)
	}
	return m, nil
}

// staleSuffix returns a styled " stale" marker to append to a status line
// when a data load failed because the connection dropped and cached data is
// being kept on screen, or "" when nothing needs marking.
func staleSuffix(connLost bool) string {
	if !connLost {
		return ""
	}
	return "  " + style.Warn.Render("stale")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
