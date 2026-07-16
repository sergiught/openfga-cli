package playground

import (
	"context"
	"errors"
	"net"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/harmonica"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/apilog"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/client"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/dsl"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

const modelTemplate = "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define owner: [user]\n    define viewer: [user] or owner\n"

// editorNoWrapWidth is the textarea's internal width. It is set far wider than
// any DSL line so the widget never soft-wraps, keeping logical line == visual
// row for our custom editor render.
const editorNoWrapWidth = 10000

type section int

const (
	secProfiles section = iota
	secStores
	secModel
	secTuples
	secChanges
	secQuery
	secAssertions
	secAPILogs
)

var sectionNames = []string{"Profiles", "Stores", "Model", "Tuples", "Changes", "Tuple Queries", "Assertions", "API Logs"}

// formKind identifies a full-panel form takeover.
type formKind int

const (
	formNone formKind = iota
	formCreateStore
	formWriteTuple
	formWriteAssertion
	formAddProfile
	formEditProfile
)

var queryModes = []string{"check", "list-objects", "list-users", "list-relations"}

// queryModeIndex returns the index of mode within queryModes, defaulting to 0
// ("check") if mode isn't recognized.
func queryModeIndex(mode string) int {
	for i, m := range queryModes {
		if m == mode {
			return i
		}
	}
	return 0
}

// queryFieldCount returns how many input fields a query mode's form carries.
// list-relations takes only User and Object — the relations it tests are
// derived from the model — while the other modes take three.
func queryFieldCount(mode string) int {
	if mode == "list-relations" {
		return 2
	}
	return 3
}

// histEntry is one rerunnable query-history record. Model.history holds up
// to 5, newest first (see pushHistory).
type histEntry struct {
	mode string
	vals [3]string
	ok   bool
	ms   int64
	qctx queryCtx // ABAC context + contextual tuples the query ran with, so rerun applies them
}

// confirmAction is a destructive action awaiting confirmation in a modal. The
// question reads "<action> <subject>?", with subject highlighted so it's clear
// exactly what is affected; detail is an optional faint note below it. run
// performs the action when confirmed and may mutate the model and return a cmd.
type confirmAction struct {
	action  string
	subject string
	detail  string
	require string
	input   string
	run     func(m *Model) tea.Cmd
}

// Model is the task-pilot-style playground model.
type Model struct {
	cli    *cli.CLI
	client *openfga.Client
	ctx    context.Context

	recorder      *apilog.Recorder
	apiLogSel     int  // selection index into Snapshot, 0 = newest (top)
	apiLogHScroll int  // horizontal scroll offset for the selected row's URL
	apiLogTab     int  // active detail sub-section (index into apiLogTabs)
	apiLogPretty  bool // pretty-print JSON bodies in the detail pane
	apiLogVP      viewport.Model
	apiLogVPInit  bool

	width, height int
	ready         bool

	// launch entrance: entranceFrac springs 1→0 (driven by entranceSpring)
	// while entering is true; the shell uses it to slide the sidebar in and
	// ghost the main pane. drift is the ambient gradient phase (0→1, wraps)
	// that animates the wordmark and active nav pill continuously, on its
	// own perpetual ticker, independent of the entrance.
	entering       bool
	entranceFrac   float64
	entranceVel    float64
	entranceSpring harmonica.Spring
	drift          float64

	fading bool // true while rendering incoming section as a ghost frame

	storeID       string
	storeName     string
	modelID       string
	modelIsLatest bool // current model is the store's newest
	profile       string
	apiURL        string

	section section
	focus   shell.Focus // FocusSidebar (tab selection) or FocusPanel (right pane)
	compact bool        // Tuples/Changes/Assertions render as a dense full-width list (session-only)
	sh      *shell.Shell
	version string

	spinner spinner.Model
	loading bool
	// pendingLoads counts in-flight background loads (stores, model, tuples,
	// changes, assertions, model list, resolution tree, assertion runs,
	// queries). loading mirrors "pendingLoads > 0" and is only ever changed via
	// beginLoad/endLoad, so several loads started together (e.g. Init's startup
	// batch, or selecting a store, which kicks off model+tuples+changes+
	// assertions at once) all have to land before the spinner stops — one of
	// them landing first can no longer stop it prematurely.
	pendingLoads int
	// modelGen, queryGen, resGen and assertGen are bumped whenever a request of
	// that kind is (re)dispatched. Each carries its generation on the message it
	// eventually produces, so a response from a superseded request — e.g. two
	// quick picks in the model switcher, or a query rerun before the first
	// finished — is detected and dropped instead of clobbering newer state.
	// newModel seeds these at 1 (see staleGen: 0 is reserved to mean "untagged,
	// always current" for tests/messages built without a generation).
	modelGen  int
	queryGen  int
	resGen    int
	assertGen int
	// storesGen is bumped both on reconnect (a profile switch or an edit to the
	// active profile's connection, both funneled through activateResolved) and on
	// every other stores-list dispatch (manual reload, and the refresh after
	// creating/deleting a store). It is stamped on the stores list load:
	// unlike every other load, that one has no store id of its own to check
	// staleness against, so it needs its own generation for both purposes —
	// a stores list from a connection that's since been replaced could
	// otherwise repopulate the list from the wrong server (or even auto-select
	// a store id that doesn't exist on the new one — see the storeID == ""
	// branch of storesLoadedMsg), and two same-connection refreshes (e.g. a
	// manual "r" racing the refresh a create/delete already triggers) could
	// otherwise land out of order and let the older one win.
	storesGen int
	// connGen changes whenever the resolved client/profile connection changes.
	// Every mutation captures it so an old server's completion cannot mutate
	// or persist state after a reconnect, even when store/model IDs match.
	connGen int
	// modelsGen is bumped each time the model-switcher's list is (re)loaded,
	// i.e. every time it's opened. Kept separate from modelGen: picking a model
	// bumps modelGen but must not invalidate an in-flight list request (and
	// vice versa) — they are independent requests that just happen to both
	// concern models.
	modelsGen int
	// tuplesGen and changesGen are bumped whenever their respective loads are
	// re-dispatched against the same store (a manual reload, the reload a
	// tuple write triggers, a reconnect, or the lazy first-entry load racing a
	// manual reload before it lands), so an older response landing late can't
	// clobber a newer one that already applied.
	tuplesGen  int
	changesGen int
	// assertLoadGen is bumped whenever the assertions list itself is
	// (re)loaded. Kept separate from assertGen (running the loaded assertions)
	// and resGen (a check's resolution tree): those are independent requests
	// against an already-loaded set, not reloads of the set itself.
	assertLoadGen int
	// spinnerRunning tracks whether the spinner tick loop is active, so it can
	// be stopped when idle and restarted on the next load instead of ticking
	// forever.
	spinnerRunning bool
	status         string
	mutationStatus string

	storeCreating     bool
	storeDeleting     bool
	modelApplying     bool
	tupleMutating     bool
	assertionsWriting bool
	// pendingTupleSelect/pendingAssertionSelect hold the id of a row just written,
	// so the reload that follows re-selects it instead of jumping to the top.
	pendingTupleSelect     string
	pendingAssertionSelect string
	queryPendingGen        int
	storeCreateGen         int
	storeDeleteGen         int
	modelApplyGen          int
	tupleMutationGen       int
	assertionWriteGen      int

	toasts toast.Model

	// connLost flips the footer to a solid "connection lost" dot when the last
	// command failed with a network-level error.
	connLost bool

	// data + lists
	// profiles are backed by config (no async load); profileEditName holds the
	// profile being edited while the edit form is open.
	profilesList      *uilist.List
	profileEditName   string
	profileAuthMethod string // auth method the open add/edit-profile form is built for

	stores     []openfga.Store
	storesList *uilist.List
	// confirm holds the destructive action awaiting a confirmation modal, or nil
	// when no modal is open. It is shared by every delete (store, tuple,
	// assertion, profile) so they all confirm the same way.
	confirm *confirmAction

	tuples       []openfga.Tuple
	tuplesList   *uilist.List
	tuplesCapped bool // more tuples exist than are shown (hit the display cap)

	models       []openfga.AuthorizationModel
	modelsList   *uilist.List
	modelPicking bool

	graph   fga.Graph
	graphVP viewport.Model

	// graph viewport spring scrolling: graphPos is the animated (fractional)
	// scroll offset that eases toward graphTarget via graphSpring.
	graphSpring    harmonica.Spring
	graphPos       float64
	graphVel       float64
	graphTarget    float64
	graphAnimating bool

	changesCapped bool // more changes exist than are shown (hit the display cap)
	changes       []openfga.TupleChange
	changesList   *uilist.List

	assertions     []openfga.Assertion
	assertionsList *uilist.List
	assertResults  []assertResult
	assertSummary  string
	assertModelID  string
	formErr        string // form-validation / write error, shown in a modal for every form

	// query
	qmode        int
	qform        *field.Form
	qShowContext bool // reveal the Context (JSON) + contextual-tuples fields
	editing      bool // a form (query or takeover) is capturing keys
	hasResult    bool
	result       queryResultMsg
	history      []histEntry // rerunnable results, newest first, capped at 5
	flash        bool        // true for one frame right after a badge result lands

	// check resolution tree (Expand), shown over the query result
	resVP       viewport.Model
	resTree     *fga.ResNode
	showRes     bool
	resPathOnly bool // collapse to just the granting branch (ACL path) vs full tree

	// full-panel form takeover
	formKind      formKind
	form          *field.Form
	assertEditIdx int // index being edited in the assertion form; -1 = adding

	paletteOpen bool
	paletteList *uilist.List

	// helpOpen shows the ? keybinding overlay.
	helpOpen bool

	// DSL model editor
	editorOpen    bool
	editor        textarea.Model
	editorTop     int // first visible logical line, managed by reflowEditorScroll
	editorErr     string
	editorDiags   []dsl.Diagnostic
	lastEditorDSL string // last DSL value diagnostics were computed for
	modelDSL      string // DSL of the currently-loaded model, for edit pre-fill
}

func newModel(ctx context.Context, cli *cli.CLI, cl *openfga.Client, storeID, modelID string) Model {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipgloss.NewStyle().Foreground(style.Primary)))

	// A lightly-damped spring gives scrolling momentum without overshoot.
	graphSpring := harmonica.NewSpring(harmonica.FPS(graphFPS), 8.0, 1.0)
	entranceSpring := harmonica.NewSpring(harmonica.FPS(30), 7.0, 0.9)

	// The mono theme boots settled — no motion to speak of, so the entrance
	// animation is skipped entirely. Reduced motion (an explicit opt-out, or an
	// environment where constant repaints are costly/ugly) skips it too — the
	// same intent that already suppresses the ambient gradient drift.
	entering, entranceFrac := true, 1.0
	if style.Active.Name == "mono" || reducedMotion() {
		entering, entranceFrac = false, 0
	}

	ta := textarea.New()
	ta.ShowLineNumbers = false // we draw our own gutter
	ta.MaxWidth = editorNoWrapWidth
	ta.SetWidth(editorNoWrapWidth)

	profile := cli.Config.Active
	apiURL := config.DefaultAPIURL
	if r, err := cli.Resolve(); err == nil {
		profile = r.Profile
		apiURL = r.APIURL
	}

	m := Model{
		cli:            cli,
		client:         cl,
		ctx:            ctx,
		spinner:        sp,
		sh:             shell.New(),
		entering:       entering,
		entranceFrac:   entranceFrac,
		entranceSpring: entranceSpring,
		section:        secProfiles,
		apiLogPretty:   true,
		version:        cli.Version,
		storeID:        storeID,
		modelID:        modelID,
		profile:        profile,
		apiURL:         apiURL,
		graphSpring:    graphSpring,
		loading:        true,
		pendingLoads:   initialPendingLoads(storeID),
		// Generations start at 1, not 0: 0 is reserved as the "untagged"
		// sentinel (see staleGen) so messages built by tests/code that don't
		// set a generation are never mistaken for stale. Starting real
		// generations at 1 means even the very first dispatch (Init's startup
		// load) is a comparable, supersedable generation.
		modelGen:       1,
		queryGen:       1,
		resGen:         1,
		assertGen:      1,
		storesGen:      1,
		connGen:        1,
		modelsGen:      1,
		tuplesGen:      1,
		changesGen:     1,
		assertLoadGen:  1,
		spinnerRunning: true, // Init starts the tick loop for the initial load
		status:         "loading stores…",
		profilesList:   uilist.New(),
		storesList:     uilist.New(),
		tuplesList:     uilist.New(),
		modelsList:     uilist.New(),
		changesList:    uilist.New(),
		assertionsList: uilist.New(),
		paletteList:    uilist.New(),
		editor:         ta,
		toasts:         toast.New(),
	}
	// Advertise the "/" filter on every section list and hint at what each one
	// matches on. Filtering is gated behind "/" (single letters are section
	// actions like n/e/d/r), so the hint is the main way users discover it.
	// Placeholders stay short: the section lists render in a ~40%-width master
	// pane, so anything much longer than the "match any field" hint truncates.
	tuplePlaceholder := "match any field"
	for _, fl := range []struct {
		list        *uilist.List
		placeholder string
	}{
		{m.profilesList, "profile name"},
		{m.storesList, "name or id"},
		{m.tuplesList, tuplePlaceholder},
		{m.changesList, tuplePlaceholder},
		{m.assertionsList, tuplePlaceholder},
	} {
		fl.list.SetFilterHint("press / to filter")
		fl.list.SetFilterPlaceholder(fl.placeholder)
	}

	m.qmode = 0
	m.populatePalette()
	m.populateProfiles()
	if storeID == "" {
		m.status = "no store selected — pick one in Stores"
	}
	return m
}

// initialPendingLoads returns how many concurrent background loads Init()
// dispatches for a freshly constructed model: the stores list always, plus —
// when a store is already selected (e.g. restored from config) — the model,
// tuples, changes and assertions loads it also kicks off together. newModel
// seeds pendingLoads with this so the spinner started there already accounts
// for every load Init() is about to fire; keep this in sync with Init()'s cmd
// list below by hand, since Init can't mutate the model that already carries
// pendingLoads (its own local receiver copy is discarded after it returns).
func initialPendingLoads(storeID string) int {
	if storeID == "" {
		return 1 // stores only
	}
	return 5 // stores, model, tuples, changes, assertions
}

// Init kicks off initial loads.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, loadStoresCmd(m.ctx, m.client, m.storesGen)}
	if m.entering {
		cmds = append(cmds, entranceTick())
	}
	if style.Active.Name != "mono" && !reducedMotion() {
		cmds = append(cmds, driftTick())
	}
	if m.storeID != "" {
		cmds = append(cmds,
			m.startModelCmd(),
			loadTuplesCmd(m.ctx, m.client, m.storeID, m.tuplesGen),
			loadChangesCmd(m.ctx, m.client, m.storeID, m.changesGen),
			loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID, m.assertLoadGen),
		)
	}
	return tea.Batch(cmds...)
}

// startModelCmd loads the model to show on startup (or after a profile switch):
// the specific persisted model when one is configured, otherwise the store's
// latest. Loading the persisted model keeps a deliberately-chosen older model
// selected across restarts instead of snapping back to latest.
//
// Value receiver by design: it is called from Init(), whose own mutations to
// its local model copy are discarded (bubbletea never persists Init's
// receiver), so it deliberately does not bump modelGen — it uses whatever
// generation newModel already seeded, matching initialPendingLoads' budget.
func (m Model) startModelCmd() tea.Cmd {
	if m.modelID != "" {
		return loadModelByIDCmd(m.ctx, m.client, m.storeID, m.modelID, m.modelGen)
	}
	return loadModelCmd(m.ctx, m.client, m.storeID, m.modelGen)
}

// Run launches the playground.
func Run(ctx context.Context, cli *cli.CLI) error {
	r, err := cli.Resolve()
	if err != nil {
		return err
	}
	rec := apilog.NewRecorder(apiLogHistory)
	cl, err := client.New(r, client.WithCapture(rec), client.WithTimeout(cli.RequestTimeout))
	if err != nil {
		return err
	}
	// On first run, materialize a starter config.toml (default profile, default
	// API URL, no store/model yet) so the file exists to be updated as the user
	// picks a store and model in the TUI. This is non-fatal — the playground can
	// still run without it, just without persisting selections — but the
	// failure must still be visible to the user rather than silently swallowed
	// at debug level.
	if !cli.Config.Existed() {
		if err := cli.SaveConfig(); err != nil {
			cli.Logger.Warn("failed to write initial config", "error", err)
		}
	}
	m := newModel(ctx, cli, cl, r.StoreID, r.ModelID)
	m.recorder = rec
	icons.Apply(icons.Parse(cli.Config.IconsMode()))
	// Bind the program to the interrupt-aware context so Ctrl-C / SIGINT tears
	// the TUI down cleanly and cancels any in-flight requests it started.
	p := tea.NewProgram(m, tea.WithContext(ctx))
	rec.SetNotify(func() { p.Send(apiLogMsg{}) })
	_, err = p.Run()
	return err
}

// --- sizing ---

func (m *Model) resize() {
	m.sh.SetSize(m.width, m.height)
	w, h := m.sh.MainSize()
	lw := splitListWidth(w)
	m.profilesList.SetSize(lw, h)
	m.storesList.SetSize(lw, h)
	// Tuples/Changes/Assertions collapse to a full-width bare list in compact
	// mode (see m.compact), so they size to the full width instead of the
	// list/detail split's list width.
	tw := lw
	if m.compact {
		tw = w
	}
	m.tuplesList.SetSize(tw, h)
	m.changesList.SetSize(tw, h)
	// The assertions panel reserves one line for the pass/fail tally, but only
	// once a run has produced one. Like the other sections it is a list/detail
	// split, so the list is sized to the split's list width.
	ah := h
	if m.assertHasResults() {
		ah = h - 1
	}
	if ah < 1 {
		ah = 1
	}
	m.assertionsList.SetSize(tw, ah)
	// Dialog-hosted lists (palette, model switcher) must fit the modal's
	// interior budget, not the full main pane — otherwise the dialog grows
	// taller than the terminal and its rounded corners clip off-screen.
	dw, dh := m.sh.DialogSize()
	if dh > 12 {
		dh = 12
	}
	m.paletteList.SetSize(dw, dh)
	m.modelsList.SetSize(dw, dh)
	if m.form != nil {
		formW, formH := m.sh.DialogSize()
		m.form.SetWidth(formW)
		m.form.SetHeight(formH)
	}
	if m.graphVP.Width() == 0 {
		// First time: create the viewport and populate it.
		m.graphVP = viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
		if len(m.graph.Types) > 0 {
			m.graphVP.SetContent(m.graph.RenderDiagram())
		}
	} else {
		// Resize: just update dimensions to preserve scroll offset.
		m.graphVP.SetWidth(w)
		m.graphVP.SetHeight(h)
	}
	m.editor.SetWidth(editorNoWrapWidth) // fixed no-wrap width; display width handled by render
	m.editor.SetHeight(h - 2)
	// A resize can shrink the editor height enough to push the cursor's line
	// outside the visible window; reflow now so it doesn't sit off-screen
	// until the next keypress.
	if m.editorOpen {
		m.reflowEditorScroll()
	}
	// Resolution viewport: a couple of rows below the query header/hint.
	rh := h - 2
	if rh < 1 {
		rh = 1
	}
	if m.resVP.Width() == 0 {
		m.resVP = viewport.New(viewport.WithWidth(w), viewport.WithHeight(rh))
	} else {
		m.resVP.SetWidth(w)
		m.resVP.SetHeight(rh)
	}
	// API Logs detail viewport follows the same list/detail split; resize it
	// (preserving scroll) so the response body stays scrollable after a
	// terminal resize.
	if m.apiLogVPInit {
		m.refreshAPILogVP()
	}
	// Preserve any in-progress query input across the rebuild: WindowSizeMsg can
	// arrive mid-typing and async loads (assertions, etc.) also call resize().
	// The mode/context is unchanged here, so the fields line up 1:1.
	if m.qform != nil {
		vals := m.qform.Values()
		m.rebuildQueryForm()
		m.qform.SetValues(vals)
	} else {
		m.rebuildQueryForm()
	}
}

func (m *Model) contentSize() (int, int) { return m.sh.MainSize() }

// refreshResVP re-renders the resolution viewport for the current mode: the
// full tree, or (ACL path) collapsed to just the branch that grants the user.
func (m *Model) refreshResVP() {
	if m.resTree == nil {
		return
	}
	// vals carry the query the resolution ran for: [user, relation, object].
	user, relation, object := m.result.vals[0], m.result.vals[1], m.result.vals[2]
	if m.resPathOnly {
		if p := fga.GrantedPath(m.resTree); p != nil {
			m.resVP.SetContent(fga.RenderResolution(p, user, object, relation))
		} else {
			m.resVP.SetContent(style.Faint.Render("no granting path — this relation doesn't resolve to the user"))
		}
		return
	}
	m.resVP.SetContent(fga.RenderResolution(m.resTree, user, object, relation))
}

// --- list population ---

func (m *Model) populateProfiles() {
	names := m.cli.Config.ProfileNames()
	items := make([]uilist.Item, len(names))
	for i, name := range names {
		p, _ := m.cli.Config.Get(name)
		safeName := safeText(name)
		desc := safeText(p.APIURL)
		if name == m.profile {
			desc = "active · " + desc
		}
		items[i] = uilist.Item{TitleText: safeName, DescText: desc, Filter: safeName, ID: name, Index: i}
	}
	m.profilesList.SetItems(items)
	m.profilesList.SelectID(m.profile)
}

func (m *Model) populateStores() {
	items := make([]uilist.Item, len(m.stores))
	for i, s := range m.stores {
		name, id := safeText(s.Name), safeText(s.ID)
		items[i] = uilist.Item{TitleText: name, DescText: id, Filter: name + " " + id, ID: s.ID, Index: i}
	}
	m.storesList.SetItems(items)
	m.selectCurrentStore()
}

func (m *Model) selectCurrentStore() {
	m.storesList.SelectID(m.storeID)
}

func (m *Model) populateTuples() {
	userW := 0
	if m.compact {
		for _, t := range m.tuples {
			if w := lipgloss.Width(safeText(t.Key.User)); w > userW {
				userW = w
			}
		}
	}
	items := make([]uilist.Item, len(m.tuples))
	for i, t := range m.tuples {
		user := safeText(t.Key.User)
		title := user
		desc := safeText(t.Key.Relation) + " → " + safeText(t.Key.Object)
		if m.compact {
			pad := strings.Repeat(" ", userW-lipgloss.Width(user))
			title = user + pad + "  " + desc
			desc = ""
		}
		items[i] = uilist.Item{
			TitleText: title,
			DescText:  desc,
			Filter:    safeText(fga.FormatTuple(t.Key)),
			ID:        fga.FormatTuple(t.Key),
			Index:     i,
		}
	}
	m.tuplesList.SetCompact(m.compact)
	m.tuplesList.SetItems(items)
	if m.pendingTupleSelect != "" {
		m.tuplesList.SelectID(m.pendingTupleSelect)
		m.pendingTupleSelect = ""
	}
}

func (m *Model) populateModels() {
	items := make([]uilist.Item, len(m.models))
	for i, md := range m.models {
		desc := "schema " + safeText(md.SchemaVersion)
		if i == 0 {
			desc += "  · latest"
		}
		id := safeText(md.ID)
		items[i] = uilist.Item{TitleText: id, DescText: desc, Filter: id, ID: md.ID, Index: i}
	}
	m.modelsList.SetItems(items)
}

func (m *Model) populateChanges() {
	items := make([]uilist.Item, len(m.changes))
	for i, ch := range m.changes {
		op := "＋ write"
		if ch.Operation == "TUPLE_OPERATION_DELETE" {
			op = "－ delete"
		}
		ts := ch.Timestamp.Format("2006-01-02 15:04:05")
		tuple := safeText(fga.FormatTuple(ch.TupleKey))
		title := tuple
		desc := ts + "  " + op
		if m.compact {
			title = ts + "  " + op + "   " + tuple
			desc = ""
		}
		items[i] = uilist.Item{
			TitleText: title,
			DescText:  desc,
			Filter:    tuple,
			Index:     i,
		}
	}
	m.changesList.SetCompact(m.compact)
	m.changesList.SetItems(items)
}

func (m *Model) populateAssertions() {
	items := make([]uilist.Item, len(m.assertions))
	for i, a := range m.assertions {
		exp := "expect allow"
		if !a.Expectation {
			exp = "expect deny"
		}
		filter := safeText(a.TupleKey.User) + " " + safeText(a.TupleKey.Relation) + " " + safeText(a.TupleKey.Object)
		title := filter
		desc := exp
		if i < len(m.assertResults) && m.assertResults[i].ran {
			r := m.assertResults[i]
			if r.pass {
				desc = style.Success.Render(style.IconCheck+" PASS") + style.Faint.Render(" · "+exp)
			} else {
				desc = style.Failure.Render(style.IconCross+" FAIL") + style.Faint.Render(" · got "+boolWord(r.got))
			}
		}
		if m.compact {
			title = desc + "  " + title
			desc = ""
		}
		items[i] = uilist.Item{TitleText: title, DescText: desc, Filter: filter, ID: filter, Index: i}
	}
	m.assertionsList.SetCompact(m.compact)
	m.assertionsList.SetItems(items)
	if m.pendingAssertionSelect != "" {
		m.assertionsList.SelectID(m.pendingAssertionSelect)
		m.pendingAssertionSelect = ""
	}
}

// assertHasResults reports whether any assertion has been run (and thus has a
// badge / tally to show).
func (m Model) assertHasResults() bool {
	for _, r := range m.assertResults {
		if r.ran {
			return true
		}
	}
	return false
}

func assertResultWord(r assertResult) string {
	if r.pass {
		return "assertion passed"
	}
	return "assertion FAILED (got " + boolWord(r.got) + ")"
}

func (m *Model) populatePalette() {
	items := make([]uilist.Item, len(sectionNames))
	for i, name := range sectionNames {
		items[i] = uilist.Item{TitleText: "Go to " + name, DescText: "section " + itoa(i+1), Filter: name, ID: itoa(i), Index: i}
	}
	m.paletteList.SetItems(items)
}

func (m *Model) rebuildQueryForm() {
	w, h := m.contentSize()
	// Leave a small margin for the fields' own focus accent so they don't
	// touch the panel edge or get clipped.
	w -= 2
	if w < 1 {
		w = 1
	}
	m.qform = buildQueryForm(queryModes[m.qmode], w, m.qShowContext)
	m.qform.SetHeight(h - 2)
	m.qform.SetHighlight(style.FieldHighlight())
	m.qform.Init()
}

// cycleQueryMode advances the active query mode by dir (+1 next, -1 previous),
// wrapping around, and rebuilds the form for the new mode. Centralizing the
// wrap keeps the backward direction from underflowing.
func (m *Model) cycleQueryMode(dir int) {
	n := len(queryModes)
	m.qmode = (m.qmode + dir + n) % n
	m.rebuildQueryForm()
	m.hasResult = false
}

// setQueryError shows msg as the persistent query error in the panel body
// (red), cleared only by the next query. Every query error — an API failure,
// bad context / contextual tuples, or missing fields — is surfaced this way
// (not as a transient toast) so error presentation is consistent.
// It does not touch loading/pendingLoads itself: most callers invoke this
// before any load has begun (a form validation failure), so clearing it here
// unconditionally would wrongly stop the spinner for an unrelated in-flight
// load. Callers that do begin a load before validating (rerunHistory) must
// call endLoad themselves alongside this.
func (m *Model) setQueryError(msg string) {
	m.result = queryResultMsg{err: errors.New(msg)}
	m.hasResult = true
	m.showRes = false
	m.resTree = nil
	m.status = "query failed"
}

// enterQueryEdit focuses the query form so the first field captures typing,
// guarding on a selected store. It returns the field's cursor-blink command
// (nil when there is no store yet).
func (m *Model) enterQueryEdit() tea.Cmd {
	if m.storeID == "" {
		m.status = "select a store first"
		return nil
	}
	m.editing = true
	return m.qform.Init()
}

// pushHistory records a query result at the front of the history, newest
// first, capped at 5 entries.
func (m *Model) pushHistory(h histEntry) {
	m.history = append([]histEntry{h}, m.history...)
	if len(m.history) > 5 {
		m.history = m.history[:5]
	}
}

// --- store selection ---

func (m *Model) selectStore(s openfga.Store) tea.Cmd {
	m.clearResourcePending()
	m.storeID = s.ID
	m.storeName = s.Name
	m.modelID = ""
	m.modelIsLatest = false
	m.graph = fga.Graph{}
	m.models = nil // the previous store's models must not linger in the picker
	m.tuples = nil
	m.changes = nil
	m.assertions = nil
	m.assertResults = nil
	m.history = nil
	m.hasResult = false
	m.result = queryResultMsg{}
	m.rebuildQueryForm()
	// A failed persist must never be reported as a clean "loaded store" success
	// (and must not be silently clobbered by that status a moment later) — see
	// persistStore's doc comment.
	var extra tea.Cmd
	if err := m.persistStore(); err != nil {
		extra = m.configSaveErrCmd(err)
	} else {
		m.status = "loaded store " + s.Name
	}
	// Bump every per-kind generation before dispatching: staleStore alone
	// isn't enough to protect a store re-selection cycle (A -> B -> A) — once
	// the store is back to A, a still-in-flight request from the *original* A
	// selection matches the current store id again and, without its own
	// generation bump, would look just as current as the new A selection's
	// request and could overwrite it. This covers both the four loads
	// dispatched immediately below and the categories that aren't redispatched
	// here (model list, query, resolution, assertion run) but could still have
	// a stale in-flight request from the previous store selection.
	m.modelGen++
	m.modelsGen++
	m.tuplesGen++
	m.changesGen++
	m.assertLoadGen++
	m.queryGen++
	m.resGen++
	m.assertGen++
	// Four concurrent loads start together here; begin each so the spinner
	// stays on until all four have landed, not just the first.
	m.beginLoad()
	m.beginLoad()
	m.beginLoad()
	m.beginLoad()
	return tea.Batch(extra,
		loadModelCmd(m.ctx, m.client, m.storeID, m.modelGen),
		loadTuplesCmd(m.ctx, m.client, m.storeID, m.tuplesGen),
		loadChangesCmd(m.ctx, m.client, m.storeID, m.changesGen),
		loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID, m.assertLoadGen),
	)
}

// clearResourcePending invalidates work tied to the selected store/model while
// preserving connection-wide store creation/deletion. Late completions retain
// their origin and are dropped without disturbing newer work.
func (m *Model) clearResourcePending() {
	m.modelApplyGen++
	m.tupleMutationGen++
	m.assertionWriteGen++
	m.modelApplying = false
	m.tupleMutating = false
	m.assertionsWriting = false
	m.queryPendingGen = 0
	if !m.storeCreating && !m.storeDeleting {
		m.mutationStatus = ""
	}
	m.assertions = nil
	m.assertResults = nil
	m.assertSummary = ""
	m.assertModelID = ""
	m.populateAssertions()
	m.history = nil
	m.hasResult = false
	m.result = queryResultMsg{}
	m.showRes = false
	m.resTree = nil
}

// persistStore records the selected store on the active profile and saves,
// clearing the profile's model id (a new store invalidates the old model; the
// model that loads next re-records it). No-op when the profile already reflects
// this store with no model, so an ordinary launch doesn't rewrite the file.
// Returns any save failure so the caller can avoid reporting a false success —
// it never reports success itself (it has no status/toast of its own), leaving
// that entirely to the caller.
func (m *Model) persistStore() error {
	if m.cli.Overrides.APIURL != "" {
		// The connection came from a one-shot --api-url override, not the saved
		// profile URL. Writing this server's store/model ids onto that profile
		// would point the next flagless launch at the wrong server / a
		// nonexistent store, so don't persist them.
		return nil
	}
	active := m.profile
	p, ok := m.cli.Config.Get(active)
	if !ok {
		return nil
	}
	if p.StoreID == m.storeID && p.ModelID == "" {
		return nil
	}
	prev := p
	p.StoreID = m.storeID
	p.ModelID = ""
	m.cli.Config.Set(active, p)
	if err := m.saveConfig(); err != nil {
		// Roll back the in-memory profile so it doesn't diverge from what's
		// actually persisted on disk.
		m.cli.Config.Set(active, prev)
		return err
	}
	return nil
}

// persistModel records the loaded model id on the active profile and saves.
// No-op when unchanged, so reloading an already-recorded model on launch
// doesn't rewrite the file. Returns any save failure so the caller can avoid
// reporting a false success.
func (m *Model) persistModel() error {
	if m.cli.Overrides.APIURL != "" {
		// See persistStore: a flag URL override must not write ids back onto the
		// saved profile.
		return nil
	}
	active := m.profile
	p, ok := m.cli.Config.Get(active)
	if !ok {
		return nil
	}
	if p.ModelID == m.modelID {
		return nil
	}
	prev := p
	p.ModelID = m.modelID
	m.cli.Config.Set(active, p)
	if err := m.saveConfig(); err != nil {
		m.cli.Config.Set(active, prev)
		return err
	}
	return nil
}

// saveConfig persists the config, returning any write failure so every caller
// must explicitly decide how to surface it — none may report a success
// status/toast once this has returned an error (see beginLoad/endLoad's sibling
// helper below, configSaveErrCmd, which every caller uses to do so
// consistently). It skips the write when the config has no resolved on-disk
// location, so it never guesses a path.
func (m *Model) saveConfig() error {
	if m.cli.Config.Path() == "" {
		return nil
	}
	return m.cli.SaveConfig()
}

func (m *Model) saveConfigWithSecretCleanup(profile string, all bool, fields ...string) (bool, error) {
	if m.cli.Config.Path() == "" {
		return true, nil
	}
	return m.cli.SaveConfigWithSecretCleanup(profile, all, fields...)
}

// configSaveErrCmd records a config-save failure as the visible status line
// and returns a command that also pushes it as an error toast, so a failed
// persist is never invisible and never quietly overwritten by a
// success-looking status set immediately after (the bug this fixes: callers
// used to set this status and then unconditionally overwrite it with a
// success message on the very next line).
func (m *Model) configSaveErrCmd(err error) tea.Cmd {
	m.status = "config not saved: " + err.Error()
	return m.toasts.Push(toast.Error, m.status)
}

// switchProfile makes name the active profile and reconnects to it.
func (m *Model) switchProfile(name string) tea.Cmd {
	if name == m.profile && name == m.cli.Config.Active {
		m.status = "already on profile " + name
		return nil
	}
	prevOverride := m.cli.Overrides.Profile
	m.cli.Overrides.Profile = name
	r, cl, err := m.resolvedClient()
	if err != nil {
		m.cli.Overrides.Profile = prevOverride
		return m.toastErr("profile", err)
	}
	prevActive := m.cli.Config.Active
	if err := m.cli.Config.Use(name); err != nil {
		m.cli.Overrides.Profile = prevOverride
		return m.toastErr("profile", err)
	}
	if err := m.saveConfig(); err != nil {
		// Roll back so the in-memory active profile matches what's actually on
		// disk, and never proceed to reconnect (a success-looking action) on a
		// failed save.
		_ = m.cli.Config.Use(prevActive)
		m.cli.Overrides.Profile = prevOverride
		return m.configSaveErrCmd(err)
	}
	// An explicit in-TUI switch is more recent than the process's initial
	// --profile/OPENFGA_PROFILE selection. Record it as a session override so
	// Resolve cannot silently reconnect to the old environment-selected
	// profile while the UI claims the newly selected profile is active.
	m.cli.Overrides.Profile = name
	m.profile = name
	return m.activateResolved(r, cl, "switched to profile "+name)
}

func (m *Model) resolvedClient() (config.Resolved, *openfga.Client, error) {
	r, err := m.cli.Resolve()
	if err != nil {
		return config.Resolved{}, nil, err
	}
	cl, err := client.New(r, client.WithCapture(m.recorder), client.WithTimeout(m.cli.RequestTimeout))
	return r, cl, err
}

func (m *Model) activateResolved(r config.Resolved, cl *openfga.Client, status string) tea.Cmd {
	m.connGen++
	m.storeCreateGen++
	m.storeDeleteGen++
	m.modelApplyGen++
	m.tupleMutationGen++
	m.assertionWriteGen++
	m.client = cl
	m.profile = r.Profile
	m.apiURL = r.APIURL

	// Reset every per-connection field, then adopt the profile's store/model.
	m.storeID = r.StoreID
	m.storeName = ""
	m.modelID = r.ModelID
	m.modelIsLatest = false
	m.stores = nil
	m.graph = fga.Graph{}
	m.models = nil
	m.tuples = nil
	m.changes = nil
	m.assertions = nil
	m.assertResults = nil
	m.assertSummary = ""
	m.history = nil
	m.hasResult = false
	m.result = queryResultMsg{}
	m.showRes = false
	m.resTree = nil
	m.connLost = false
	m.storeCreating = false
	m.storeDeleting = false
	m.modelApplying = false
	m.tupleMutating = false
	m.assertionsWriting = false
	m.mutationStatus = ""
	m.queryPendingGen = 0
	m.rebuildQueryForm()
	m.populateProfiles()
	m.populateStores()
	m.status = status

	// Reconnecting fires the same batch of concurrent loads Init() does on
	// first launch (see initialPendingLoads); begin exactly that many so the
	// spinner stays on until all of them land. Bump every per-kind generation
	// this batch (re)dispatches, plus every generation that isn't redispatched
	// here but could still have a request in flight from before the reconnect
	// (the model list, a query, a resolution tree, an assertion run): unlike
	// Init() (a value receiver whose mutations never persist), this is a
	// pointer receiver, so it can — and should — invalidate every one of them.
	// This matters even when the store/model ids happen to be unchanged (e.g.
	// editing the active profile's URL keeps the same persisted store and
	// model ids) — a response from the old connection landing late would
	// otherwise pass every storeID/modelID check and clobber the new
	// connection's view with stale data from a different server. storesGen
	// additionally covers the stores list itself, which has no store id of its
	// own to check.
	m.beginLoad()
	m.storesGen++
	m.modelGen++
	m.tuplesGen++
	m.changesGen++
	m.assertLoadGen++
	m.modelsGen++
	m.queryGen++
	m.resGen++
	m.assertGen++
	cmds := []tea.Cmd{loadStoresCmd(m.ctx, m.client, m.storesGen)}
	if m.storeID != "" {
		m.beginLoad()
		m.beginLoad()
		m.beginLoad()
		m.beginLoad()
		cmds = append(cmds,
			m.startModelCmd(),
			loadTuplesCmd(m.ctx, m.client, m.storeID, m.tuplesGen),
			loadChangesCmd(m.ctx, m.client, m.storeID, m.changesGen),
			loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID, m.assertLoadGen),
		)
	}
	return tea.Batch(cmds...)
}

// currentStoreName resolves the active store's display name, preferring the
// cached name (set on selection) and otherwise looking it up in the loaded
// stores list — so the footer labels the store as soon as the list arrives.
func (m Model) currentStoreName() string {
	if m.storeName != "" {
		return m.storeName
	}
	for _, s := range m.stores {
		if s.ID == m.storeID {
			return s.Name
		}
	}
	return ""
}

// staleStore reports whether an async load's result belongs to a store other
// than the one now selected — i.e. it was dispatched before a store switch and
// landed late. Such results must be dropped so a previous store's tuples,
// changes, model or assertions don't overwrite the current store's view. A
// blank tag (msg.storeID == "") is treated as current for backward-compatible
// tests that construct messages without a store id.
func staleStore(msgStore, current string) bool {
	return msgStore != "" && msgStore != current
}

// staleModel reports whether an async load's result belongs to a model other
// than the one now active — e.g. a query or assertion run was in flight when
// the user switched models. Same zero-value bypass as staleStore: a blank
// msgModel is treated as current, for tests/messages that don't set one.
func staleModel(msgModel, current string) bool {
	return msgModel != "" && msgModel != current
}

// staleModelKnown is like staleModel but additionally treats an unknown
// current model (current == "", nothing active yet) as "can't tell, don't
// reject" rather than "stale". It's used to compare a response against
// m.modelID (the actively selected model) in handlers whose primary
// comparator is a different, section-local model id (e.g. assertModelID)
// that may legitimately lag m.modelID until that section reloads — a bare
// staleModel(msg.modelID, m.modelID) would wrongly flag every response as
// stale before any model has ever been selected.
func staleModelKnown(msgModel, current string) bool {
	return current != "" && msgModel != "" && msgModel != current
}

// staleGen reports whether an async load's result was superseded by a newer
// request of the same kind against the same store (e.g. the user reselected a
// model, or reran a query, before the previous one finished). A zero msgGen is
// treated as current for backward-compatible tests/messages that don't set a
// generation, so only requests built by the gen-aware command constructors
// actually get checked.
func staleGen(msgGen, current int) bool {
	return msgGen != 0 && msgGen != current
}

func (m Model) mutationOrigin(storeID, modelID string, gen ...int) mutationOrigin {
	mutationGen := 0
	if len(gen) > 0 {
		mutationGen = gen[0]
	}
	return mutationOrigin{
		connGen: m.connGen,
		gen:     mutationGen,
		profile: m.profile,
		storeID: storeID,
		modelID: modelID,
	}
}

// staleMutation keeps zero-valued origins accepted for compatibility with
// direct unit-test messages, while every real command carries full identity.
func (m Model) staleMutation(origin mutationOrigin, currentGen int) bool {
	if origin.connGen != 0 && origin.connGen != m.connGen {
		return true
	}
	if origin.gen != 0 && origin.gen != currentGen {
		return true
	}
	if origin.profile != "" && origin.profile != m.profile {
		return true
	}
	if staleStore(origin.storeID, m.storeID) {
		return true
	}
	return staleModelKnown(origin.modelID, m.modelID)
}

// beginLoad registers one more in-flight async load, keeping the spinner
// (loading) on until every one of them has completed. Callers that dispatch
// several concurrent commands as one logical action (e.g. selecting a store
// fires four loads at once) must call this once per command — a single call
// covering all of them would let the spinner stop the moment the first of
// several sibling responses lands, while the others are still in flight.
func (m *Model) beginLoad() {
	m.pendingLoads++
	m.loading = true
}

// endLoad marks one in-flight async load as finished. It must be called
// unconditionally for every completion message — including ones dropped as
// stale — since the request genuinely finished and its slot must be freed;
// leaving it pending would strand the spinner on.
func (m *Model) endLoad() {
	if m.pendingLoads > 0 {
		m.pendingLoads--
	}
	m.loading = m.pendingLoads > 0
}

// activeAPIURL is the API URL the current connection targets: a one-shot flag
// override if present, otherwise the active profile's saved URL. Used to name
// the unreachable server in the error empty-state.
func (m Model) activeAPIURL() string {
	if m.apiURL != "" {
		return m.apiURL
	}
	return config.DefaultAPIURL
}

func boolWord(b bool) string {
	if b {
		return "allowed"
	}
	return "denied"
}

func short(id string) string {
	if len(id) > 14 {
		return id[:10] + "…"
	}
	return id
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return safeMultiline(err.Error())
}

func safeText(s string) string {
	return style.SanitizeTerminal(s)
}

func safeMultiline(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = safeText(lines[i])
	}
	return strings.Join(lines, "\n")
}

// isConnErr reports whether err looks like a network-level failure (refused
// connection, DNS lookup failure, timeout) rather than a normal API error
// response (validation, auth, not-found, …), which the SDK returns as typed
// *openfga.ErrorResponse variants carrying an actual HTTP status. There is no
// existing error-classification helper in the codebase to key off, so this is
// a minimal heuristic: first the idiomatic net.Error check (matches the
// *url.Error the standard http.Client wraps transport failures in), then a
// substring fallback for anything that slips through unwrapped.
func isConnErr(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"connection refused", "no such host", "network is unreachable",
		"i/o timeout", "connection reset", "broken pipe",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
