package playground

import (
	"context"

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
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

const modelTemplate = "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define owner: [user]\n    define viewer: [user] or owner\n"

type section int

const (
	secStores section = iota
	secModel
	secTuples
	secChanges
	secQuery
	secAssertions
)

var sectionNames = []string{"Stores", "Model", "Tuples", "Changes", "Query", "Assertions"}

// formKind identifies a full-panel form takeover.
type formKind int

const (
	formNone formKind = iota
	formCreateStore
	formWriteTuple
)

var queryModes = []string{"check", "list-objects", "list-users"}

// Model is the task-pilot-style playground model.
type Model struct {
	app    *app.App
	client *openfga.Client
	ctx    context.Context

	width, height int
	ready         bool
	splash        bool

	storeID   string
	storeName string
	modelID   string

	section section
	sh      *shell.Shell

	spinner spinner.Model
	loading bool
	status  string

	// data + lists
	stores     []openfga.Store
	storesList *uilist.List

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

	// query
	qmode     int
	qform     *field.Form
	editing   bool // a form (query or takeover) is capturing keys
	hasResult bool
	result    queryResultMsg

	// full-panel form takeover
	formKind formKind
	form     *field.Form

	paletteOpen bool
	paletteList *uilist.List

	// DSL model editor
	editorOpen bool
	editor     textarea.Model
	editorErr  string
	modelDSL   string // DSL of the currently-loaded model, for edit pre-fill
}

func newModel(ctx context.Context, a *app.App, cl *openfga.Client, storeID string) Model {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipgloss.NewStyle().Foreground(style.Primary)))

	// A lightly-damped spring gives scrolling momentum without overshoot.
	graphSpring := harmonica.NewSpring(harmonica.FPS(graphFPS), 8.0, 1.0)

	ta := textarea.New()
	ta.ShowLineNumbers = true

	m := Model{
		app:            a,
		client:         cl,
		ctx:            ctx,
		spinner:        sp,
		sh:             shell.New(),
		splash:         true,
		section:        secStores,
		storeID:        storeID,
		graphSpring:    graphSpring,
		loading:        true,
		status:         "loading stores…",
		storesList:     uilist.New(),
		tuplesList:     uilist.New(),
		modelsList:     uilist.New(),
		changesList:    uilist.New(),
		assertionsList: uilist.New(),
		paletteList:    uilist.New(),
		editor:         ta,
	}
	m.qmode = 0
	m.populatePalette()
	if storeID == "" {
		m.status = "no store selected — pick one in Stores"
	}
	return m
}

// Init kicks off initial loads.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, loadStoresCmd(m.ctx, m.client)}
	if m.storeID != "" {
		cmds = append(cmds,
			loadModelCmd(m.ctx, m.client, m.storeID),
			loadTuplesCmd(m.ctx, m.client, m.storeID),
		)
	}
	return tea.Batch(cmds...)
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
	m := newModel(ctx, a, cl, r.StoreID)
	if r.StoreID != "" {
		m.storeName = r.StoreID
	}
	p := tea.NewProgram(m)
	_, err = p.Run()
	return err
}

// --- sizing ---

func (m *Model) resize() {
	m.sh.SetSize(m.width, m.height)
	w, h := m.sh.MainSize()
	m.storesList.SetSize(w, h)
	m.tuplesList.SetSize(w, h)
	m.modelsList.SetSize(w, h)
	m.changesList.SetSize(w, h)
	m.assertionsList.SetSize(w, h)
	m.paletteList.SetSize(w, h)
	m.graphVP = viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
	if len(m.graph.Types) > 0 {
		m.graphVP.SetContent(m.graph.RenderDiagram())
	}
	m.editor.SetWidth(w)
	m.editor.SetHeight(h - 2)
	m.rebuildQueryForm()
}

func (m *Model) contentSize() (int, int) { return m.sh.MainSize() }

// --- list population ---

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
		if i < len(m.assertResults) {
			r := m.assertResults[i]
			if r.pass {
				desc = style.IconCheck + " pass · " + exp
			} else {
				desc = style.IconCross + " FAIL · got " + boolWord(r.got)
			}
		}
		items[i] = uilist.Item{TitleText: title, DescText: desc, Filter: title, Index: i}
	}
	m.assertionsList.SetItems(items)
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
	m.qform = buildQueryForm(queryModes[m.qmode], w)
	m.qform.Init()
}

// --- store selection ---

func (m *Model) selectStore(s openfga.Store) tea.Cmd {
	m.storeID = s.ID
	m.storeName = s.Name
	m.modelID = ""
	m.graph = fga.Graph{}
	m.tuples = nil
	m.changes = nil
	m.assertions = nil
	m.assertResults = nil
	m.status = "loaded store " + s.Name
	return tea.Batch(
		loadModelCmd(m.ctx, m.client, m.storeID),
		loadTuplesCmd(m.ctx, m.client, m.storeID),
	)
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
