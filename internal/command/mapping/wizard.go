package mapping

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/modeltest"
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
)

// sideBySideMin is the width at which the preview pane moves beside the editor
// instead of under it.
const sideBySideMin = 100

// modelLoader fetches the authorization model to index. Injected so tests need
// no server.
type modelLoader func(ctx context.Context) (*openfga.AuthorizationModel, error)

// modelLoadedMsg carries the result of a background model fetch.
type modelLoadedMsg struct {
	model *openfga.AuthorizationModel
	err   error
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
	tupleIdx  int //nolint:unused // wired up by the rule/tuple editor screens in task 12+
	varIdx    int //nolint:unused // wired up by the variable editor screen in task 12+
	filterIdx int //nolint:unused // wired up by the filter editor screen in task 12+
	inIter    bool

	// Live state recomputed by refresh.
	preview  mapping.Preview
	problems []mapping.Problem

	// Widgets. Later tasks add theirs; these three exist from the start.
	sourcePick *picker.Picker
	modelPath  *field.Form
	rules      *uilist.List
	sections   *picker.Picker
	trigger    *field.Form
	confirmMsg string

	// loadCmd is the pending model fetch, kept on the model so tests can drive
	// it without a bubbletea runtime.
	loadCmd tea.Cmd

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
	}
	m.sourcePick = picker.New(m.sourceItems())
	// "Skip" is the recommended default and the last row, so start there.
	m.sourcePick.SetCursor(m.sourcePick.Len() - 1)
	m.modelPath = field.NewForm(field.New("Model file", defaultModelFile()))
	m.trigger = field.NewForm(
		field.New("Rule name", "organization.member.added"),
		field.New("When (expression)", `input.type == "organization.member.added"`),
	)
	m.sections = picker.New(nil)
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

	case modelLoadedMsg:
		m.loadCmd = nil
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

	switch m.top() {
	case screenWelcome:
		switch k.String() {
		case "enter":
			m.push(screenModelSource)
		case "esc", "ctrl+c":
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
	case screenTrigger:
		return m.keyTrigger(k)
	case screenConfirmDelete:
		return m.keyConfirmDelete(k)
	}
	return nil
}

func (m *wizardModel) keyModelSource(k tea.KeyPressMsg) tea.Cmd {
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
			m.loadCmd = m.fetchModel()
			return m.loadCmd
		case "file":
			m.push(screenModelFile)
		default:
			m.push(screenRules)
		}
	}
	return nil
}

func (m *wizardModel) fetchModel() tea.Cmd {
	return func() tea.Msg {
		model, err := m.load(m.ctx)
		return modelLoadedMsg{model: model, err: err}
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
//
//nolint:unused // called by the tuple editor screen added in task 12+
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
