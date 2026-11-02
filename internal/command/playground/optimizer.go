package playground

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
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
	if m.opt.view != optBench {
		return nil, false
	}
	switch key {
	case "B":
		return m.openBench(), true
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
	}
}
