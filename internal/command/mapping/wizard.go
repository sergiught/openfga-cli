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
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
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
	screenModelBrowse
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
	screenIterForm
	screenFilters
	screenFilter
	screenPathPick
	screenConfirmSave
	screenConfirmDelete
	screenPayloadKind
	screenRecipe
	screenHelp
	screenCount
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

	// recipeEvent and recipe are the catalog entry shown on the recipe screen,
	// set by openRecipe. Nothing is committed to doc until useRecipe runs.
	recipeEvent auth0.Event
	recipe      auth0.Recipe

	// How far the card is scrolled, and how far it can go — the second is
	// measured by the render, since how tall a body is depends on how wide the
	// terminal is. Both reset whenever the screen changes.
	cardOff    int
	cardMaxOff int

	// Which of the preview's two long sections is on show, and how far each is
	// scrolled. They keep separate offsets so that a look at the payload and
	// back does not cost the user their place in the file. previewMaxOff and
	// previewPage belong to whichever is on show and are measured by the render,
	// for the reason cardMaxOff is: how many rows a section comes to depends on
	// how wide the pane is. payloadShown is the payload the offset was taken on,
	// so that a different one opens at its top rather than wherever the last one
	// was left.
	previewMode   previewMode
	docOff        int
	payloadOff    int
	previewMaxOff int
	previewPage   int
	payloadShown  string

	// Live state recomputed by refresh.
	preview  mapping.Preview
	problems []mapping.Problem

	// Widgets. Later tasks add theirs; these three exist from the start.
	sourcePick *picker.Picker
	modelPath  *field.Form
	// modelFiles lists modelDir for the model browser. The directory is held
	// here because the listing shows only names: it is what says which of the
	// model.fga files on this machine the rows are offering.
	modelDir   string
	modelFiles *uilist.List
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
	iterHub    *picker.Picker
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
	m.modelPath = field.NewForm(field.New("Model file", defaultModelFile()))
	m.trigger = field.NewForm(
		field.New("Rule name", "organization.member.added"),
		field.New("When (expression)", `input.type == "organization.member.added"`),
	)
	m.kindPick = picker.New([]picker.Item{
		{Title: "Auth0 events", Desc: fmt.Sprintf("%d event types, %d with a ready-made mapping", len(auth0.Catalog()), mappedCount()), Value: "auth0"},
		{Title: "Another JSON payload", Desc: "paste or load your own event", Value: "other"},
	})
	m.sections = picker.New(nil)
	m.iterHub = picker.New(nil)
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
	m.modelFiles = uilist.New()
	m.modelFiles.SetFilterPlaceholder("filter this folder")
	// Compact: the rows are bare names with nothing to describe, and a directory
	// worth browsing is one with more entries than a half-height list can show.
	m.modelFiles.SetCompact(true)
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
		// Not optional, unlike the two above it: mapper rejects a filter with no
		// object and Lint blocks the save on one. The label said otherwise while
		// the screen's own subtitle said "object needs a type".
		field.New("Object", "organization:{{ input.data.object.organization.id }}").WithValidate(vFilterObject),
		// Placeholder rather than example: an empty action really is a patch, so
		// the ghost text is the value the field already has.
		field.New("Action", "patch").WithValidate(vFilterAction),
	)
	m.refresh()
	return m
}

// sourceItems builds the model-source choices. "Connected store" only appears
// when a profile is active — offering a server fetch with nothing configured
// would fail in a way the user cannot act on. The order is the default: the
// question is asked when the user wants a model, so the row that gets them one
// leads, and "Skip" comes last.
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
		// Back to whatever asked for the model (see keyModelSource). A fetch the
		// user cancelled returned above, so this cannot pull the screen out from
		// under them.
		m.pop()
		return m, nil

	case tea.PasteMsg:
		return m, m.routePaste(msg)

	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

// routePaste forwards a terminal paste to the focused text widget on a screen
// that hosts one. Model file, Event file, and Paste JSON have only that one
// field, so forwarding is the whole job. The five multi-field rule forms
// (trigger, tuple, variable, iterator, filter) also live-commit on every
// keystroke — see keyTrigger et al. — so a forwarded paste mirrors that
// screen's own commit tail; otherwise the preview and problem list would
// freeze until the next keypress. Every other screen ignores the paste, the
// same as an unhandled key.
func (m *wizardModel) routePaste(msg tea.PasteMsg) tea.Cmd {
	switch m.top() {
	case screenModelFile:
		return m.modelPath.Update(msg)
	case screenEventFile:
		return m.eventPath.Update(msg)
	case screenEventPaste:
		var cmd tea.Cmd
		m.paste, cmd = m.paste.Update(msg)
		return cmd
	case screenTrigger:
		cmd := m.trigger.Update(msg)
		m.commitTrigger()
		return cmd
	case screenTuple:
		// Mirrors keyTuple's overlay guard: the picker owns the keyboard while
		// it is open, so a paste here must not edit the form hidden behind it.
		if m.fieldPick != nil {
			return nil
		}
		cmd := m.tupleForm.Update(msg)
		m.syncTupleVisibility()
		m.commitTuple()
		return cmd
	case screenVariable:
		cmd := m.varForm.Update(msg)
		m.commitVariable()
		return cmd
	case screenIterForm:
		cmd := m.iterForm.Update(msg)
		if strings.TrimSpace(m.iterForm.Values()[0]) != "" {
			m.commitIterator()
		}
		return cmd
	case screenFilter:
		cmd := m.filterForm.Update(msg)
		m.commitFilter()
		return cmd
	}
	return nil
}

// previewMode is which of the preview pane's two long sections it shows. They
// take turns rather than share: the file grows with every rule and the payload
// with whatever the event carries, and a pane split between two growing things
// gives neither enough rows to be read in.
type previewMode int

const (
	previewDoc previewMode = iota
	previewPayload
)

// togglePreview switches the pane between the file and the payload.
//
// Switching to a payload that is not there does nothing rather than showing an
// empty section — on those screens the header does not offer the key either, so
// pressing it is a guess, and the honest answer to a guess is no change.
func (m *wizardModel) togglePreview() {
	if m.previewMode == previewPayload {
		m.previewMode = previewDoc
		return
	}
	if _, payload := m.samplePayload(); payload != "" {
		m.previewMode = previewPayload
	}
}

// pagePreview moves the section on show by n pages.
//
// A page is one row short of the window, so that the line the eye stopped on is
// still there after the jump. Paging by the whole window gives the reader
// nothing to land on and makes them hunt for where they were.
func (m *wizardModel) pagePreview(n int) {
	off := m.previewOff()
	*off = min(max(*off+n*max(m.previewPage-1, 1), 0), m.previewMaxOff)
}

func (m *wizardModel) key(k tea.KeyPressMsg) tea.Cmd {
	// Every keystroke clears the last transient message; a stale error next to a
	// fresh screen is worse than none. The note goes with it: the two are set
	// together by a failed model load, where the note is the reassurance for that
	// error, and clearing only the error left "That's OK" standing alone on every
	// screen afterwards with nothing left to be OK about.
	m.errMsg, m.noteMsg = "", ""

	// ctrl+c gets out from anywhere, ahead of the per-screen routing. bubbletea
	// delivers it as an ordinary key, so a screen that does not handle it traps
	// the user — and most of these screens are text fields, where esc is the only
	// other way out and it means "done", not "quit".
	if k.String() == "ctrl+c" {
		m.cancelled = true
		return tea.Quit
	}

	// ? opens the help overlay for screens whose concept helpFor explains — but
	// not while a list on the current screen is taking filter input, where ? is
	// a character the user is typing rather than a request for help.
	if k.String() == "?" && !m.filtering() {
		if _, _, ok := helpFor(m.top()); ok {
			m.push(screenHelp)
			return nil
		}
	}

	// ctrl+s saves the whole file, from wherever the user happens to be. It used
	// to be handled on the two hubs only; on the form screens it reached the
	// field package, where the same chord means "submit this form" — so pressing
	// it while editing a trigger left the screen and saved nothing, which is the
	// one outcome indistinguishable from esc. Routing it here makes the key mean
	// one thing everywhere, which is what lets every hint row advertise it.
	if m.canSave() && k.String() == "ctrl+s" {
		m.push(screenConfirmSave)
		return nil
	}

	// The preview's own keys, from any screen that has a preview: ^t switches
	// the pane between the file and the payload, pgup/pgdn page whichever is on
	// show. The bare arrows belong to whichever list or form has the screen —
	// the pane is never the focused thing — so it takes keys nothing else in
	// the wizard binds. ^t is advertised in the hint row the way ^s is, being
	// global in the same way; the section's own header names both keys again,
	// beside the position readout that gives them their point.
	//
	// alt+↑↓ held this job first and never arrived: a modified arrow is encoded
	// by agreement between terminal and application, and enough of them are
	// taken by the window manager on the way that the binding could not be
	// relied on. pgup/pgdn are unmodified keys with one sequence each.
	if m.hasPane() {
		switch k.String() {
		case "ctrl+t":
			m.togglePreview()
			return nil
		case "pgup":
			m.pagePreview(-1)
			return nil
		case "pgdown":
			m.pagePreview(1)
			return nil
		}
	}

	switch m.top() {
	case screenWelcome:
		switch k.String() {
		case "enter":
			// The hub is the base of navigation: everything else is pushed on top
			// of it, and acceptPick pops back to it when a new rule is accepted.
			// The fork is opened through addRule rather than pushed here, so there
			// stays one definition of what "add a rule" means.
			m.push(screenRules)
			m.addRule()
			// The model question goes on top of the fork rather than in front of
			// it, so that every way of answering it — Skip, a file that parses, a
			// fetch that returns — lands on the event pick through the pop each
			// one already does. Asked first and answered forwards, it would need
			// three new "and then continue" branches to reach the same screen.
			m.push(screenModelSource)
		case "esc":
			m.cancelled = true
			return tea.Quit
		}
	case screenModelSource:
		return m.keyModelSource(k)
	case screenModelBrowse:
		return m.keyModelBrowse(k)
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
	case screenRecipe:
		return m.keyRecipe(k)
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
	case screenIterForm:
		return m.keyIterForm(k)
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
	case screenHelp:
		return m.keyHelp(k)
	}
	return nil
}

// keyModelSource handles the source picker. Every outcome — a fetch that
// returns, a file that parses, or Skip — leaves by popping back to whatever
// asked: the hub, or the recipe screen the user pressed `m` on. Pushing on
// instead would strand the screens underneath, and fromFork reads that stack.
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
			m.openModelBrowse()
			return nil
		default:
			m.pop()
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
		return m.loadModelFile(path)
	}
	return m.modelPath.Update(k)
}

// --- navigation ---

func (m *wizardModel) push(s screen) {
	m.stack = append(m.stack, s)
	m.cardOff = 0
	m.applySize()
}

// pop returns to the previous screen, never past the root.
func (m *wizardModel) pop() {
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
	}
	m.cardOff = 0
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

// ruleFilters is the open rule's tuple filters, or none when no rule is open.
func (m *wizardModel) ruleFilters() []mapping.TupleFilter {
	if r := m.rule(); r != nil {
		return r.Filters
	}
	return nil
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

	iterCursor := m.iterHub.Cursor()
	m.iterHub = picker.New(m.iteratorSections())
	m.iterHub.SetCursor(iterCursor)
}
