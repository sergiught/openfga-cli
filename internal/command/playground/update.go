package playground

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/forms"
	"github.com/sergiught/openfga-cli/internal/ui/list"
)

type pendingAction struct{ runAssertions bool }

var pending pendingAction

// Update is the central dispatcher.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		m.ready = true
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case storesLoadedMsg:
		m.splash = false
		m.loading = false
		if msg.err != nil {
			m.status = errStr(msg.err)
			return m, nil
		}
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
			m.status = "model: " + errStr(msg.err)
			m.graph = fga.Graph{}
			m.graphVP.SetContent(style.Faint.Render("no model: " + errStr(msg.err)))
			return m, nil
		}
		m.modelID = msg.modelID
		m.graph = msg.graph
		m.graphVP.SetContent(m.graph.RenderDiagram())
		m.resetGraphScroll()
		m.status = "model " + short(msg.modelID) + " · " + m.graph.Summary()
		return m, nil

	case modelsListedMsg:
		if msg.err != nil {
			m.status = "models: " + errStr(msg.err)
			return m, nil
		}
		m.models = msg.models
		m.populateModels()
		return m, nil

	case tuplesLoadedMsg:
		if msg.err != nil {
			m.status = "tuples: " + errStr(msg.err)
			return m, nil
		}
		m.tuples = msg.tuples
		m.populateTuples()
		return m, nil

	case changesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.status = "changes: " + errStr(msg.err)
			return m, nil
		}
		m.changes = msg.changes
		m.populateChanges()
		m.status = plural(len(msg.changes), "change")
		return m, nil

	case assertionsLoadedMsg:
		m.loading = false
		m.assertModelID = msg.modelID
		if msg.err != nil {
			m.status = "assertions: " + errStr(msg.err)
			m.assertions = nil
			return m, nil
		}
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
			m.status = "assertion test: " + errStr(msg.err)
			return m, nil
		}
		m.assertResults = msg.results
		m.assertSummary = strconv.Itoa(msg.passed) + "/" + strconv.Itoa(msg.total) + " passed"
		m.populateAssertions()
		m.status = m.assertSummary
		return m, nil

	case storeCreatedMsg:
		if msg.err != nil {
			m.status = "create store: " + errStr(msg.err)
			return m, nil
		}
		m.status = "created store " + msg.store.Name
		return m, tea.Batch(m.selectStore(msg.store), loadStoresCmd(m.ctx, m.client))

	case tupleWrittenMsg:
		if msg.err != nil {
			m.status = "tuple: " + errStr(msg.err)
			return m, nil
		}
		verb := "wrote"
		if msg.deleted {
			verb = "deleted"
		}
		m.status = verb + " " + msg.label
		return m, loadTuplesCmd(m.ctx, m.client, m.storeID)

	case queryResultMsg:
		m.loading = false
		m.hasResult = true
		m.result = msg
		if msg.err != nil {
			m.status = "query: " + errStr(msg.err)
		} else {
			m.status = "query complete"
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case graphTickMsg:
		return m.advanceGraphScroll()

	default:
		// huh drives field navigation, cursor blinking, and submission via its
		// own (non-key) messages such as nextFieldMsg. Bubble Tea delivers those
		// here, so an active form must see every message, not just key presses —
		// otherwise tab/enter never moves focus and the form never completes.
		if m.formKind != formNone {
			return m.advanceTakeoverForm(msg)
		}
		if m.section == secQuery && m.editing {
			return m.advanceQueryForm(msg)
		}
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.splash {
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		m.splash = false
		return m, nil
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
		return m.onEnterSection()
	case "shift+tab":
		m.section = (m.section + section(len(sectionNames)) - 1) % section(len(sectionNames))
		return m.onEnterSection()
	case "1", "2", "3", "4", "5", "6", "7":
		m.section = section(key[0] - '1')
		return m.onEnterSection()
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
	case secSettings:
		return m.themesList
	}
	return nil
}

func (m Model) handleSectionKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		case "up", "k":
			return m.scrollGraph(-graphLineStep)
		case "down", "j":
			return m.scrollGraph(graphLineStep)
		case "left", "h":
			return m.panGraph(-graphColStep)
		case "right", "l":
			return m.panGraph(graphColStep)
		case "pgup", "b":
			return m.scrollGraph(-m.graphVP.Height)
		case "pgdown", "f", " ":
			return m.scrollGraph(m.graphVP.Height)
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

	case secSettings:
		cmd := m.themesList.Update(msg)
		if it, ok := m.themesList.Selected(); ok {
			style.SetTheme(it.ID) // live preview
			if key == "enter" {
				m.app.Config.Theme = it.ID
				m.themeOrig = it.ID
				m.populateThemes()
				if err := m.app.SaveConfig(); err != nil {
					m.status = "save theme: " + errStr(err)
				} else {
					m.status = "theme saved: " + it.ID
				}
			}
		}
		return m, cmd
	}
	return m, nil
}

// --- forms ---

func (m Model) enterForm(kind formKind) (tea.Model, tea.Cmd) {
	w, h := m.contentSize()
	fh := h - 2
	if fh < 4 {
		fh = 4
	}
	m.formKind = kind
	switch kind {
	case formCreateStore:
		m.form, m.ftheme = forms.CreateStore(w, fh)
	case formWriteTuple:
		m.form, m.ftheme = forms.WriteTuple(w, fh)
	}
	return m, m.form.Init()
}

func (m Model) handleTakeoverForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.formKind = formNone
		return m, nil
	}
	return m.advanceTakeoverForm(msg)
}

// advanceTakeoverForm feeds any message (key or huh-internal) to the takeover
// form and dispatches the resulting action once the form completes.
func (m Model) advanceTakeoverForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	fm, cmd := m.form.Update(msg)
	m.form = fm.(*huh.Form)
	if m.form.State == huh.StateCompleted {
		kind := m.formKind
		m.formKind = formNone
		switch kind {
		case formCreateStore:
			name := strings.TrimSpace(m.form.GetString("name"))
			if name == "" {
				m.status = "store name required"
				return m, nil
			}
			m.status = "creating store " + name + "…"
			return m, createStoreCmd(m.ctx, m.client, name)
		case formWriteTuple:
			key, err := fga.ParseTuple(m.form.GetString("user"), m.form.GetString("relation"), m.form.GetString("object"))
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

func (m Model) handleQueryForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.editing = false
		return m, nil
	}
	return m.advanceQueryForm(msg)
}

// advanceQueryForm feeds any message (key or huh-internal) to the query form
// and runs the selected query once the form completes.
func (m Model) advanceQueryForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	fm, cmd := m.qform.Update(msg)
	m.qform = fm.(*huh.Form)
	if m.qform.State == huh.StateCompleted {
		m.editing = false
		a := strings.TrimSpace(m.qform.GetString("a"))
		b := strings.TrimSpace(m.qform.GetString("b"))
		c := strings.TrimSpace(m.qform.GetString("c"))
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

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
