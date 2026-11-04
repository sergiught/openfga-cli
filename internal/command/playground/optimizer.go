package playground

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/aymanbagabas/go-udiff"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
	"github.com/sergiught/openfga-cli/internal/optimize/bench"
)

// benchIterations is the timed-check count per benchmark run; the CLI default
// is 100, the playground trades precision for responsiveness.
const benchIterations = 20

// noGraphMsg is shown when the model's weighted graph cannot be built, so no
// tuples can be synthesized; reason is the builder's own message.
func noGraphMsg(reason string) string {
	return "couldn't benchmark this model (" + reason + ")"
}

type optView int

const (
	optNone optView = iota
	optBench
	optReview
)

type optBenchKind int

const (
	optKindProfile optBenchKind = iota
	optKindSelection
	optKindWrite
)

// optimizer is the Model tab's optimizer state: the benchmark view today, the
// review view and write dialog on top of it.
type optimizer struct {
	view    optView
	gen     int                           // bumped on every run start / view exit
	cancel  context.CancelFunc            // cancels the in-flight benchmark, nil when idle
	running bool                          // a benchmark is in flight
	proto   *openfgav1.AuthorizationModel // the loaded model, parsed once per entry
	profile *bench.Result                 // last Shift-B result
	err     string                        // inline error for the current view
	cursor  int                           // selected row in the current view
	safeN   int                           // number of safe rewrites found by Analyze (for the hint)

	// Review view (o).
	report   *optimize.Report
	model    *optimize.Model
	rows     []optimize.Finding // safe rewrites, report.Rewrite.Applied
	keys     []optimize.FindingKey
	accepted map[optimize.FindingKey]bool
	subset   *optimize.RewriteResult // RewriteSubset(model, accepted)
	advice   []optimize.Finding      // findings that are not safe rewrites
	gated    []optimize.GatedFinding // derived safe rewrites that were not applied; not selectable
	selBench *bench.Result           // last review-B result; cleared on toggle or cursor move

	// Write dialog (w).
	dialog       bool          // the dialog is open over the review
	dialogScroll int           // first diff line shown
	dialogDiff   []string      // Before → After list then unified diff of the applied rewrites, built on open
	writeBench   *bench.Result // answer check of the applied rewrites
	writeNoGraph string        // builder message when there is no weighted graph: the check can't run, write is allowed
	writeErr     string        // last write failure, shown in the dialog
	errNoGraph   bool          // err is the no-weighted-graph notice: a warning, not a failure
}

// appliedKeys is the set of accepted rewrites that actually apply; an
// accepted rewrite that depends on a rejected one drops out.
func (o optimizer) appliedKeys() map[optimize.FindingKey]bool {
	out := map[optimize.FindingKey]bool{}
	if o.subset == nil {
		return out
	}
	for _, k := range optimize.Keys(o.subset.Applied) {
		out[k] = true
	}
	return out
}

// optBenchMsg carries a finished benchmark back to the update loop.
type optBenchMsg struct {
	origin  mutationOrigin
	kind    optBenchKind
	result  *bench.Result
	noGraph string // builder message when Synthesize returned ErrNoWeightedGraph
	err     error
}

// startBench benchmarks before (and after, when non-nil) over tuples
// synthesized from before. It cancels any previous run, so only one is ever
// in flight.
func (m *Model) startBench(kind optBenchKind, before, after *openfgav1.AuthorizationModel) tea.Cmd {
	if m.opt.cancel != nil {
		m.opt.cancel()
	}
	m.opt.gen++
	ctx, cancel := context.WithCancel(m.reqCtx)
	m.opt.cancel = cancel
	m.opt.running = true
	m.opt.err, m.opt.errNoGraph = "", false
	m.beginLoad()
	origin := m.mutationOrigin(m.storeID, m.modelID, m.opt.gen)
	rw := m.opt.subset
	return func() tea.Msg {
		msg := optBenchMsg{origin: origin, kind: kind}
		src, err := bench.Synthesize(before, bench.SynthOptions{})
		if reason, ok := optimize.NoWeightedGraphReason(err); ok {
			msg.noGraph = reason
			return msg
		}
		if err != nil {
			msg.err = err
			return msg
		}
		msg.result, msg.err = bench.Run(ctx, before, after, src, bench.Options{Iterations: benchIterations})
		if msg.result != nil && rw != nil && after != nil {
			msg.result.Unexercised = optimize.Unexercised(rw.After, rw.Applied, msg.result.Skipped)
		}
		return msg
	}
}

// stopOptimizer cancels any benchmark and leaves the optimizer views. The
// bumped generation makes the cancelled run's message stale on arrival.
func (m *Model) stopOptimizer() {
	if m.opt.cancel != nil {
		m.opt.cancel()
	}
	m.opt = optimizer{gen: m.opt.gen + 1}
}

// openBench handles Shift-B on the Model tab: profile the loaded model.
func (m *Model) openBench() tea.Cmd {
	if m.storeID == "" {
		m.status = "select a store first"
		return nil
	}
	if m.modelDSL == "" {
		m.status = "no model loaded"
		return nil
	}
	lm, err := modeltest.LoadModelBytes([]byte(m.modelDSL))
	if err != nil {
		m.status = "benchmark: " + errStr(err)
		return nil
	}
	m.opt.proto = lm.Proto
	m.opt.safeN = 0
	if rep, err := optimize.Analyze(lm.Proto); err == nil && rep.Rewrite != nil {
		m.opt.safeN = len(rep.Rewrite.Applied)
	}
	m.opt.view = optBench
	m.opt.profile = nil
	m.opt.cursor = 0
	return m.startBench(optKindProfile, lm.Proto, nil)
}

// handleOptKey handles keys in the optimizer views; it reports whether the
// key was consumed.
func (m *Model) handleOptKey(key string) (tea.Cmd, bool) {
	switch m.opt.view {
	case optNone:
		if key == "o" {
			return m.openReview(), true
		}
		return nil, false
	case optReview:
		return m.handleReviewKey(key)
	}
	switch key {
	case "B":
		return m.openBench(), true
	case "o":
		return m.openReview(), true
	case "up", "k":
		m.moveOptCursor(-1)
		return nil, true
	case "down", "j":
		m.moveOptCursor(1)
		return nil, true
	}
	if d, ok := m.pagingDelta(key); ok {
		m.moveOptCursor(d)
		return nil, true
	}
	return nil, false
}

// optJump moves the cursor to the first or last row.
const optJump = 1 << 30

// pagingDelta maps a paging key to a cursor delta: a page is the visible
// rows of the body, home/end jump to the first/last row.
func (m *Model) pagingDelta(key string) (int, bool) {
	_, h := m.contentSize()
	page := max(1, h-4)
	switch key {
	case "pgup":
		return -page, true
	case "pgdown":
		return page, true
	case "home", "g":
		return -optJump, true
	case "end", "G":
		return optJump, true
	}
	return 0, false
}

func (m *Model) moveOptCursor(d int) {
	n := len(denyRows(m.opt.profile))
	if m.opt.view == optReview {
		n = len(m.opt.rows) + len(m.opt.gated) + len(m.opt.advice)
		m.opt.selBench = nil
		m.opt.err, m.opt.errNoGraph = "", false
	}
	m.opt.cursor = max(0, min(m.opt.cursor+d, n-1))
}

// handleOptBench applies a finished benchmark. The slot is freed first, as for
// every load completion, then stale results are dropped silently.
func (m *Model) handleOptBench(msg optBenchMsg) {
	m.endLoad()
	if m.staleMutation(msg.origin, m.opt.gen) || staleCancel(msg.err) {
		return
	}
	m.opt.running = false
	if m.opt.cancel != nil {
		m.opt.cancel()
		m.opt.cancel = nil
	}
	switch msg.kind {
	case optKindProfile:
		switch {
		case msg.noGraph != "":
			m.opt.err = noGraphMsg(msg.noGraph)
			m.opt.errNoGraph = true
		case msg.err != nil:
			m.opt.err, m.opt.errNoGraph = errStr(msg.err), false
		default:
			m.opt.profile = msg.result
			m.opt.cursor = 0
		}
	case optKindSelection:
		switch {
		case msg.noGraph != "":
			m.opt.err = noGraphMsg(msg.noGraph)
			m.opt.errNoGraph = true
		case msg.err != nil:
			m.opt.err, m.opt.errNoGraph = errStr(msg.err), false
		default:
			m.opt.selBench = msg.result
		}
	case optKindWrite:
		switch {
		case msg.noGraph != "":
			m.opt.writeNoGraph = msg.noGraph
		case msg.err != nil:
			m.opt.err, m.opt.errNoGraph = errStr(msg.err), false
		default:
			m.opt.writeBench = msg.result
		}
	}
}

// openReview handles o on the Model tab: list the loaded model's safe
// rewrites for the user to accept.
func (m *Model) openReview() tea.Cmd {
	if m.storeID == "" {
		m.status = "select a store first"
		return nil
	}
	if m.modelDSL == "" {
		m.status = "no model loaded"
		return nil
	}
	lm, err := modeltest.LoadModelBytes([]byte(m.modelDSL))
	if err != nil {
		m.status = "optimize: " + errStr(err)
		return nil
	}
	m.stopOptimizer()
	m.opt.view = optReview
	m.opt.proto = lm.Proto
	m.opt.accepted = map[optimize.FindingKey]bool{}
	rep, err := optimize.Analyze(lm.Proto)
	if err != nil {
		m.opt.err, m.opt.errNoGraph = errStr(err), false
		return nil
	}
	om, err := optimize.NewModel(lm.Proto)
	if err != nil {
		m.opt.err, m.opt.errNoGraph = errStr(err), false
		return nil
	}
	m.opt.report, m.opt.model = rep, om
	if rep.Rewrite != nil {
		m.opt.rows = rep.Rewrite.Applied
		m.opt.keys = optimize.Keys(m.opt.rows)
	}
	m.opt.gated = rep.Gated
	for _, f := range rep.Findings {
		if f.Tier != optimize.TierSafe {
			m.opt.advice = append(m.opt.advice, f)
		}
	}
	m.recomputeSubset()
	return nil
}

// recomputeSubset re-derives what the accepted set would apply, and drops any
// benchmark of the previous selection.
func (m *Model) recomputeSubset() {
	m.invalidateSelection()
	sub, err := optimize.RewriteSubset(m.opt.model, m.opt.accepted)
	if err != nil {
		m.opt.err, m.opt.errNoGraph = errStr(err), false
		return
	}
	m.opt.subset = sub
}

// invalidateSelection forgets the selection benchmark, cancelling one in
// flight (its result arrives stale).
func (m *Model) invalidateSelection() {
	if m.opt.cancel != nil {
		m.opt.cancel()
		m.opt.cancel = nil
		m.opt.gen++
	}
	m.opt.running = false
	m.opt.selBench = nil
	m.opt.err, m.opt.errNoGraph = "", false
}

// noApplyHint explains why nothing can be benchmarked or written yet.
func (o optimizer) noApplyHint() string {
	if o.report != nil && o.report.Blocked != "" {
		return "rewrites are blocked for this model"
	}
	if len(o.rows) == 0 {
		return "no safe rewrites to apply"
	}
	if len(o.accepted) > 0 {
		return "accepted rewrites depend on rejected ones"
	}
	return "accept a rewrite first"
}

// openWriteDialog handles w in the review: confirm writing the applied
// rewrites, gated on an answer check of exactly that selection.
func (m *Model) openWriteDialog() tea.Cmd {
	if m.opt.subset == nil || len(m.opt.subset.Applied) == 0 {
		m.status = m.opt.noApplyHint()
		return nil
	}
	before, err := m.opt.subset.Before.DSL()
	if err != nil {
		m.status = "optimize: " + errStr(err)
		return nil
	}
	after, err := m.opt.subset.After.DSL()
	if err != nil {
		m.status = "optimize: " + errStr(err)
		return nil
	}
	m.opt.dialog, m.opt.dialogScroll = true, 0
	m.opt.writeBench, m.opt.writeNoGraph, m.opt.writeErr = nil, "", ""
	m.opt.dialogDiff = dialogBody(m.opt.subset.Applied, before, after)
	return m.startBench(optKindWrite, m.opt.proto, m.opt.subset.After.Proto)
}

// dialogBody lists each applied rewrite's before and after, then the unified
// diff of the whole model.
func dialogBody(applied []optimize.Finding, before, after string) []string {
	var lines []string
	for _, f := range applied {
		lines = append(lines, f.Type+"#"+f.Relation, "- "+f.Before, "+ "+f.After)
	}
	lines = append(lines, "")
	return append(lines, strings.Split(strings.TrimRight(udiff.Unified("model", "model (optimized)", before, after), "\n"), "\n")...)
}

// closeWriteDialog returns to the review with the accepted set intact; the
// answer check is cancelled, so a late result arrives stale.
func (m *Model) closeWriteDialog() {
	m.opt.dialog, m.opt.dialogScroll, m.opt.dialogDiff = false, 0, nil
	m.opt.writeBench, m.opt.writeNoGraph, m.opt.writeErr = nil, "", ""
	m.invalidateSelection()
}

// canWrite reports whether the dialog's Write is enabled: the answer check
// finished with no changed answer, or it cannot run at all.
func (o optimizer) canWrite() bool {
	if o.running || o.err != "" {
		return false
	}
	return o.writeNoGraph != "" || (o.writeBench != nil && len(o.writeBench.Mismatches) == 0)
}

// handleDialogKey handles keys while the write dialog is open; it swallows
// every key.
func (m *Model) handleDialogKey(key string) tea.Cmd {
	switch key {
	case "esc":
		m.closeWriteDialog()
	case "up", "k":
		m.scrollDialog(-1)
	case "down", "j":
		m.scrollDialog(1)
	case "pgup", "pgdown", "home", "end", "g", "G":
		_, _, diffH := m.writeDialogLayout()
		page := max(1, diffH-1)
		d := map[string]int{"pgup": -page, "pgdown": page, "home": -optJump, "g": -optJump, "end": optJump, "G": optJump}[key]
		m.scrollDialog(d)
	case "y":
		if m.modelApplying {
			m.status = "model apply already in progress"
			return nil
		}
		if !m.opt.canWrite() {
			return nil
		}
		dsl, err := m.opt.subset.After.DSL()
		if err != nil {
			m.opt.writeErr = errStr(err)
			return nil
		}
		m.opt.writeErr = ""
		m.beginLoad()
		m.modelApplying = true
		m.modelApplyGen++
		n := len(m.opt.subset.Applied)
		m.mutationStatus = "writing optimized model…"
		m.status = m.mutationStatus
		apply := applyModelCmd(m.ctx, m.client, m.mutationOrigin(m.storeID, m.modelID, m.modelApplyGen), dsl)
		return func() tea.Msg {
			msg := apply()
			if am, ok := msg.(modelAppliedMsg); ok {
				am.optimized = n
				return am
			}
			return msg
		}
	}
	return nil
}

func (m *Model) scrollDialog(d int) {
	_, _, diffH := m.writeDialogLayout()
	maxScroll := max(0, len(m.wrappedDialogBody())-diffH)
	m.opt.dialogScroll = max(0, min(m.opt.dialogScroll+d, maxScroll))
}

func (m *Model) handleReviewKey(key string) (tea.Cmd, bool) {
	switch key {
	case "up", "k":
		m.moveOptCursor(-1)
	case "down", "j":
		m.moveOptCursor(1)
	case "pgup", "pgdown", "home", "end", "g", "G":
		d, _ := m.pagingDelta(key)
		m.moveOptCursor(d)
	case "space", " ":
		if c := m.opt.cursor; c < len(m.opt.keys) {
			if k := m.opt.keys[c]; m.opt.accepted[k] {
				delete(m.opt.accepted, k)
			} else {
				m.opt.accepted[k] = true
			}
			m.recomputeSubset()
		}
	case "a":
		if len(m.opt.keys) > 0 {
			all := len(m.opt.accepted) < len(m.opt.keys)
			m.opt.accepted = map[optimize.FindingKey]bool{}
			if all {
				for _, k := range m.opt.keys {
					m.opt.accepted[k] = true
				}
			}
			m.recomputeSubset()
		}
	case "B":
		if m.opt.subset == nil || len(m.opt.subset.Applied) == 0 {
			m.status = m.opt.noApplyHint()
			return nil, true
		}
		m.opt.selBench = nil
		return m.startBench(optKindSelection, m.opt.proto, m.opt.subset.After.Proto), true
	case "w":
		return m.openWriteDialog(), true
	case "o":
	default:
		return nil, false
	}
	return nil, true
}
