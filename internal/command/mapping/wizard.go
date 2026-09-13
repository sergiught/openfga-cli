package mapping

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// screen identifies one view. The wizard is hub-and-spoke rather than linear, so
// navigation is a stack of these rather than an incrementing step.
type screen int

const (
	screenWelcome screen = iota
	screenModelSource
	screenModelFile
	screenRules
	screenRule
	screenTrigger
	screenEventPick
	screenEventPaste
	screenEventFile
	screenTuples
	screenTuple
	screenAction
	screenVariables
	screenVariable
	screenIterator
	screenFilters
	screenFilter
	screenPathPick
	screenConfirmSave
	screenConfirmDelete
	screenPayloadKind
)

// sideBySideMin is the width at which the preview pane moves beside the editor
// instead of under it.
const sideBySideMin = 100

// modelLoader fetches the authorization model to index. Injected so tests need
// no server.
type modelLoader func(ctx context.Context) (*openfga.AuthorizationModel, error)

// modelLoadedMsg carries the result of a background model fetch. gen identifies
// the fetch that produced it, so a result the user has already walked away from
// can be told apart from the one being waited on.
type modelLoadedMsg struct {
	model *openfga.AuthorizationModel
	err   error
	gen   int
}

type wizardModel struct {
	ctx     context.Context
	path    string
	profile string
	load    modelLoader

	stack []screen
	doc   mapping.Document
	index *mapping.ModelIndex

	// Cursors into doc. ruleIdx addresses doc.Rules; tupleIdx, varIdx and
	// filterIdx address the open rule's slices. inIter selects which tuple slice
	// the tuple editor is bound to.
	ruleIdx   int
	tupleIdx  int
	varIdx    int
	filterIdx int
	inIter    bool

	// Live state recomputed by refresh.
	preview  mapping.Preview
	problems []mapping.Problem

	// Widgets. Later tasks add theirs; these three exist from the start.
	sourcePick *picker.Picker
	modelPath  *field.Form
	rules      *uilist.List
	kindPick   *picker.Picker
	sections   *picker.Picker
	trigger    *field.Form
	events     *uilist.List
	paste      textarea.Model
	eventPath  *field.Form
	tupleList  *uilist.List
	tupleForm  *field.Form
	fieldPick  *picker.Picker
	pickField  tupleField
	ctxKeys    []string
	confirmMsg string
	paths      *uilist.List
	pathTarget *field.Form
	pathIdx    int
	pathTmpl   bool
	actionPick *picker.Picker
	varList    *uilist.List
	varForm    *field.Form
	iterForm   *field.Form
	filterList *uilist.List
	filterForm *field.Form

	// loadCmd is the pending model fetch, kept on the model so tests can drive
	// it without a bubbletea runtime.
	loadCmd tea.Cmd
	// loading is true between dispatching a fetch and its result arriving. The
	// server can be slow or down, and the SDK retries before giving up, so this
	// is the only thing standing between the user and a frozen screen.
	loading    bool
	loadCancel context.CancelFunc
	loadGen    int
	spin       spinner.Model

	errMsg    string
	noteMsg   string
	width     int
	height    int
	cancelled bool
	done      bool
	result    *wizardResult
}

func newWizard(ctx context.Context, path, profile string, load modelLoader) *wizardModel {
	m := &wizardModel{
		ctx:     ctx,
		path:    path,
		profile: profile,
		load:    load,
		stack:   []screen{screenWelcome},
		rules:   uilist.New(),
		spin: spinner.New(
			spinner.WithSpinner(spinner.Dot),
			spinner.WithStyle(lipgloss.NewStyle().Foreground(style.Primary)),
		),
	}
	m.sourcePick = picker.New(m.sourceItems())
	// "Skip" is the recommended default and the last row, so start there.
	m.sourcePick.SetCursor(m.sourcePick.Len() - 1)
	m.modelPath = field.NewForm(field.New("Model file", defaultModelFile()))
	m.trigger = field.NewForm(
		field.New("Rule name", "organization.member.added"),
		field.New("When (expression)", `input.type == "organization.member.added"`),
	)
	m.kindPick = picker.New([]picker.Item{
		{Title: "Auth0 events", Desc: "21 event types, each with a worked mapping", Value: "auth0"},
		{Title: "Another JSON payload", Desc: "paste or load your own event", Value: "other"},
	})
	m.sections = picker.New(nil)
	m.events = uilist.New()
	m.events.SetFilterPlaceholder("filter events")
	// Compact: 21 catalog entries plus the two escapes do not fit on one page
	// at title+description height, which would hide the escapes below the
	// fold. Filter still searches type, group and summary via each item's
	// Filter field even though the description itself is not drawn.
	m.events.SetCompact(true)
	m.paste = textarea.New()
	m.paste.Placeholder = `{"type": "...", "data": {"object": {}}}`
	m.eventPath = field.NewForm(field.New("Event file", "event.json"))
	m.tupleList = uilist.New()
	m.tupleForm = newTupleForm()
	m.paths = uilist.New()
	m.paths.SetFilterPlaceholder("filter paths")
	m.actionPick = picker.New([]picker.Item{
		{Title: "Per tuple", Desc: "each tuple names its own action", Value: ""},
		{Title: "write", Desc: "every tuple in this rule is written", Value: "write"},
		{Title: "delete", Desc: "every tuple in this rule is deleted", Value: "delete"},
	})
	m.varList = uilist.New()
	m.varForm = field.NewForm(
		field.New("Name", "org").WithValidate(vIdent),
		field.New("Expression", "input.data.object.organization.id"),
	)
	m.iterForm = field.NewForm(
		field.New("Source (expression)", "input.data.object.identities"),
		field.New("As", "identity").WithValidate(vIdent),
	)
	m.filterList = uilist.New()
	m.filterForm = field.NewForm(
		field.New("User (optional)", "user:{{ fga_escape(input.data.object.user.user_id) }}").WithValidate(vUserRef),
		field.New("Relation (optional)", "member").WithValidate(vTemplate),
		field.New("Object (optional)", "organization:{{ input.data.object.organization.id }}").WithValidate(vFilterObject),
		field.New("Action", "delete").WithValidate(vFilterAction),
	)
	m.refresh()
	return m
}

// sourceItems builds the model-source choices. "Connected store" only appears
// when a profile is active — offering a server fetch with nothing configured
// would fail in a way the user cannot act on.
func (m *wizardModel) sourceItems() []picker.Item {
	var items []picker.Item
	if m.profile != "" && m.load != nil {
		items = append(items, picker.Item{
			Title: fmt.Sprintf("Connected store (profile %s)", m.profile),
			Desc:  "read the latest authorization model from the server",
			Value: "server",
		})
	}
	return append(items,
		picker.Item{Title: "Model file", Desc: "load a .fga or .json model from disk", Value: "file"},
		picker.Item{Title: "Skip", Desc: "no model: pickers accept free text", Value: "skip"},
	)
}

// defaultModelFile prefills the model path when the conventional file is right
// here, which it usually is.
func defaultModelFile() string {
	for _, p := range []string{"model.fga", "model.json"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "model.fga"
}

func (m *wizardModel) Init() tea.Cmd { return nil }

func (m *wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.applySize()
		return m, nil

	case spinner.TickMsg:
		if !m.loading {
			// Nothing to animate — let the tick loop end so the UI can idle.
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case modelLoadedMsg:
		if msg.gen != m.loadGen {
			// A fetch the user cancelled, finally reporting back.
			return m, nil
		}
		m.loadCmd = nil
		m.loading = false
		m.loadCancel = nil
		if msg.err != nil {
			// A missing model is a degraded mode, not a failure: the pickers fall
			// back to free text and the mapping is just as valid.
			m.errMsg = fmt.Sprintf("could not read the model: %v", msg.err)
			m.noteMsg = "That's OK — pickers will accept free text."
		} else {
			m.index = mapping.IndexModel(msg.model)
		}
		m.push(screenRules)
		return m, nil

	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *wizardModel) key(k tea.KeyPressMsg) tea.Cmd {
	// Every keystroke clears the last transient message; a stale error next to a
	// fresh screen is worse than none.
	m.errMsg = ""

	// ctrl+c gets out from anywhere, ahead of the per-screen routing. bubbletea
	// delivers it as an ordinary key, so a screen that does not handle it traps
	// the user — and most of these screens are text fields, where esc is the only
	// other way out and it means "done", not "quit".
	if k.String() == "ctrl+c" {
		m.cancelled = true
		return tea.Quit
	}

	switch m.top() {
	case screenWelcome:
		switch k.String() {
		case "enter":
			m.push(screenModelSource)
		case "esc":
			m.cancelled = true
			return tea.Quit
		}
	case screenModelSource:
		return m.keyModelSource(k)
	case screenModelFile:
		return m.keyModelFile(k)
	case screenRules:
		return m.keyRules(k)
	case screenRule:
		return m.keyRule(k)
	case screenPayloadKind:
		return m.keyPayloadKind(k)
	case screenTrigger:
		return m.keyTrigger(k)
	case screenEventPick:
		return m.keyEventPick(k)
	case screenEventPaste:
		return m.keyEventPaste(k)
	case screenEventFile:
		return m.keyEventFile(k)
	case screenTuples:
		return m.keyTuples(k)
	case screenTuple:
		return m.keyTuple(k)
	case screenAction:
		return m.keyAction(k)
	case screenVariables:
		return m.keyVariables(k)
	case screenVariable:
		return m.keyVariable(k)
	case screenIterator:
		return m.keyIterator(k)
	case screenFilters:
		return m.keyFilters(k)
	case screenFilter:
		return m.keyFilter(k)
	case screenPathPick:
		return m.keyPathPick(k)
	case screenConfirmSave:
		return m.keyConfirmSave(k)
	case screenConfirmDelete:
		return m.keyConfirmDelete(k)
	}
	return nil
}

func (m *wizardModel) keyModelSource(k tea.KeyPressMsg) tea.Cmd {
	// A fetch in flight owns the screen. Moving the cursor or selecting again
	// would either dispatch a second fetch over the top of the first or land the
	// user on a row that is not the one being loaded; esc is the way out.
	if m.loading {
		if k.String() == "esc" {
			m.cancelLoad()
		}
		return nil
	}
	switch k.String() {
	case "up", "k":
		m.sourcePick.Move(-1)
	case "down", "j":
		m.sourcePick.Move(1)
	case "esc":
		m.pop()
	case "enter", " ":
		switch m.sourcePick.Selected().Value {
		case "server":
			return m.startLoad()
		case "file":
			m.push(screenModelFile)
		default:
			m.push(screenRules)
		}
	}
	return nil
}

// startLoad dispatches a model fetch and puts the screen into its loading
// state. The spinner's tick loop starts with it and stops itself once loading
// ends, so an idle wizard is not redrawn forever.
func (m *wizardModel) startLoad() tea.Cmd {
	m.noteMsg = ""
	m.loading = true
	m.loadGen++
	ctx, cancel := context.WithCancel(m.ctx)
	m.loadCancel = cancel
	m.loadCmd = m.fetchModel(ctx, m.loadGen)
	return tea.Batch(m.spin.Tick, m.loadCmd)
}

// cancelLoad abandons the fetch in flight. The generation bump is what makes it
// an abandonment rather than a wait: the result is still on its way, and
// without it a user who cancels and immediately retries would be shown the
// first attempt's answer.
func (m *wizardModel) cancelLoad() {
	if m.loadCancel != nil {
		m.loadCancel()
		m.loadCancel = nil
	}
	m.loading = false
	m.loadCmd = nil
	m.loadGen++
	m.noteMsg = "Cancelled — pick another source."
}

func (m *wizardModel) fetchModel(ctx context.Context, gen int) tea.Cmd {
	return func() tea.Msg {
		model, err := m.load(ctx)
		return modelLoadedMsg{model: model, err: err, gen: gen}
	}
}

func (m *wizardModel) keyModelFile(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.pop()
		return nil
	case "enter":
		path := strings.TrimSpace(m.modelPath.Values()[0])
		if path == "" {
			m.errMsg = "enter a path, or go back and skip the model"
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			m.errMsg = fmt.Sprintf("could not read %s: %v", path, err)
			return nil
		}
		loaded, err := modeltest.LoadModelBytes(raw)
		if err != nil {
			m.errMsg = fmt.Sprintf("could not parse %s: %v", path, err)
			return nil
		}
		m.index = mapping.IndexModel(loaded.SDK)
		m.push(screenRules)
		return nil
	}
	return m.modelPath.Update(k)
}

// --- navigation ---

func (m *wizardModel) push(s screen) {
	m.stack = append(m.stack, s)
	m.applySize()
}

// pop returns to the previous screen, never past the root.
func (m *wizardModel) pop() {
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
	}
	m.applySize()
}

func (m *wizardModel) top() screen { return m.stack[len(m.stack)-1] }

// --- document access ---

// rule returns the rule being edited, or nil when the cursor is out of range —
// which happens transiently after a delete.
func (m *wizardModel) rule() *mapping.Rule {
	if m.ruleIdx < 0 || m.ruleIdx >= len(m.doc.Rules) {
		return nil
	}
	return &m.doc.Rules[m.ruleIdx]
}

// tuples returns the slice the tuple editor is bound to: a rule's own tuples,
// or its iterator's. Returns nil when there is no rule to edit.
func (m *wizardModel) tuples() *[]mapping.Tuple {
	r := m.rule()
	if r == nil {
		return nil
	}
	if m.inIter {
		if r.Iterator == nil {
			return nil
		}
		return &r.Iterator.Tuples
	}
	return &r.Tuples
}

// tuplesOrEmpty wraps tuples() for the render path, which must never nil-deref.
func (m *wizardModel) tuplesOrEmpty() *[]mapping.Tuple {
	if ts := m.tuples(); ts != nil {
		return ts
	}
	return &[]mapping.Tuple{}
}

// refresh recomputes the preview and the lint problems. Called after every
// mutation. Compiling and evaluating a wizard-sized document costs microseconds
// and is capped by mapping.EvalTimeout, so doing it synchronously keeps the
// model free of in-flight state.
func (m *wizardModel) refresh() {
	var sample map[string]any
	if r := m.rule(); r != nil && r.Sample != nil {
		sample = r.Sample.Event
	}
	m.preview = mapping.Evaluate(m.ctx, &m.doc, sample)
	m.problems = mapping.Lint(&m.doc, m.index)

	cursor := m.sections.Cursor()
	m.sections = picker.New(m.ruleSections())
	m.sections.SetCursor(cursor)
}
