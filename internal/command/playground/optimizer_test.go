package playground

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

const noGraphDSL = "model\n  schema 1.1\n\ntype user\n\ntype kt\n  relations\n" +
	"    define parent: [kt]\n    define member: [user] or both from parent\n" +
	"    define wide: [user] or member\n    define both: member and wide\n"

// optModel is a test model on the Model tab with the given DSL loaded.
func optModel(t *testing.T, dsl string) tea.Model {
	t.Helper()
	m := newTestModel()
	mm := m.(Model)
	mm.modelDSL = dsl
	m = mm
	m, _ = m.Update(key("3"))
	m, _ = m.Update(key("enter"))
	return m
}

func referenceDSL(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../optimize/testdata/reference.fga")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// benchMsgs runs cmd (flattening batches) and returns the optBenchMsgs it
// produced, ignoring every other message (spinner ticks and the like).
func benchMsgs(cmd tea.Cmd) []optBenchMsg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case optBenchMsg:
		return []optBenchMsg{msg}
	case tea.BatchMsg:
		var out []optBenchMsg
		for _, c := range msg {
			out = append(out, benchMsgs(c)...)
		}
		return out
	}
	return nil
}

// shiftB presses B and delivers the resulting benchmark message when deliver.
func shiftB(t *testing.T, m tea.Model, deliver bool) (tea.Model, []optBenchMsg) {
	t.Helper()
	m, cmd := m.Update(key("B"))
	// Run the batch before anything else touches the model; the bench cmd is
	// self-contained.
	msgs := benchMsgs(cmd)
	if deliver {
		for _, msg := range msgs {
			m, _ = m.Update(msg)
		}
	}
	return m, msgs
}

func view(m tea.Model) string { return ansi.Strip(m.(Model).viewString()) }

func TestOptimizer_ShiftBShowsProfile(t *testing.T) {
	m := optModel(t, referenceDSL(t))
	m, msgs := shiftB(t, m, true)
	if len(msgs) != 1 {
		t.Fatalf("want one optBenchMsg, got %d", len(msgs))
	}
	out := view(m)
	for _, want := range []string{
		"Benchmark · synthesis ·", "Deny reads", "Most expensive deny checks",
		"safe rewrites available — press o to review", "in-process, indicative only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "document") && strings.Contains(out, "weighted graph") {
		t.Errorf("graph content still visible:\n%s", out)
	}
	if mm := m.(Model); mm.opt.running || mm.opt.profile == nil || mm.pendingLoads != 0 || mm.loading {
		t.Errorf("state after result: %+v pending=%d", mm.opt, mm.pendingLoads)
	}
}

func TestOptimizer_LowercaseBStillPagesUp(t *testing.T) {
	m := optModel(t, referenceDSL(t))
	m, cmd := m.Update(key("b"))
	if cmd != nil && len(benchMsgs(cmd)) > 0 {
		t.Fatal("b started a benchmark")
	}
	if m.(Model).opt.view != optNone {
		t.Fatal("b entered the benchmark view")
	}
}

func TestOptimizer_EscReturnsToGraph(t *testing.T) {
	m := optModel(t, referenceDSL(t))
	m, _ = shiftB(t, m, false)
	if !m.(Model).opt.running {
		t.Fatal("expected a running benchmark")
	}
	called := false
	mm := m.(Model)
	inner := mm.opt.cancel
	mm.opt.cancel = func() { called = true; inner() }
	m = mm
	m, _ = m.Update(key("esc"))
	o := m.(Model).opt
	if !called || o.running || o.view != optNone {
		t.Errorf("esc: cancelled=%v running=%v view=%v", called, o.running, o.view)
	}
	if strings.Contains(view(m), "Benchmark ·") {
		t.Error("benchmark view still showing after esc")
	}
}

func TestOptimizer_StaleBenchDropped(t *testing.T) {
	m := optModel(t, referenceDSL(t))
	m, msgs := shiftB(t, m, false)
	if len(msgs) != 1 {
		t.Fatalf("want one optBenchMsg, got %d", len(msgs))
	}
	before := m.(Model).pendingLoads
	// Model switch, as modelLoadedMsg does it.
	m, _ = m.Update(modelLoadedMsg{modelID: "model-2", graph: sampleGraph()})
	m, _ = m.Update(msgs[0])
	o := m.(Model)
	if o.opt.profile != nil || o.opt.running {
		t.Errorf("stale result landed: %+v", o.opt)
	}
	if o.pendingLoads >= before+1 || o.pendingLoads < 0 {
		t.Errorf("pending loads unbalanced: before=%d after=%d", before, o.pendingLoads)
	}
	if o.loading {
		t.Error("spinner still on")
	}
}

func TestOptimizer_NoWeightedGraphInline(t *testing.T) {
	m := optModel(t, noGraphDSL)
	m, _ = shiftB(t, m, true)
	out := view(m)
	want := "couldn't benchmark this model (an AND or BUT NOT sits on a tuple cycle)"
	if !strings.Contains(out, want) {
		t.Errorf("missing %q:\n%s", want, out)
	}
}

func TestOptimizer_LeavingSectionCancels(t *testing.T) {
	m := optModel(t, referenceDSL(t))
	m, _ = shiftB(t, m, false)
	m, _ = m.Update(key("1"))
	o := m.(Model).opt
	if o.running || o.view != optNone || o.cancel != nil {
		t.Errorf("section switch left optimizer active: %+v", o)
	}
}

func TestOptimizer_HelpListsKeys(t *testing.T) {
	m := optModel(t, referenceDSL(t))
	m, _ = m.Update(key("?"))
	out := view(m)
	for _, want := range []string{"benchmark the model", "review optimizations"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func TestOptimizer_ReconnectStopsBench(t *testing.T) {
	configtest.Isolate(t)
	m := optModel(t, referenceDSL(t))
	m, msgs := shiftB(t, m, false)
	if len(msgs) != 1 {
		t.Fatalf("want one optBenchMsg, got %d", len(msgs))
	}
	mm := m.(Model)
	other, _ := openfga.NewClient("http://localhost:9090")
	mm.activateResolved(config.Resolved{StoreID: "store-1", APIURL: "http://localhost:9090"}, other, "switched")
	m = mm
	// The reconnect's own reloads stay pending; only the bench slot must free.
	pending := mm.pendingLoads
	m, _ = m.Update(msgs[0])
	o := m.(Model)
	if o.opt.view != optNone || o.opt.running {
		t.Errorf("reconnect left optimizer active: %+v", o.opt)
	}
	if o.pendingLoads != pending-1 {
		t.Errorf("pending loads unbalanced: before=%d after=%d", pending, o.pendingLoads)
	}
}
