package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// Options configures `ofga mappings`.
type Options struct {
	// Path opens this file directly, skipping the start screen ("" shows it).
	Path string
	// Dir is where the start screen looks for mapping files.
	Dir string
	// RecentFile is mappings-recent.json; "" keeps no recent list.
	RecentFile string
	// Store names the configured store for the model dialog, e.g.
	// "profile prod, store 01ARZ3NDEKTSV4RRFFQ69G5FAV"; "" when none is set,
	// and then the store option is not offered.
	Store string
	// LoadStoreModel reads the store's model: --model-id or the profile's
	// model when set, else the latest. Nil when Store is "".
	LoadStoreModel func(ctx context.Context) (*openfga.AuthorizationModel, error)
	// Version is shown in the sidebar's brand line.
	Version string
	// Apply is the store the Apply pane writes to; nil when no store is set,
	// or when one is set but cannot be used.
	Apply *ApplyTarget
	// ApplyErr is why the store that is set cannot be used, such as a
	// malformed store ID; "" when Apply is set or no store is.
	ApplyErr string
}

// ApplyTarget is the active profile's store.
type ApplyTarget struct {
	StoreID string
	Profile string
	// Name looks the store's name up. Best effort: "" when it cannot.
	Name   func(ctx context.Context) string
	Reader mapping.TupleReader
	Writer mapping.TupleWriter
	// Hints explains an apply failure the way the CLI does (clierr.Friendly,
	// the condition-conflict hint). It may return nothing.
	Hints func(err error) []string
	// Resume is the CLI command that resumes applying path's events from event
	// from. eventsPath is "" when the events did not come from one file; the
	// command then says "EVENTS_FILE". path is the workspace's file, which
	// Options.Path does not know when the start screen opened it.
	Resume func(path, eventsPath string, from int) string
}

// opened is a file the start screen resolved: loaded, fresh, or broken.
type opened struct {
	path    string
	file    *mapping.File     // loaded (possibly a draft or read-only)
	doc     *mapping.Document // fresh: the document to start from
	auth0   bool              // fresh from Auth0 recipes
	loadErr []mapping.Finding // the YAML did not parse
}

// openPath loads path for the workspace: a draft load, then the samples
// recovered from its tests. A missing file is an error (NewApp opens one
// fresh before it gets here); bad YAML is not.
func openPath(ctx context.Context, path string) (opened, error) {
	// A pipe or device would block the read, and a folder is not a mapping.
	if info, err := os.Stat(path); err != nil {
		return opened{}, err
	} else if !info.Mode().IsRegular() {
		return opened{}, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := mapping.LoadDraft(path)
	var le *mapping.LoadError
	if errors.As(err, &le) {
		fs := mapping.FindingsFrom(le.Diagnostics)
		if len(fs) == 0 {
			// loadErr must be non-nil for the workspace to know the file is broken.
			fs = []mapping.Finding{{Rule: -1, Message: le.Error()}}
		}
		return opened{path: path, loadErr: fs}, nil
	}
	if err != nil {
		return opened{}, err
	}
	mapping.SamplesFromTests(ctx, f.Doc)
	return opened{path: path, file: f}, nil
}

// pane is a sidebar entry.
type pane int

const (
	paneRules pane = iota
	paneCheck
	paneTests
	paneApply
	paneCount
)

const overlayPane pane = -1 // paneMsg target for the overlay stack

// checkDelay is how long typing must pause before Check, the Tests badge and
// the exact dirty flag catch up with it.
const checkDelay = 250 * time.Millisecond

// workspace is one mapping file open for editing.
type workspace struct {
	ctx  context.Context
	opts Options
	path string

	// file is what was loaded or last saved; nil while the file does not exist
	// yet (fresh) or did not load (loadErr).
	file    *mapping.File
	fresh   bool              // the first save creates the file
	auth0   bool              // built from Auth0 recipes: the first save may write model.fga
	loadErr []mapping.Finding // non-nil: the YAML did not parse; nothing is editable

	// doc is the working copy. doc.Tests holds the file's own tests (and any the
	// user edited); generated tests come from samples at save time, so they are
	// refreshed on every save in this session.
	doc  mapping.Document
	sess *Session

	stacks [paneCount]*Stack
	// paneEpoch and overlayEpoch number the generations of the pane stacks
	// (advanced by build) and of the overlay (advanced by openOverlay).
	paneEpoch    int
	overlayEpoch int
	check        *checkView // root of stacks[paneCheck], fed by runCheck
	preview      *Preview
	// apply is the root of stacks[paneApply]. It outlives a rebuild: the events
	// pasted and loaded are the session's, not the file's.
	apply      *applyView
	storeName  string // the Apply store's name, once looked up
	storeAsked bool   // the name lookup has started

	// The Apply pane's background work: one plan or apply at a time.
	applyGen    int                // numbers the runs, so a late result is told from the current
	applyCancel context.CancelFunc // stops the run in flight; nil when there is none
	applying    bool               // the run in flight is an apply
	applyFeed   <-chan tea.Msg     // the run in flight's progress
	pending     *applyPlan         // what the plan in flight is made from; nil when none is
	confirmNext bool               // the plan in flight goes on to the question (a or r asked for it)
	now         func() time.Time   // the clock plans are dated by

	// overlay is a modal over everything: the model dialog, the quit prompt,
	// the save conflict and its confirms. onOverlay decides what its result means.
	overlay   *Stack
	onOverlay func(result any) tea.Cmd

	active  pane
	focus   shell.Focus
	help    bool
	helpOff int // how far the help is scrolled

	dirty       bool
	closing     bool // rootClosed is running an overlay's onClose and resumes the shown pane after it, so modelChanged leaves that to it
	gen         int  // debounce generation
	findings    []mapping.Finding
	tests       TestResults // badge run
	needSamples bool        // the last badge run did not compile: samples are owed
	testsGen    int         // numbers badge runs, so a late one is told from the current
	// testsCancel stops the badge run in flight when an edit makes it stale.
	testsCancel context.CancelFunc
	model       modelState
	modelDue    bool // the model dialog is still to open: a load error postponed it

	sh     *shell.Shell
	toasts toast.Model
	spin   spinner.Model
	busy   int // background jobs in flight; the spinner shows while > 0
	width  int
	height int
}

// paneMsg carries a message produced by a pane's command back to that pane, so
// a list filter's matches land on the list that asked even after the user
// switched panes or an overlay opened.
//
// epoch is the generation of the stack the command was issued for (see
// workspace.epochOf): a stack that was rebuilt, or an overlay another replaced,
// has a newer one, and the message is dropped instead of reaching its successor.
type paneMsg struct {
	p     pane
	epoch int
	msg   tea.Msg
}

// debounceMsg is the tick an edit schedules; only the one carrying the latest
// generation does anything.
type debounceMsg struct{ gen int }

func newWorkspace(ctx context.Context, opts Options, o opened) *workspace {
	w := &workspace{
		ctx: ctx, opts: opts, path: o.path, file: o.file, auth0: o.auth0,
		now:    time.Now,
		focus:  shell.FocusPanel,
		sh:     shell.New(),
		toasts: toast.New(),
		spin: spinner.New(spinner.WithSpinner(spinner.Dot),
			spinner.WithStyle(lipgloss.NewStyle().Foreground(style.Primary))),
	}
	switch {
	case o.file != nil:
		w.doc = *o.file.Doc
	case o.loadErr == nil:
		// Nothing on disk yet: the edits so far exist only here.
		w.fresh, w.dirty = true, true
		if o.doc != nil {
			w.doc = *o.doc
		}
	}
	w.model.label = noModel
	w.sess = NewSession(ctx, &w.doc, nil)
	w.sess.ReadOnly = o.file != nil && !o.file.Editable
	w.build()
	if o.loadErr != nil {
		w.enterLoadError(o.loadErr)
	} else {
		w.runCheck()
	}
	return w
}

// build makes every pane's stack and the preview from w.doc.
func (w *workspace) build() {
	w.paneEpoch++
	w.stacks[paneRules] = OpenRules(w.sess)
	w.check = newCheckView(w.sess)
	w.stacks[paneCheck] = newStack(w.sess, w.check)
	w.stacks[paneTests] = OpenTests(w.sess, Focus{})
	if w.apply == nil {
		w.apply = newApplyView(w.sess, filepath.Dir(w.path))
		w.apply.setHeader(w.applyHeader())
	}
	w.stacks[paneApply] = newStack(w.sess, w.apply)
	w.preview = NewPreview(w.sess, style.SanitizeTerminal(w.path))
	w.preview.Refresh(w.focusRule())
}

func (w *workspace) Init() tea.Cmd {
	if w.loadErr != nil {
		w.modelDue = true
		return w.initStacks()
	}
	return tea.Batch(w.initStacks(), w.openModelDialog(), w.startTestsRun())
}

func (w *workspace) initStacks() tea.Cmd {
	cmds := make([]tea.Cmd, 0, paneCount)
	for p, st := range w.stacks {
		cmds = append(cmds, w.tag(pane(p), st.Init()))
	}
	return tea.Batch(cmds...)
}

// Update takes everything the program receives.
func (w *workspace) Update(msg tea.Msg) tea.Cmd {
	// Toasts expire on their own timer, whatever else is going on.
	return tea.Batch(w.toasts.Update(msg), w.update(msg))
}

func (w *workspace) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		w.width, w.height = msg.Width, msg.Height
		w.sh.SetSize(msg.Width, msg.Height)
		return nil
	case tea.KeyPressMsg:
		return w.key(msg)
	case paneMsg:
		if msg.epoch != w.epochOf(msg.p) {
			// Meant for a stack that was rebuilt or an overlay that was
			// replaced. Nothing tagged survives that: a rebuilt stack
			// re-issues its own Init, and what the workspace runs itself
			// (plans, applies, toasts, debounce) is not tagged.
			return nil
		}
		return w.route(msg.p, msg.msg)
	case storeModelMsg:
		return w.storeModelArrived(msg)
	case storeNameMsg:
		return w.storeNamed(msg)
	case planProgressMsg:
		return w.planProgress(msg)
	case planDoneMsg:
		return w.planDone(msg)
	case applyProgressMsg:
		return w.applyProgress(msg)
	case applyDoneMsg:
		return w.applyDone(msg)
	case testsRunMsg:
		return w.testsArrived(msg)
	case editorDoneMsg:
		return w.editorDone(msg)
	case debounceMsg:
		return w.debounced(msg.gen)
	case spinner.TickMsg:
		// Not re-arming the tick is what stops the spinner once nothing runs.
		if w.busy == 0 {
			return nil
		}
		var cmd tea.Cmd
		w.spin, cmd = w.spin.Update(msg)
		return cmd
	case tea.PasteMsg:
		// A paste is typing: it goes where the keys go, and nowhere from the
		// sidebar, or from behind the help.
		if w.help || w.overlay == nil && w.focus != shell.FocusPanel {
			return nil
		}
	}
	if w.overlay != nil {
		return w.route(overlayPane, msg)
	}
	return w.route(w.active, msg)
}

func (w *workspace) stackOf(p pane) *Stack {
	if p == overlayPane {
		return w.overlay
	}
	return w.stacks[p]
}

// route sends msg to pane p's stack and settles the outcome.
func (w *workspace) route(p pane, msg tea.Msg) tea.Cmd {
	st := w.stackOf(p)
	if st == nil {
		return nil // the overlay it was for has closed
	}
	cmd, out := st.Update(msg)
	// Tagged first: settling may replace the overlay, and cmd belongs to the
	// one that issued it.
	issued := w.tag(p, cmd)
	settled := w.settle(p, out)
	w.follow()
	return tea.Batch(issued, settled)
}

// settle applies an Outcome from pane p: toasts, the dirty flag and the
// debounce on Changed, and the root's close (rootClosed) on Closed.
func (w *workspace) settle(p pane, out Outcome) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range out.Toasts {
		cmds = append(cmds, w.toast(t.Text, t.Err))
	}
	if out.Changed {
		cmds = append(cmds, w.changed())
	}
	for _, r := range out.Requests {
		switch r := r.(type) {
		case RulesRequest:
			if r == RequestModel {
				cmds = append(cmds, w.openModelDialog())
			}
		case applyRequest:
			cmds = append(cmds, w.applyRequested(r))
		case planDropped:
			cmds = append(cmds, w.selectionChanged(r.why))
		}
	}
	if out.Closed {
		cmds = append(cmds, w.rootClosed(p, out.Result))
	}
	return tea.Batch(cmds...)
}

// rootClosed handles a pane root closing with result.
func (w *workspace) rootClosed(p pane, result any) tea.Cmd {
	if p == overlayPane {
		// Closed before onClose runs, so onClose can open the next overlay.
		onClose := w.onOverlay
		w.closeOverlay()
		var cmd tea.Cmd
		if onClose != nil {
			w.closing = true
			cmd = onClose(result)
			w.closing = false
		}
		if w.overlay != nil {
			return cmd // the next overlay has the screen
		}
		// The pane is on screen again, and the overlay may have changed what it
		// shows: a reload, a save, a model.
		return tea.Batch(cmd, w.switchTo(w.active))
	}
	switch r := result.(type) {
	case RulesRequest:
		switch r {
		case RequestAdd:
			return w.pushOn(paneRules, newRuleFork(w.sess, false))
		case RequestModel:
			return w.openModelDialog()
		case RequestSave:
			return w.saveKey()
		case RequestTests:
			return w.show(paneTests)
		}
	case Jump:
		return w.jump(r)
	}
	// RequestQuit, or a root that closed with nothing: the user backed out of
	// the pane, so the sidebar takes the keys.
	w.focus = shell.FocusSidebar
	return nil
}

// pushOn opens c on pane p's stack and settles what opening it posted, such as
// the fork's first question.
func (w *workspace) pushOn(p pane, c component) tea.Cmd {
	st := w.stacks[p]
	cmd := st.push(c)
	var out Outcome
	settled := st.settle(&out)
	return tea.Batch(w.tag(p, tea.Batch(cmd, settled)), w.settle(p, out))
}

// jump shows rule j.Rule in the Rules pane with the editor for j.Section open.
func (w *workspace) jump(j Jump) tea.Cmd {
	if w.loadErr != nil {
		return nil // the findings are the file's, and nothing is loaded to show
	}
	if title, question, yes := heldInput(w.stacks[paneRules]); title != "" {
		return w.openOverlay(newStack(w.sess, newConfirm(w.sess, title, question, yes)), func(result any) tea.Cmd {
			if result != true {
				return nil
			}
			return w.goTo(j)
		})
	}
	return w.goTo(j)
}

// heldInput is the question to ask before dropping stacks that hold input no
// save keeps: a rule being added, or text in a paste box. An empty title is
// nothing to ask about.
func heldInput(stacks ...*Stack) (title, question, yes string) {
	for _, st := range stacks {
		if st != nil && st.holdsNewRule() {
			return "Leave new rule", "Drop the rule you were adding?", "drop"
		}
	}
	for _, st := range stacks {
		if st != nil && st.holdsPaste() {
			return "Discard paste", "Discard the pasted text?", "discard"
		}
	}
	return "", "", ""
}

// goTo is jump past its questions.
func (w *workspace) goTo(j Jump) tea.Cmd {
	cmd := w.show(paneRules)
	if w.sess.rule(j.Rule) == nil {
		return cmd
	}
	ShowRule(w.stacks[paneRules], j.Rule)
	// A read-only rule opens no editor: its spoke refuses ↵ for the same reason.
	if w.sess.ReadOnly {
		w.follow()
		return cmd
	}
	at := Focus{Rule: j.Rule}
	var next component
	switch j.Section {
	case "trigger":
		next = newTriggerForm(w.sess, at)
	case "action":
		next = newActionPick(w.sess, at)
	case "variables":
		next = newVarList(w.sess, at)
	case "iterator":
		next = newIterHub(w.sess, at)
	case "filters":
		next = newFilterList(w.sess, at)
	case "tuple":
		next = newTupleList(w.sess, at)
	}
	if next != nil {
		cmd = tea.Batch(cmd, w.pushOn(paneRules, next))
	}
	w.follow()
	return cmd
}

// show switches the active pane, resumes its stack so it re-reads the
// document, and focuses the panel.
func (w *workspace) show(p pane) tea.Cmd {
	w.focus = shell.FocusPanel
	return w.switchTo(p)
}

// switchTo is show without taking focus from the sidebar.
func (w *workspace) switchTo(p pane) tea.Cmd {
	w.active = p
	var shown tea.Cmd
	if p == paneApply {
		shown = w.applyShown()
	}
	cmd, out := w.stacks[p].Resume()
	issued := w.tag(p, cmd)
	settled := w.settle(p, out)
	w.follow()
	return tea.Batch(issued, settled, shown)
}

// openOverlay puts a modal on top; onClose receives its result. It replaces
// any overlay already open, whose onClose never runs.
func (w *workspace) openOverlay(st *Stack, onClose func(result any) tea.Cmd) tea.Cmd {
	w.overlay, w.onOverlay = st, onClose
	w.overlayEpoch++
	return w.tag(overlayPane, st.Init())
}

func (w *workspace) closeOverlay() { w.overlay, w.onOverlay = nil, nil }

// changed is what every edit leads to: dirty, preview refresh, debounce.
func (w *workspace) changed() tea.Cmd {
	w.dirty = true
	w.preview.Refresh(w.focusRule())
	w.gen++
	gen := w.gen
	return tea.Tick(checkDelay, func(time.Time) tea.Msg { return debounceMsg{gen: gen} })
}

// debounced catches up with the edits once typing pauses. Only the latest
// edit's tick does it: the earlier ones would check the same document.
func (w *workspace) debounced(gen int) tea.Cmd {
	if gen != w.gen {
		return nil
	}
	// One render serves all three: it evaluates every sample, each evaluation
	// capped at a second, on the goroutine that draws the screen, so Check
	// and the badge take its bytes instead of asking for them again.
	r := w.render()
	w.checkRendered(r)
	cmd := w.startTestsRunFrom(r.src)
	w.dirty = r.unsaved()
	return cmd
}

// unsaved is the exact dirty flag: whether a save would write anything.
func (w *workspace) unsaved() bool { return w.render().unsaved() }

// rendered is the file as the screen has it, written the way a save would
// write it.
type rendered struct {
	// file is what Check reads: Check judges bytes, not the document.
	file *mapping.File
	// changed is whether a save would write: the edits mean something the file
	// on disk does not, or there is no file on disk yet.
	changed bool
	err     error
	// src is the bytes a save would write, which the badge tests; nil where
	// there are none (nothing loaded, or a file that cannot be edited).
	src []byte
}

// unsaved is the exact dirty flag. A render that fails counts: a save would
// try, and say why it cannot.
func (r rendered) unsaved() bool { return r.changed || r.err != nil }

func (w *workspace) render() rendered {
	var (
		src     []byte
		changed bool
		err     error
	)
	switch {
	case w.loadErr != nil:
		return rendered{} // nothing loaded, so nothing to write or check
	case w.fresh:
		src, err = mapping.Marshal(w.savedDoc())
		changed = true
	case !w.file.Editable:
		// Nothing can have been edited, and Render refuses such a file.
		return rendered{file: w.file}
	default:
		src, changed, err = w.file.Render(w.savedDoc())
	}
	if err != nil {
		return rendered{changed: changed, err: err}
	}
	f, err := mapping.LoadDraftBytes(w.path, src)
	return rendered{file: f, changed: changed, err: err, src: src}
}

// toast shows a line; err picks the error level.
func (w *workspace) toast(text string, err bool) tea.Cmd {
	level := toast.Info
	if err {
		level = toast.Error
	}
	return w.toasts.Push(level, style.SanitizeTerminal(text))
}

// rebuild recreates every pane stack and the preview from w.doc, for a
// reload or a model change that invalidates what they hold.
func (w *workspace) rebuild() tea.Cmd {
	dropped := w.dropPlan("the file was reloaded")
	w.build()
	synced := w.syncApply()
	r := w.render()
	w.checkRendered(r)
	return tea.Batch(dropped, synced, w.initStacks(), w.startTestsRunFrom(r.src))
}

// focusRule is the rule the Rules pane is on, for the preview: what the
// topmost component editing a rule edits.
func (w *workspace) focusRule() Focus {
	items := w.stacks[paneRules].items
	for i := len(items) - 1; i >= 0; i-- {
		if f, ok := items[i].(focused); ok {
			return f.focus()
		}
	}
	return Focus{}
}

// follow moves the preview to the rule the Rules pane is on, when it moved.
func (w *workspace) follow() {
	if at := w.focusRule(); at != w.preview.at {
		w.preview.Refresh(at)
	}
}

// savedDoc is what a save writes and what Check and the dirty flag judge:
// the working document with the user's own tests first, then the tests
// generated from this session's samples (mapping.SavedTests).
func (w *workspace) savedDoc() *mapping.Document {
	d := w.doc
	d.Tests = mapping.SavedTests(w.ctx, &w.doc)
	return &d
}

// runCheck re-runs Check on what is on screen and feeds the Check pane.
func (w *workspace) runCheck() { w.checkRendered(w.render()) }

func (w *workspace) checkRendered(r rendered) {
	switch {
	case w.loadErr != nil:
		w.findings = w.loadErr
	case r.err != nil:
		w.findings = []mapping.Finding{{Rule: -1, Message: r.err.Error()}}
	default:
		w.findings = mapping.Check(r.file, w.sess.Index)
	}
	var missing []string
	if w.loadErr == nil && w.sess.Index != nil {
		missing = mapping.MissingTypes(&w.doc, w.sess.Index)
	}
	w.check.set(w.findings, w.modelName(), w.model.path, missing)
}

// epochOf is the current generation of pane p's stack, or of the overlay.
func (w *workspace) epochOf(p pane) int {
	if p == overlayPane {
		return w.overlayEpoch
	}
	return w.paneEpoch
}

// tag is tagged for the stack p has now.
func (w *workspace) tag(p pane, cmd tea.Cmd) tea.Cmd { return tagged(p, w.epochOf(p), cmd) }

// tagged wraps cmd so its messages come back as paneMsg{p, msg}.
//
// The runtime acts on some messages itself instead of handing them to Update:
// a batch, a sequence, an exec, a quit, a window title. A tagged one reaches
// Update as a paneMsg and is never acted on. tagged opens a batch and passes a
// quit through; anything else of that kind must not come from a pane's
// command. The workspace issues it itself, as the $EDITOR hand-off does.
//
// epoch is read once, when the command is issued, so the batch's inner
// commands carry it too.
func tagged(p pane, epoch int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		switch msg := cmd().(type) {
		case nil, tea.QuitMsg:
			return msg
		case tea.BatchMsg:
			// The runtime runs a batch's commands itself, so each is tagged
			// rather than the batch.
			out := make(tea.BatchMsg, len(msg))
			for i, c := range msg {
				out[i] = tagged(p, epoch, c)
			}
			return out
		default:
			return paneMsg{p: p, epoch: epoch, msg: msg}
		}
	}
}
