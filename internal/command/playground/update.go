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
	// Accept both the canonical OPENFGA_-prefixed name and the legacy OFGA_ one
	// (kept for compatibility) so the opt-out matches every other env var.
	return os.Getenv("OPENFGA_REDUCED_MOTION") != "" ||
		os.Getenv("OFGA_REDUCED_MOTION") != "" ||
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

	case bootNoticeMsg:
		return m, m.toasts.Push(toast.Info, msg.text)

	case storesLoadedMsg:
		// A stores list from a connection that's since been replaced (a profile
		// switch or an edit to the active profile's connection, both of which
		// go through activateResolved) must be dropped — unlike every other load,
		// this one has no store id of its own to check, so it needs its own
		// generation or it could repopulate the list from the wrong server, or
		// even auto-select a store id that doesn't exist there.
		stale := staleGen(msg.gen, m.storesGen)
		m.endLoad()
		if stale {
			return m, nil
		}
		if msg.err != nil {
			if isPermissionErr(msg.err) {
				// The server rejected listing stores for lack of permission
				// (401/403). Show a dedicated Stores-panel notice instead of an
				// empty "no stores" list plus a red error toast, which together
				// misread as "this server has no stores" when the real cause is a
				// missing permission to manage them.
				m.storesForbidden = true
				m.stores = nil
				m.populateStores()
				m.status = "no permission to manage stores on this server"
				return m, m.toasts.Push(toast.Info, m.status)
			}
			m.storesForbidden = false
			return m, m.toastErr("", msg.err)
		}
		m.storesForbidden = false
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
		// A stale response (superseded store switch, or a superseded model
		// request against the same store — e.g. two quick picks in the model
		// switcher) must still free its load slot; only the state it carries is
		// dropped.
		stale := staleStore(msg.storeID, m.storeID) || staleGen(msg.gen, m.modelGen)
		m.endLoad()
		if stale {
			return m, nil
		}
		if msg.err != nil {
			// A pinned model id the store doesn't have (e.g. the store was switched
			// via `ofga profiles set store` without clearing model_id, or a config
			// was hand-edited) must not strand the Model view claiming the store has
			// no model. Drop the stale pin and load the store's latest model once;
			// if that also fails the pin is already cleared, so this can't loop.
			if m.modelID != "" && strings.Contains(msg.err.Error(), "authorization_model_not_found") {
				m.modelID = ""
				m.beginLoad()
				m.modelGen++
				return m, loadModelCmd(m.ctx, m.client, m.storeID, m.modelGen)
			}
			cmd := m.toastErr("model", msg.err)
			if !m.connLost {
				m.graph = fga.Graph{}
				m.graphVP.SetContent(style.Faint.Render("no model: " + errStr(msg.err)))
			}
			return m, cmd
		}
		m.connLost = false
		if m.modelID != "" && msg.modelID != m.modelID {
			m.clearResourcePending()
		}
		m.modelID = msg.modelID
		// ReadLatest flags it directly; a picked model is latest only if it is
		// the newest in the (already loaded) models list.
		m.modelIsLatest = msg.latest || (len(m.models) > 0 && msg.modelID == m.models[0].ID)
		m.graph = msg.graph
		m.modelDSL = msg.dsl
		m.graphVP.SetContent(m.graph.RenderDiagram())
		m.resetGraphScroll()
		// A failed persist must never be reported as a clean success — see
		// persistStore's doc comment for why this can't just be overwritten a
		// moment later.
		if err := m.persistModel(); err != nil {
			return m, m.configSaveErrCmd(err)
		}
		m.status = "model " + short(msg.modelID) + " · " + m.graph.Summary()
		return m, nil

	case modelAppliedMsg:
		m.endLoad()
		if m.staleMutation(msg.origin, m.modelApplyGen) {
			return m, nil
		}
		m.modelApplying = false
		m.mutationStatus = ""
		if msg.err != nil {
			m.connLost = isConnErr(msg.err)
			m.editorErr = errStr(msg.err)
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
		// A fresh reload of the just-applied model follows; begin its slot and
		// bump modelGen so a slow in-flight model load from before the apply
		// can't clobber it.
		m.beginLoad()
		m.modelGen++
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status), loadModelCmd(m.ctx, m.client, m.storeID, m.modelGen))

	case modelsListedMsg:
		// A rapid close/reopen of the model switcher can have two list loads in
		// flight against the same store; only storeID was checked here before,
		// so the older of the two could win a race and show a stale list.
		stale := staleStore(msg.storeID, m.storeID) || staleGen(msg.gen, m.modelsGen)
		m.endLoad()
		if stale {
			return m, nil // a load from a store we've since switched away from, or superseded by a newer list open
		}
		if msg.err != nil {
			return m, m.toastErr("models", msg.err)
		}
		m.connLost = false
		m.models = msg.models
		m.populateModels()
		return m, nil

	case tuplesLoadedMsg:
		stale := staleStore(msg.storeID, m.storeID) || staleGen(msg.gen, m.tuplesGen)
		m.endLoad()
		if stale {
			return m, nil // a load from a store we've since switched away from, or superseded by a newer reload
		}
		if msg.err != nil {
			return m, m.toastErr("tuples", msg.err)
		}
		m.connLost = false
		m.tuples = msg.tuples
		m.tuplesCapped = msg.capped
		m.populateTuples()
		return m, nil

	case changesLoadedMsg:
		stale := staleStore(msg.storeID, m.storeID) || staleGen(msg.gen, m.changesGen)
		m.endLoad()
		if stale {
			return m, nil // a load from a store we've since switched away from, or superseded by a newer reload
		}
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
		// The load may have started before any model was loaded (modelID == ""
		// at dispatch), resolved "latest" internally, and landed after the user
		// switched to a specific different model — that resolved identity is no
		// longer current, so it's compared against the active model when one is
		// known (an unknown m.modelID means nothing to compare against yet, so
		// the resolved latest is accepted).
		stale := staleStore(msg.storeID, m.storeID) || staleGen(msg.gen, m.assertLoadGen) || staleModelKnown(msg.modelID, m.modelID)
		m.endLoad()
		if stale {
			return m, nil // a load from a store/model we've since switched away from, or superseded by a newer reload
		}
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
			m.beginLoad()
			m.assertGen++
			m.status = "running assertions…"
			return m, runAssertionsCmd(m.ctx, m.client, m.storeID, m.assertModelID, m.assertions, m.assertGen)
		}
		return m, nil

	case assertTestMsg:
		// assertModelID is the primary comparator (the model the assertions
		// list itself was loaded against), but it only updates when that list
		// reloads — if the user switches the *active* model (m.modelID) after
		// starting this run but before Assertions reloads, assertModelID would
		// still match and wrongly accept a now-stale result. staleModelKnown
		// catches that by also checking against m.modelID when it's known.
		stale := staleStore(msg.storeID, m.storeID) || staleModel(msg.modelID, m.assertModelID) ||
			staleModelKnown(msg.modelID, m.modelID) || staleGen(msg.gen, m.assertGen)
		m.endLoad()
		if stale {
			return m, nil // superseded by a newer assertion run against the same store
		}
		if msg.err != nil {
			return m, m.toastErr("assertion test", msg.err)
		}
		m.connLost = false
		m.assertResults = msg.results
		m.assertSummary = strconv.Itoa(msg.passed) + "/" + strconv.Itoa(msg.total) + " passed"
		m.populateAssertions()
		m.resize()
		m.status = m.assertSummary
		if msg.passed < msg.total {
			// Some assertions failed — surface it as an error toast, not a green
			// success, mirroring the CLI's non-zero exit on assertion failure.
			return m, m.toasts.Push(toast.Error, m.status)
		}
		return m, m.toasts.Push(toast.Success, m.status)

	case assertOneMsg:
		// Same rationale as assertTestMsg: check both assertModelID and the
		// active model, since the latter can change before the former catches
		// up on the next Assertions reload.
		stale := staleStore(msg.storeID, m.storeID) || staleModel(msg.modelID, m.assertModelID) ||
			staleModelKnown(msg.modelID, m.modelID) || staleGen(msg.gen, m.assertGen)
		m.endLoad()
		if stale {
			return m, nil // superseded by a newer assertion run against the same store
		}
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
		// This is the real verdict for the assertion opened via Enter (see the
		// secAssertions enter handler): fill the Query panel's result now, so the
		// verdict reflects the actual Check instead of a fabricated denial.
		m.result.ok = msg.result.got
		m.result.badge = true
		m.hasResult = true
		m.populateAssertions()
		m.resize()
		m.status = assertResultWord(msg.result)
		return m, nil

	case assertionsWrittenMsg:
		m.endLoad()
		if m.staleMutation(msg.origin, m.assertionWriteGen) {
			return m, nil
		}
		m.assertionsWriting = false
		m.mutationStatus = ""
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
		m.beginLoad()
		m.assertLoadGen++
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status),
			loadAssertionsCmd(m.ctx, m.client, m.storeID, msg.modelID, m.assertLoadGen))

	case resolutionMsg:
		// resolutionMsg is dispatched with either m.modelID (Query section's
		// "r") or m.assertModelID (Assertions section's "enter"), so its own
		// modelID field is the primary comparator via staleGen's per-store/gen
		// scoping; staleModelKnown additionally rejects it if the *active*
		// model has since changed to something else, catching a race where the
		// user switches models before this resolution lands.
		stale := staleStore(msg.storeID, m.storeID) || staleGen(msg.gen, m.resGen) || staleModelKnown(msg.modelID, m.modelID)
		m.endLoad()
		if stale {
			return m, nil // superseded by a newer resolution request against the same store
		}
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
		m.endLoad()
		if m.staleMutation(msg.origin, m.storeCreateGen) {
			return m, nil
		}
		m.storeCreating = false
		m.mutationStatus = ""
		if msg.err != nil {
			return m, m.toastErr("create store", msg.err)
		}
		m.connLost = false
		m.status = "created store " + msg.store.Name
		// selectStore begins its own 4 loads; the stores-list refresh below is a
		// fifth, independent load and needs its own begin. Bump storesGen too:
		// it's an independent dispatch from any other in-flight stores refresh
		// (e.g. a manual reload started just before this create completed), so
		// the older of the two must not be allowed to win.
		m.beginLoad()
		m.storesGen++
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status), m.selectStore(msg.store), loadStoresCmd(m.ctx, m.client, m.storesGen))

	case storeDeletedMsg:
		m.endLoad()
		if m.staleMutation(msg.origin, m.storeDeleteGen) {
			return m, nil
		}
		m.storeDeleting = false
		m.mutationStatus = ""
		if msg.err != nil {
			return m, m.toastErr("delete store", msg.err)
		}
		m.connLost = false
		m.status = "store deleted"
		cmds := []tea.Cmd{m.toasts.Push(toast.Success, m.status)}
		// If the active store was deleted, clear it (a reload then auto-selects
		// the first remaining store, or leaves the playground store-less).
		if msg.id == m.storeID {
			m.storeID, m.storeName, m.modelID = "", "", ""
			m.modelIsLatest = false
			m.graph = fga.Graph{}
			m.models, m.tuples, m.changes, m.assertions, m.assertResults = nil, nil, nil, nil, nil
			m.history, m.hasResult = nil, false
			// The store itself is genuinely gone (the delete API call already
			// succeeded) regardless of whether clearing its id from the saved
			// profile succeeds, so this failure is additive — a separate error
			// toast alongside the "store deleted" success already queued above,
			// not a replacement of it (configSaveErrCmd would overwrite m.status,
			// which must keep reporting the delete's own genuine success).
			if err := m.persistStore(); err != nil {
				cmds = append(cmds, m.toasts.Push(toast.Error, "config not saved: "+err.Error()))
			}
		}
		m.beginLoad()
		// Bump storesGen: this refresh must not lose a race to (or win one
		// against) another in-flight stores dispatch out of order.
		m.storesGen++
		cmds = append(cmds, loadStoresCmd(m.ctx, m.client, m.storesGen))
		return m, tea.Batch(cmds...)

	case tupleWrittenMsg:
		m.endLoad()
		if m.staleMutation(msg.origin, m.tupleMutationGen) {
			return m, nil
		}
		m.tupleMutating = false
		m.mutationStatus = ""
		if msg.err != nil {
			return m, m.toastErr("tuple", msg.err)
		}
		m.connLost = false
		verb := "wrote"
		if msg.deleted {
			verb = "deleted"
		}
		m.status = verb + " " + msg.label
		m.beginLoad()
		m.tuplesGen++
		return m, tea.Batch(m.toasts.Push(toast.Success, m.status), loadTuplesCmd(m.ctx, m.client, m.storeID, m.tuplesGen))

	case queryResultMsg:
		stale := staleStore(msg.storeID, m.storeID) || staleModel(msg.modelID, m.modelID) || staleGen(msg.gen, m.queryGen)
		m.endLoad()
		if stale {
			return m, nil // superseded by a newer query submission or rerun
		}
		m.queryPendingGen = 0
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
		m.pushHistory(histEntry{mode: msg.mode, vals: msg.vals, ok: msg.ok, ms: msg.ms, qctx: msg.qctx})
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

	case apiLogMsg:
		if m.section == secAPILogs {
			if m.apiLogSel > 0 {
				m.apiLogSel++ // keep the pinned entry selected as newer ones arrive
			}
			m.refreshAPILogVP()
		}
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
		// bubbles/list filtering is asynchronous: Model.Update returns a command
		// that produces a list.FilterMatchesMsg, which must be fed back to the
		// list for the filter to actually narrow the rows. Nothing else forwards
		// it, so without this the "/" filter shows a filter prompt but every row
		// stays visible — and a following delete would hit the wrong (unfiltered)
		// row. Forward these async messages to the active section list — or to the
		// command palette when it's open, whose list is otherwise never fed its own
		// FilterMatchesMsg, leaving its "/" filter dead and navigation landing on
		// the wrong section.
		if m.paletteOpen {
			return m, m.paletteList.Update(msg)
		}
		if lst := m.activeList(); lst != nil {
			return m, lst.Update(msg)
		}
	}
	return m, nil
}

// toastErr surfaces a failed API call as a transient toast (and flags a
// possible connection loss). A normal API error stays out of the footer status
// line, but a connection failure is persisted there — a toast expires after a
// few seconds while the outage doesn't, and the user may not have been looking.
// It also collapses the storm of per-section toasts that a single unreachable
// server produces (stores + model + tuples + changes + assertions all fail at
// once) into one, so the stack isn't flooded with the same outage.
func (m *Model) toastErr(label string, err error) tea.Cmd {
	wasConnLost := m.connLost
	m.connLost = isConnErr(err)
	m.status = ""
	detail := errStr(err)
	if label != "" {
		detail = label + ": " + detail
	}
	if m.connLost {
		m.status = "connection failed: " + errStr(err)
		if wasConnLost {
			// Already in a connection-lost state (a concurrent failed load) —
			// the status line already carries it; don't stack another toast.
			return nil
		}
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
	// Note: this is a deliberate, unconfirmed hard exit — unlike Esc in the DSL
	// editor (which prompts before discarding unsaved edits, see below), Ctrl+C
	// quits immediately and discards any unsaved editor buffer. That is the
	// intended "get me out now" escape hatch.
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
		if m.confirm.require != "" {
			switch msg.String() {
			case "esc":
				m.confirm = nil
			case "enter":
				if m.confirm.input == m.confirm.require {
					run := m.confirm.run
					m.confirm = nil
					return m, run(&m)
				}
				m.status = "confirmation did not match"
			case "backspace":
				runes := []rune(m.confirm.input)
				if len(runes) > 0 {
					m.confirm.input = string(runes[:len(runes)-1])
				}
			default:
				if text := msg.Key().Text; text != "" {
					m.confirm.input += text
				}
			}
			return m, nil
		}
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
			if m.modelApplying {
				m.status = "model apply already in progress"
				return m, nil
			}
			m.editorErr = ""
			m.beginLoad()
			m.modelApplying = true
			m.modelApplyGen++
			m.mutationStatus = "applying model…"
			m.status = m.mutationStatus
			return m, applyModelCmd(m.ctx, m.client,
				m.mutationOrigin(m.storeID, m.modelID, m.modelApplyGen), m.editor.Value())
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
	// Digit section-jumps stay global even with the panel focused — the ?
	// overlay advertises "1–7 jump to a section" as global. The exception is
	// Tuple Queries, where the digits rerun recent history.
	if m.section != secQuery && len(key) == 1 && key[0] >= '1' && key[0] <= '8' {
		return m.gotoSection(section(key[0] - '1'))
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
	case "1", "2", "3", "4", "5", "6", "7", "8":
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
			m.clearResourcePending()
			// A rapid re-pick before the previous one lands must not let the
			// stale response overwrite the newer pick's graph/DSL.
			m.beginLoad()
			m.modelGen++
			return m, loadModelByIDCmd(m.ctx, m.client, m.storeID, id, m.modelGen)
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
	case secStores:
		m.selectCurrentStore()
	case secChanges:
		if m.storeID != "" && len(m.changes) == 0 {
			m.beginLoad()
			// A slow first load racing a second lazy trigger (e.g. leaving and
			// re-entering the tab before the first response lands — len(changes)
			// stays 0 either way) must not let the older response overwrite
			// whichever landed later.
			m.changesGen++
			return m, loadChangesCmd(m.ctx, m.client, m.storeID, m.changesGen)
		}
	case secAssertions:
		// Assertions are stored per authorization model, so reload them when the
		// tab is first opened or the selected model has changed since they were
		// loaded — otherwise the tab would keep running the first model's set
		// against a now-different selection. (Skip the model check when no model
		// is resolved yet, to avoid reloading on every entry.)
		if m.storeID != "" && (m.assertions == nil || (m.modelID != "" && m.assertModelID != m.modelID)) {
			m.beginLoad()
			m.assertLoadGen++
			return m, loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID, m.assertLoadGen)
		}
	case secQuery:
		// Descending into the panel starts in the first field, ready to type
		// (same as arriving via tab). Browsing tabs in the sidebar keeps
		// FocusSidebar, so it must not begin editing.
		if m.focus == shell.FocusPanel {
			return m, m.enterQueryEdit()
		}
	case secAPILogs:
		m.apiLogSel = 0
		m.apiLogHScroll = 0
		m.apiLogTab = 0
		m.refreshAPILogVP()
		m.apiLogVP.GotoTop()
		return m, nil
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
