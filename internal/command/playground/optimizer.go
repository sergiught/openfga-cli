package playground

import (
	"context"
	"errors"
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
// tuples can be synthesized.
const noGraphMsg = "couldn't benchmark this model (an AND or BUT NOT sits on a tuple cycle)"

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
	selBench *bench.Result           // last review-B result; cleared on toggle or cursor move

	// Write dialog (w).
	dialog       bool          // the dialog is open over the review
	dialogScroll int           // first diff line shown
	dialogDiff   []string      // unified diff of the applied rewrites, built on open
	writeBench   *bench.Result // answer check of the applied rewrites
	writeNoGraph bool          // no weighted graph: the check can't run, write is allowed
	writeErr     string        // last write failure, shown in the dialog
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
	noGraph bool // Synthesize returned ErrNoWeightedGraph
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
	m.opt.err = ""
	m.beginLoad()
	origin := m.mutationOrigin(m.storeID, m.modelID, m.opt.gen)
	return func() tea.Msg {
		msg := optBenchMsg{origin: origin, kind: kind}
		src, err := bench.Synthesize(before, bench.SynthOptions{})
		if errors.Is(err, optimize.ErrNoWeightedGraph) {
			msg.noGraph = true
			return msg
		}
		if err != nil {
			msg.err = err
			return msg
		}
		msg.result, msg.err = bench.Run(ctx, before, after, src, bench.Options{Iterations: benchIterations})
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
	return nil, false
}

func (m *Model) moveOptCursor(d int) {
	n := len(denyRows(m.opt.profile))
	if m.opt.view == optReview {
		n = len(m.opt.rows) + len(m.opt.advice)
		m.opt.selBench = nil
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
		case msg.noGraph:
			m.opt.err = noGraphMsg
		case msg.err != nil:
			m.opt.err = errStr(msg.err)
		default:
			m.opt.profile = msg.result
			m.opt.cursor = 0
		}
	case optKindSelection:
		switch {
		case msg.noGraph:
			m.opt.err = noGraphMsg
		case msg.err != nil:
			m.opt.err = errStr(msg.err)
		default:
			m.opt.selBench = msg.result
		}
	case optKindWrite:
		switch {
		case msg.noGraph:
			m.opt.writeNoGraph = true
		case msg.err != nil:
			m.opt.err = errStr(msg.err)
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
		m.opt.err = errStr(err)
		return nil
	}
	om, err := optimize.NewModel(lm.Proto)
	if err != nil {
		m.opt.err = errStr(err)
		return nil
	}
	m.opt.report, m.opt.model = rep, om
	if rep.Rewrite != nil {
		m.opt.rows = rep.Rewrite.Applied
		m.opt.keys = optimize.Keys(m.opt.rows)
	}
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
		m.opt.err = errStr(err)
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
	m.opt.err = ""
}

// noApplyHint explains why nothing can be benchmarked or written yet.
func (o optimizer) noApplyHint() string {
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
	m.opt.writeBench, m.opt.writeNoGraph, m.opt.writeErr = nil, false, ""
	m.opt.dialogDiff = strings.Split(strings.TrimRight(udiff.Unified("model", "model (optimized)", before, after), "\n"), "\n")
	return m.startBench(optKindWrite, m.opt.proto, m.opt.subset.After.Proto)
}

// closeWriteDialog returns to the review with the accepted set intact; the
// answer check is cancelled, so a late result arrives stale.
func (m *Model) closeWriteDialog() {
	m.opt.dialog, m.opt.dialogScroll, m.opt.dialogDiff = false, 0, nil
	m.opt.writeBench, m.opt.writeNoGraph, m.opt.writeErr = nil, false, ""
	m.invalidateSelection()
}

// canWrite reports whether the dialog's Write is enabled: the answer check
// finished with no changed answer, or it cannot run at all.
func (o optimizer) canWrite() bool {
	if o.running || o.err != "" {
		return false
	}
	return o.writeNoGraph || (o.writeBench != nil && len(o.writeBench.Mismatches) == 0)
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
	maxScroll := max(0, len(m.opt.dialogDiff)-diffH)
	m.opt.dialogScroll = max(0, min(m.opt.dialogScroll+d, maxScroll))
}

func (m *Model) handleReviewKey(key string) (tea.Cmd, bool) {
	switch key {
	case "up", "k":
		m.moveOptCursor(-1)
	case "down", "j":
		m.moveOptCursor(1)
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
