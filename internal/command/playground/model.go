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
	"github.com/sergiught/openfga-cli/internal/app"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

const modelTemplate = "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define owner: [user]\n    define viewer: [user] or owner\n"

type section int

const (
	secProfiles section = iota
	secStores
	secModel
	secTuples
	secChanges
	secQuery
	secAssertions
)

var sectionNames = []string{"Profiles", "Stores", "Model", "Tuples", "Changes", "Tuple Queries", "Assertions"}

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

var queryModes = []string{"check", "list-objects", "list-users"}

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

// histEntry is one rerunnable query-history record. Model.history holds up
// to 5, newest first (see pushHistory).
type histEntry struct {
	mode string
	vals [3]string
	ok   bool
	ms   int64
}

// Model is the task-pilot-style playground model.
type Model struct {
	app    *app.App
	client *openfga.Client
	ctx    context.Context

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

	section section
	focus   shell.Focus // FocusSidebar (tab selection) or FocusPanel (right pane)
	sh      *shell.Shell
	version string

	spinner spinner.Model
	loading bool
	status  string

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
	// confirmStore* holds the store awaiting a delete-confirmation modal.
	confirmStoreID   string
	confirmStoreName string

	tuples     []openfga.Tuple
	tuplesList *uilist.List

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

	changes     []openfga.TupleChange
	changesList *uilist.List

	assertions     []openfga.Assertion
	assertionsList *uilist.List
	assertResults  []assertResult
	assertSummary  string
	assertModelID  string
	assertErr      string // API error from the last write, shown as a modal

	// query
	qmode        int
	qform        *field.Form
	qShowContext bool // reveal the Context (JSON) + contextual-tuples fields
	editing   bool // a form (query or takeover) is capturing keys
	hasResult bool
	result    queryResultMsg
	history   []histEntry // rerunnable results, newest first, capped at 5
	flash     bool        // true for one frame right after a badge result lands

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

	// DSL model editor
	editorOpen bool
	editor     textarea.Model
	editorErr  string
	modelDSL   string // DSL of the currently-loaded model, for edit pre-fill
}

func newModel(ctx context.Context, a *app.App, cl *openfga.Client, storeID, modelID string) Model {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipgloss.NewStyle().Foreground(style.Primary)))

	// A lightly-damped spring gives scrolling momentum without overshoot.
	graphSpring := harmonica.NewSpring(harmonica.FPS(graphFPS), 8.0, 1.0)
	entranceSpring := harmonica.NewSpring(harmonica.FPS(30), 7.0, 0.9)

	// The mono theme boots settled — no motion to speak of, so the entrance
	// animation is skipped entirely.
	entering, entranceFrac := true, 1.0
	if style.Active.Name == "mono" {
		entering, entranceFrac = false, 0
	}

	ta := textarea.New()
	ta.ShowLineNumbers = true

	m := Model{
		app:            a,
		client:         cl,
		ctx:            ctx,
		spinner:        sp,
		sh:             shell.New(),
		entering:       entering,
		entranceFrac:   entranceFrac,
		entranceSpring: entranceSpring,
		section:        secStores,
		version:        a.Version,
		storeID:        storeID,
		modelID:        modelID,
		graphSpring:    graphSpring,
		loading:        true,
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
	m.qmode = 0
	m.populatePalette()
	m.populateProfiles()
	if storeID == "" {
		m.status = "no store selected — pick one in Stores"
	}
	return m
}

// Init kicks off initial loads.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, loadStoresCmd(m.ctx, m.client)}
	if m.entering {
		cmds = append(cmds, entranceTick())
	}
	if style.Active.Name != "mono" {
		cmds = append(cmds, driftTick())
	}
	if m.storeID != "" {
		cmds = append(cmds,
			m.startModelCmd(),
			loadTuplesCmd(m.ctx, m.client, m.storeID),
			loadChangesCmd(m.ctx, m.client, m.storeID),
			loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID),
		)
	}
	return tea.Batch(cmds...)
}

// startModelCmd loads the model to show on startup (or after a profile switch):
// the specific persisted model when one is configured, otherwise the store's
// latest. Loading the persisted model keeps a deliberately-chosen older model
// selected across restarts instead of snapping back to latest.
func (m Model) startModelCmd() tea.Cmd {
	if m.modelID != "" {
		return loadModelByIDCmd(m.ctx, m.client, m.storeID, m.modelID)
	}
	return loadModelCmd(m.ctx, m.client, m.storeID)
}

// Run launches the playground.
func Run(ctx context.Context, a *app.App) error {
	r, err := a.Resolve()
	if err != nil {
		return err
	}
	cl, err := a.Client()
	if err != nil {
		return err
	}
	// On first run, materialize a starter config.toml (default profile, default
	// API URL, no store/model yet) so the file exists to be updated as the user
	// picks a store and model in the TUI.
	if !a.Config.Existed() {
		if err := a.SaveConfig(); err != nil {
			a.Logger.Debug("failed to write initial config", "error", err)
		}
	}
	m := newModel(ctx, a, cl, r.StoreID, r.ModelID)
	icons.Apply(icons.Parse(a.Config.IconsMode()))
	p := tea.NewProgram(m)
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
	m.tuplesList.SetSize(lw, h)
	m.changesList.SetSize(lw, h)
	// The assertions panel reserves one line for the pass/fail tally, but only
	// once a run has produced one.
	ah := h
	if m.assertHasResults() {
		ah = h - 1
	}
	if ah < 1 {
		ah = 1
	}
	m.assertionsList.SetSize(w, ah)
	// Dialog-hosted lists (palette, model switcher) must fit the modal's
	// interior budget, not the full main pane — otherwise the dialog grows
	// taller than the terminal and its rounded corners clip off-screen.
	dw, dh := m.sh.DialogSize()
	if dh > 12 {
		dh = 12
	}
	m.paletteList.SetSize(dw, dh)
	m.modelsList.SetSize(dw, dh)
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
	m.editor.SetWidth(w)
	m.editor.SetHeight(h - 2)
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
	m.rebuildQueryForm()
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
	names := m.app.Config.ProfileNames()
	items := make([]uilist.Item, len(names))
	for i, name := range names {
		p, _ := m.app.Config.Get(name)
		desc := p.APIURL
		if name == m.app.Config.Active {
			desc = "active · " + desc
		}
		items[i] = uilist.Item{TitleText: name, DescText: desc, Filter: name, ID: name, Index: i}
	}
	m.profilesList.SetItems(items)
}

func (m *Model) populateStores() {
	items := make([]uilist.Item, len(m.stores))
	for i, s := range m.stores {
		items[i] = uilist.Item{TitleText: s.Name, DescText: s.ID, Filter: s.Name + " " + s.ID, ID: s.ID, Index: i}
	}
	m.storesList.SetItems(items)
}

func (m *Model) populateTuples() {
	items := make([]uilist.Item, len(m.tuples))
	for i, t := range m.tuples {
		items[i] = uilist.Item{
			TitleText: t.Key.User,
			DescText:  t.Key.Relation + " → " + t.Key.Object,
			Filter:    fga.FormatTuple(t.Key),
			Index:     i,
		}
	}
	m.tuplesList.SetItems(items)
}

func (m *Model) populateModels() {
	items := make([]uilist.Item, len(m.models))
	for i, md := range m.models {
		desc := "schema " + md.SchemaVersion
		if i == 0 {
			desc += "  · latest"
		}
		items[i] = uilist.Item{TitleText: md.ID, DescText: desc, Filter: md.ID, ID: md.ID, Index: i}
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
		items[i] = uilist.Item{
			TitleText: fga.FormatTuple(ch.TupleKey),
			DescText:  ch.Timestamp.Format("2006-01-02 15:04:05") + "  " + op,
			Filter:    fga.FormatTuple(ch.TupleKey),
			Index:     i,
		}
	}
	m.changesList.SetItems(items)
}

func (m *Model) populateAssertions() {
	items := make([]uilist.Item, len(m.assertions))
	for i, a := range m.assertions {
		exp := "expect allow"
		if !a.Expectation {
			exp = "expect deny"
		}
		title := a.TupleKey.User + " " + a.TupleKey.Relation + " " + a.TupleKey.Object
		desc := exp
		if i < len(m.assertResults) && m.assertResults[i].ran {
			r := m.assertResults[i]
			if r.pass {
				desc = style.Success.Render(style.IconCheck+" PASS") + style.Faint.Render(" · "+exp)
			} else {
				desc = style.Failure.Render(style.IconCross+" FAIL") + style.Faint.Render(" · got "+boolWord(r.got))
			}
		}
		items[i] = uilist.Item{TitleText: title, DescText: desc, Filter: title, Index: i}
	}
	m.assertionsList.SetItems(items)
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
	w, _ := m.contentSize()
	// Leave a small margin for the fields' own focus accent so they don't
	// touch the panel edge or get clipped.
	w -= 2
	if w < 1 {
		w = 1
	}
	m.qform = buildQueryForm(queryModes[m.qmode], w, m.qShowContext)
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
func (m *Model) setQueryError(msg string) {
	m.result = queryResultMsg{err: errors.New(msg)}
	m.hasResult = true
	m.loading = false
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
	m.persistStore()
	m.status = "loaded store " + s.Name
	return tea.Batch(
		loadModelCmd(m.ctx, m.client, m.storeID),
		loadTuplesCmd(m.ctx, m.client, m.storeID),
		loadChangesCmd(m.ctx, m.client, m.storeID),
		loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID),
	)
}

// persistStore records the selected store on the active profile and saves,
// clearing the profile's model id (a new store invalidates the old model; the
// model that loads next re-records it). No-op when the profile already reflects
// this store with no model, so an ordinary launch doesn't rewrite the file.
func (m *Model) persistStore() {
	active := m.app.Config.Active
	p, ok := m.app.Config.Get(active)
	if !ok {
		return
	}
	if p.StoreID == m.storeID && p.ModelID == "" {
		return
	}
	p.StoreID = m.storeID
	p.ModelID = ""
	m.app.Config.Set(active, p)
	m.saveConfig()
}

// persistModel records the loaded model id on the active profile and saves.
// No-op when unchanged, so reloading an already-recorded model on launch
// doesn't rewrite the file.
func (m *Model) persistModel() {
	active := m.app.Config.Active
	p, ok := m.app.Config.Get(active)
	if !ok {
		return
	}
	if p.ModelID == m.modelID {
		return
	}
	p.ModelID = m.modelID
	m.app.Config.Set(active, p)
	m.saveConfig()
}

// saveConfig persists the config, recording a non-fatal failure in the status
// line rather than interrupting the session. It skips the write when the config
// has no resolved on-disk location, so it never guesses a path.
func (m *Model) saveConfig() {
	if m.app.Config.Path() == "" {
		return
	}
	if err := m.app.SaveConfig(); err != nil {
		m.status = "could not save config: " + err.Error()
	}
}

// switchProfile makes name the active profile and reconnects to it.
func (m *Model) switchProfile(name string) tea.Cmd {
	if name == m.app.Config.Active {
		m.status = "already on profile " + name
		return nil
	}
	if err := m.app.Config.Use(name); err != nil {
		return m.toastErr("profile", err)
	}
	m.saveConfig()
	return m.reloadActive("switched to profile " + name)
}

// reloadActive repoints the client at the active profile's resolved connection,
// resets all loaded data, and reloads the stores (plus the profile's store and
// model, when set). It is used both when switching profiles and when the active
// profile's connection details are edited. On a client-build failure it keeps
// the previous client and surfaces a toast, so a broken profile can be fixed.
func (m *Model) reloadActive(status string) tea.Cmd {
	r, err := m.app.Resolve()
	if err != nil {
		return m.toastErr("profile", err)
	}
	cl, err := m.app.Client()
	if err != nil {
		m.populateProfiles()
		return m.toastErr("profile", err)
	}
	m.client = cl

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
	m.rebuildQueryForm()
	m.populateProfiles()
	m.populateStores()
	m.loading = true
	m.status = status

	cmds := []tea.Cmd{loadStoresCmd(m.ctx, m.client)}
	if m.storeID != "" {
		cmds = append(cmds,
			m.startModelCmd(),
			loadTuplesCmd(m.ctx, m.client, m.storeID),
			loadChangesCmd(m.ctx, m.client, m.storeID),
			loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID),
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
	return err.Error()
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
