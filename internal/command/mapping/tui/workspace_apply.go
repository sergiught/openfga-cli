package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// What the Apply pane says when it cannot list events, and on unsaved edits.
const (
	applyMustLoad = "Apply needs the file to load first. e opens it in your editor."
	applyNoStore  = "No store is set. Apply writes to the active profile's store: set one with " +
		"ofga profiles set store_id <id>, or start ofga mappings with --store-id."
	applyMustSave = "Apply uses the saved file. Save it first with ctrl+s."
	applyUnsaved  = "Apply uses the saved file, and you have unsaved edits. ctrl+s saves them."

	applyChangedOnDisk = "the file changed on disk: ctrl+s reloads it or saves over it"
	applyRunning       = "events are being applied: wait, or ctrl+c stops them"
	applyLocksFile     = "the file cannot change while events are applied: wait, or ctrl+c stops them"
)

// planMaxAge is how old a plan may be when a asks for it. An older one is
// made again before the question, since the store may have moved on.
const planMaxAge = 5 * time.Minute

// The messages planning and applying send back. One whose gen is not
// applyGen comes from a run since replaced: only its busy count still counts.
type (
	planDoneMsg struct {
		gen  int
		plan *mapping.Plan
		err  error
	}
	planProgressMsg struct{ gen, done, total int }
	applyDoneMsg    struct {
		gen, applied int
		err          error // nil: every event was sent
	}
	applyProgressMsg struct{ gen, done, total int }
)

// storeNameMsg is the store's name arriving: "" when it could not be looked up.
type storeNameMsg struct{ name string }

// applyBlocked is why the Apply pane shows only a message, in order of
// precedence, or "" when it lists events.
func (w *workspace) applyBlocked() string {
	switch {
	case w.loadErr != nil:
		return applyMustLoad
	case w.opts.Apply == nil && w.opts.ApplyErr != "":
		// The reason quotes the configuration, which can hold anything.
		return strings.Join(sanitizedLines("The store cannot be used: "+w.opts.ApplyErr), "\n")
	case w.opts.Apply == nil:
		return applyNoStore
	case w.fresh:
		return applyMustSave
	}
	return ""
}

// applyShown is what showing the Apply pane does: the events of the file as it
// is now, and the first time, the store's name, looked up in the background.
func (w *workspace) applyShown() tea.Cmd {
	synced := w.syncApply()
	a := w.opts.Apply
	if a == nil || a.Name == nil || w.storeAsked {
		return synced
	}
	w.storeAsked = true
	ctx := w.ctx
	return tea.Batch(synced, func() tea.Msg { return storeNameMsg{name: a.Name(ctx)} })
}

// syncApply lists the saved file's events in the Apply pane again. A sample
// that changed takes its selection with it, which drops the plan. Nothing
// changes under an apply.
func (w *workspace) syncApply() tea.Cmd {
	if w.applyBlocked() != "" || w.applying {
		return nil
	}
	before := w.planKey()
	w.apply.setSamples(mapping.Events(w.savedDoc()))
	if w.planKey() != before {
		return w.selectionChanged("the selection changed")
	}
	return nil
}

// storeNamed takes the store's name for the pane's header.
func (w *workspace) storeNamed(msg storeNameMsg) tea.Cmd {
	w.storeName = msg.name
	w.apply.setHeader(w.applyHeader())
	return nil
}

// applyHeader is the store line: "store <name> (<id>) · profile <p>", the id
// alone until the name arrives. The name comes from the store, so the line is
// sanitized.
func (w *workspace) applyHeader() string {
	a := w.opts.Apply
	if a == nil {
		return ""
	}
	store := a.StoreID
	if w.storeName != "" {
		store = w.storeName + " (" + a.StoreID + ")"
	}
	line := "store " + store
	if a.Profile != "" {
		line += " · profile " + a.Profile
	}
	return style.SanitizeTerminal(line)
}

// applyBadge is the Apply pane's sidebar badge: a dash with no usable store,
// the writes and deletes of the plan on screen, how the last apply ended, else
// how many events are selected.
func (w *workspace) applyBadge() string {
	c := w.apply
	switch {
	case w.opts.Apply == nil:
		return "–"
	case c.plan != nil:
		return fmt.Sprintf("+%d −%d", c.plan.plan.Writes, c.plan.plan.Deletes)
	case c.outcome != "":
		return c.outcome
	}
	return strconv.Itoa(c.selected())
}

// applyRequested is what the Apply pane asked for: p, a or r.
func (w *workspace) applyRequested(r applyRequest) tea.Cmd {
	switch r {
	case requestPlan:
		return w.startPlan(1, false)
	case requestApply:
		return w.askApply()
	case requestResume:
		return w.startPlan(w.apply.resumeAt, true)
	}
	return nil
}

// planKey is what a plan depends on: the selected events' sample keys in
// order, a hash of w.file.Src, and the store ID. A plan whose key differs
// from the current one is dropped.
func (w *workspace) planKey() string {
	h := sha256.New()
	if w.file != nil {
		src := sha256.Sum256(w.file.Src)
		h.Write(src[:])
	}
	if a := w.opts.Apply; a != nil {
		h.Write([]byte(a.StoreID))
	}
	for _, e := range w.apply.events {
		if e.selected {
			key, _ := mapping.SampleKey(e.payload)
			h.Write([]byte("\x00" + key))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// planRefusal is why the pane cannot plan now, or "". Apply uses the saved
// file, so not while there are edits on top of it, or with no events to plan,
// or when the file on disk is no longer the one saved.
func (w *workspace) planRefusal() string {
	switch {
	case w.applyBlocked() != "":
		return w.applyBlocked()
	case w.dirty:
		return applyUnsaved
	case w.apply.selected() == 0:
		return "no events selected: space selects one"
	case w.changedOnDisk():
		return applyChangedOnDisk
	}
	return ""
}

// changedOnDisk is whether the file on disk is not the one loaded or saved.
// A file that cannot be read counts: Apply must not run on a guess.
func (w *workspace) changedOnDisk() bool {
	changed, err := w.file.ChangedOnDisk()
	return changed || err != nil
}

// startPlan plans the selected events from event from in the background,
// from the saved file. confirm asks to apply the plan once it lands, as a
// does.
func (w *workspace) startPlan(from int, confirm bool) tea.Cmd {
	if w.applying {
		return w.toast(applyRunning, true)
	}
	if why := w.planRefusal(); why != "" {
		return w.toast(why, true)
	}
	w.cancelPlanning()
	c := w.apply
	c.setPlan(nil)
	c.planErr = nil
	m, _, err := mapping.CompileSource(w.file.Src)
	if err != nil {
		c.planErr = sanitizedLines("the saved file does not compile: " + err.Error())
		return nil
	}
	events := c.selectedEvents()
	ctx, cancel := context.WithCancel(w.ctx)
	w.applyGen++
	gen, reader := w.applyGen, w.opts.Apply.Reader
	feed := make(chan tea.Msg, 1)
	w.applyCancel, w.applyFeed = cancel, feed
	w.pending = &applyPlan{key: w.planKey(), from: from}
	w.confirmNext = confirm
	w.busy++
	c.running = fmt.Sprintf("planning event 1 of %d", len(events)+1-from)
	run := func() tea.Msg {
		defer close(feed)
		p, err := mapping.PlanEvents(ctx, m, events, from, reader, func(done, total int) {
			offer(feed, planProgressMsg{gen: gen, done: done, total: total})
		})
		return planDoneMsg{gen: gen, plan: p, err: err}
	}
	return tea.Batch(w.spin.Tick, run, readFeed(feed))
}

// planProgress moves the planning line on, and waits for the next.
func (w *workspace) planProgress(msg planProgressMsg) tea.Cmd {
	if msg.gen != w.applyGen || w.pending == nil {
		return nil
	}
	w.apply.running = fmt.Sprintf("planning event %d of %d", min(msg.done+1, msg.total), msg.total)
	return readFeed(w.applyFeed)
}

// planDone shows the plan that landed, or why there is none, unless a later
// run replaced it. A plan a or r asked for goes on to the question, but only
// where the user is waiting for it; a plan from p replaces the last report.
func (w *workspace) planDone(msg planDoneMsg) tea.Cmd {
	w.busy--
	if msg.gen != w.applyGen || w.pending == nil {
		return nil
	}
	p, confirm := w.pending, w.confirmNext
	w.endRun()
	c := w.apply
	c.running = ""
	if msg.err != nil {
		c.planErr = sanitizedLines(msg.err.Error())
		return nil
	}
	if w.planKey() != p.key {
		return w.toast("the plan was dropped: the events or the file changed", false)
	}
	// Its age counts from now, so a plan slow to make can still be confirmed.
	p.plan, p.at = msg.plan, w.now()
	c.setPlan(p)
	switch {
	case !confirm:
		c.clearResume()
	case w.atApplyRoot():
		return w.askApply()
	default:
		// The user is typing, or elsewhere: a question opening there would
		// take their next y.
		return w.toast("the plan is ready: a applies it", false)
	}
	return nil
}

// atApplyRoot is whether the user is at the Apply pane's root with nothing
// else taking the keys: the only place the question may open by itself.
func (w *workspace) atApplyRoot() bool {
	return w.overlay == nil && !w.help && w.focus == shell.FocusPanel && w.active == paneApply &&
		w.stacks[paneApply].Depth() == 1 && !w.typing()
}

// cancelPlanning stops the plan in flight, whose result then arrives late.
// It reports whether there was one.
func (w *workspace) cancelPlanning() bool {
	if w.pending == nil {
		return false
	}
	w.endRun()
	w.applyGen++
	w.apply.running = ""
	return true
}

// endRun forgets the run in flight, releasing its context.
func (w *workspace) endRun() {
	if w.applyCancel != nil {
		w.applyCancel()
	}
	w.applyCancel, w.applyFeed, w.applying, w.pending, w.confirmNext = nil, nil, false, nil, false
}

// dropPlan forgets the plan on screen, or the one being made, and why the
// last one failed, because why: what they were made from changed. It says so
// when there was a plan. The resume point stays: a save is how a failed rule
// gets fixed before r. An apply that is running is left alone: it sends the
// plan that was confirmed, and its report says how far it got.
func (w *workspace) dropPlan(why string) tea.Cmd {
	if w.applying {
		return nil
	}
	c := w.apply
	planning := w.cancelPlanning()
	had := planning || c.plan != nil
	c.setPlan(nil)
	c.planErr = nil
	if !had {
		return nil
	}
	return w.toast("the plan was dropped: "+why, false)
}

// selectionChanged is another set of events selected: the plan goes, and the
// resume point with it, since r counts the events it failed on.
func (w *workspace) selectionChanged(why string) tea.Cmd {
	if w.applying {
		return nil
	}
	w.apply.clearResume()
	return w.dropPlan(why)
}

// readyToApply is the checks a plan passes before the question opens and
// again before it runs: still the plan on screen, of the saved file and the
// selection as they are, with something to change, and fresh. When it is
// not, cmd says why, drops the plan, or plans again.
func (w *workspace) readyToApply(p *applyPlan) (cmd tea.Cmd, ok bool) {
	switch {
	case w.apply.plan != p:
		return nil, false
	case p.plan.Writes+p.plan.Deletes == 0:
		return w.toast("nothing to change", true), false
	case w.dirty:
		return w.toast(applyUnsaved, true), false
	case w.changedOnDisk():
		return w.dropPlan(applyChangedOnDisk), false
	case w.planKey() != p.key:
		return w.dropPlan("the events or the file changed"), false
	case w.now().Sub(p.at) > planMaxAge:
		return tea.Batch(w.toast("the plan is over five minutes old: planning it again", false),
			w.startPlan(p.from, true)), false
	}
	return nil, true
}

// askApply is a: the question, worded like the CLI's prompt, on the plan on
// screen. Only y applies it.
func (w *workspace) askApply() tea.Cmd {
	p := w.apply.plan
	if p == nil || w.applying {
		return nil
	}
	if cmd, ok := w.readyToApply(p); !ok {
		return cmd
	}
	a := w.opts.Apply
	q := mapping.ApplyQuestion(p.plan.Writes, p.plan.Deletes, style.SanitizeTerminal(w.storeName),
		style.SanitizeTerminal(a.StoreID))
	return w.openOverlay(newStack(w.sess, newConfirm(w.sess, "Apply", q, "apply")), func(result any) tea.Cmd {
		if result != true {
			return nil
		}
		return w.startApply(p)
	})
}

// startApply sends p, the plan the user said y to, in the background.
func (w *workspace) startApply(p *applyPlan) tea.Cmd {
	if cmd, ok := w.readyToApply(p); !ok {
		return cmd
	}
	ctx, cancel := context.WithCancel(w.ctx)
	w.applyGen++
	gen, plan := w.applyGen, p.plan
	feed := make(chan tea.Msg, 1)
	w.applyCancel, w.applyFeed, w.applying = cancel, feed, true
	w.busy++
	c := w.apply
	c.clearResume()
	c.planErr = nil
	c.applying, c.running = true, fmt.Sprintf("applying event 1 of %d", len(plan.Events))
	// A stop cancels ctx, which Execute checks before each request. The
	// request in flight runs on the workspace's context instead, so it
	// finishes rather than being cut off halfway.
	writer := inFlight{ctx: w.ctx, w: w.opts.Apply.Writer}
	run := func() tea.Msg {
		defer close(feed)
		n, err := mapping.Execute(ctx, plan, writer, func(done, total int) {
			offer(feed, applyProgressMsg{gen: gen, done: done, total: total})
		})
		return applyDoneMsg{gen: gen, applied: n, err: err}
	}
	return tea.Batch(w.spin.Tick, run, readFeed(feed))
}

// applyProgress moves the applying line on, and waits for the next.
func (w *workspace) applyProgress(msg applyProgressMsg) tea.Cmd {
	if msg.gen != w.applyGen || !w.applying {
		return nil
	}
	w.apply.running = fmt.Sprintf("applying event %d of %d", min(msg.done+1, msg.total), msg.total)
	return readFeed(w.applyFeed)
}

// applyDone reports how the apply ended: a toast when every event was sent,
// or else the CLI's report and the offer to resume.
func (w *workspace) applyDone(msg applyDoneMsg) tea.Cmd {
	w.busy--
	if msg.gen != w.applyGen || !w.applying {
		return nil
	}
	w.endRun()
	c := w.apply
	p := c.plan
	c.applying, c.running = false, ""
	c.setPlan(nil)
	if msg.err == nil {
		c.outcome = "✓"
		return w.toast(mapping.SentLine(p.plan, w.storeLabel()), false)
	}
	c.outcome = "✗"
	c.setReport(w.applyReport(p.plan, msg.applied, msg.err))
	return nil
}

// stopApply is ctrl+c or q while applying: Execute returns before its next
// request, and the report says what landed. Nothing quits until then.
func (w *workspace) stopApply() tea.Cmd {
	w.applyCancel()
	return w.toast("stopping after the request in flight", false)
}

// applyReport is how an apply that did not finish ended, worded as the CLI
// reports it (reportApplyFailure): what was applied, why it stopped, the
// hints, then the event to resume from and the CLI command that does.
func (w *workspace) applyReport(p *mapping.Plan, applied int, err error) (report []string, resumeAt int, how []string) {
	a := w.opts.Apply
	ep := p.Events[applied]
	failed := ep.Index
	done := mapping.AppliedSoFar(p, applied)
	if errors.Is(err, context.Canceled) {
		text := fmt.Sprintf("%s; stopped before event %d finished", done, failed)
		if !ep.Atomic {
			text += " (it may be partly written)"
		}
		report = []string{text}
	} else {
		msg := strings.TrimPrefix(err.Error(), fmt.Sprintf("event %d: ", failed))
		report = []string{fmt.Sprintf("%s; event %d failed: %s", done, failed, msg)}
		if !ep.Atomic {
			report = append(report, fmt.Sprintf("event %d goes out in several requests and may be partly written", failed))
		}
		if a.Hints != nil {
			report = append(report, a.Hints(err)...)
		}
	}
	file := w.apply.eventsFile()
	how = []string{a.Resume(w.path, file, failed)}
	if file == "" {
		how = append(how, "put the selected events in a file first")
	}
	return sanitizedLines(report...), failed, sanitizedLines(how...)
}

// storeLabel is the store as the success toast names it: its name, or its ID
// until the name is known.
func (w *workspace) storeLabel() string {
	if name := style.SanitizeTerminal(w.storeName); name != "" {
		return name
	}
	return w.opts.Apply.StoreID
}

// sanitizedLines is lines split at their newlines, each made safe to draw.
func sanitizedLines(lines ...string) []string {
	var out []string
	for _, l := range lines {
		for _, part := range strings.Split(l, "\n") {
			out = append(out, style.SanitizeTerminal(part))
		}
	}
	return out
}

// readFeed waits for a run's next progress message: nothing once the run is
// over.
func readFeed(feed <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-feed }
}

// offer puts msg on feed unless the last one is still unread. Progress can
// skip a step, and the run must never wait on the screen.
func offer(feed chan<- tea.Msg, msg tea.Msg) {
	select {
	case feed <- msg:
	default:
	}
}

// inFlight is the writer an apply sends through. Each request runs on ctx,
// which a stop does not cancel, so the request in flight when ctrl+c is
// pressed finishes; Execute's own context, which the stop does cancel, keeps
// the next one from starting.
type inFlight struct {
	ctx context.Context
	w   mapping.TupleWriter
}

func (f inFlight) Write(_ context.Context, writes, deletes []language.Tuple) error {
	return f.w.Write(f.ctx, writes, deletes)
}
