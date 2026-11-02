package playground

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openfga/language/pkg/go/transformer"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/optimize/bench"
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

const dependentDSL = "model\n  schema 1.1\n\ntype user\n\ntype t\n  relations\n" +
	"    define a: [user]\n    define b: [user] or a\n    define c: [user] or b\n    define d: a or c or b\n"

const blockedDSL = "model\n  schema 1.1\n\ntype user\n\ntype folder\n  relations\n" +
	"    define parent: [folder]\n    define owner: [user]\n" +
	"    define viewer: [user] or viewer from parent\n    define allowed: owner but not viewer\n"

// review presses o on the Model tab.
func review(t *testing.T, dsl string) tea.Model {
	t.Helper()
	m, _ := optModel(t, dsl).Update(key("o"))
	return m
}

func TestOptimizer_ReviewListsRewrites(t *testing.T) {
	m := review(t, referenceDSL(t))
	out := view(m)
	for _, want := range []string{
		"Safe rewrites · 0 of 5 accepted", "[ ] organization#can_view S1", "[ ] document#can_view S4",
		"Worth a look · ", "why it's safe",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	o := m.(Model).opt
	f := o.rows[0]
	for _, want := range []string{"- " + f.Before, "+ " + f.After} {
		if !strings.Contains(out, want) {
			t.Errorf("card missing %q:\n%s", want, out)
		}
	}
	if o.view != optReview || len(o.accepted) != 0 {
		t.Errorf("state: %+v", o)
	}
}

func TestOptimizer_SpaceAndA(t *testing.T) {
	m := review(t, referenceDSL(t))
	m, _ = m.Update(key(" "))
	out := view(m)
	if !strings.Contains(out, "1 of 5 accepted") || !strings.Contains(out, "[✓] organization#can_view") {
		t.Errorf("space did not accept row 0:\n%s", out)
	}
	m, _ = m.Update(key("a"))
	if !strings.Contains(view(m), "5 of 5 accepted") {
		t.Error("a did not accept all")
	}
	m, _ = m.Update(key("a"))
	if !strings.Contains(view(m), "0 of 5 accepted") {
		t.Error("second a did not clear all")
	}
}

func TestOptimizer_DependentRowDimmed(t *testing.T) {
	m := review(t, dependentDSL)
	o := m.(Model).opt
	if len(o.rows) != 2 {
		t.Fatalf("want 2 rewrites, got %d", len(o.rows))
	}
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key(" ")) // accept only the dependent row
	out := view(m)
	if !strings.Contains(out, "depends on a rejected rewrite") {
		t.Errorf("dependent row not flagged:\n%s", out)
	}
	if !strings.Contains(out, "1 of 2 accepted (0 apply)") {
		t.Errorf("header missing apply count:\n%s", out)
	}
}

func TestOptimizer_BlockedModel(t *testing.T) {
	m := review(t, blockedDSL)
	out := view(m)
	if !strings.Contains(out, "subtracts a recursive") {
		t.Errorf("blocked reason missing:\n%s", out)
	}
	if strings.Contains(out, "Safe rewrites") {
		t.Errorf("checklist shown for blocked model:\n%s", out)
	}
	m, _ = m.Update(key("w"))
	mm := m.(Model)
	if mm.opt.view != optReview || mm.opt.running {
		t.Error("w changed state on a blocked model")
	}
}

func TestOptimizer_NothingToOptimize(t *testing.T) {
	m := review(t, "model\n  schema 1.1\n\ntype user\n")
	if out := view(m); !strings.Contains(out, "Nothing to optimize") {
		t.Errorf("missing message:\n%s", out)
	}
}

func TestOptimizer_ReviewBNeedsSelection(t *testing.T) {
	m := review(t, referenceDSL(t))
	m, msgs := shiftB(t, m, false)
	if len(msgs) != 0 || m.(Model).opt.running || m.(Model).status != "accept a rewrite first" {
		t.Errorf("running=%v status=%q msgs=%d", m.(Model).opt.running, m.(Model).status, len(msgs))
	}
}

func TestOptimizer_ReviewBShowsSummary(t *testing.T) {
	m := review(t, referenceDSL(t))
	m, _ = m.Update(key("a"))
	m, msgs := shiftB(t, m, true)
	if len(msgs) != 1 || msgs[0].kind != optKindSelection {
		t.Fatalf("want one selection msg, got %+v", msgs)
	}
	if out := view(m); !strings.Contains(out, "Deny checks whose cost changed") {
		t.Errorf("summary missing:\n%s", out)
	}
	m, _ = m.Update(key("down"))
	if m.(Model).opt.selBench != nil {
		t.Error("cursor move kept the summary")
	}
}

func TestOptimizer_EscFromReview(t *testing.T) {
	m := review(t, referenceDSL(t))
	m, _ = m.Update(key("a"))
	m, _ = m.Update(key("esc"))
	if m.(Model).opt.view != optNone {
		t.Fatal("esc stayed in review")
	}
	m, _ = m.Update(key("o"))
	if o := m.(Model).opt; o.view != optReview || len(o.accepted) != 0 {
		t.Errorf("reopened with accepted=%v", o.accepted)
	}
}

func TestOptimizer_NarrowTerminal(t *testing.T) {
	check := func(name string, m tea.Model) {
		t.Helper()
		for i, l := range strings.Split(m.(Model).viewString(), "\n") {
			if w := ansi.StringWidth(l); w > 40 {
				t.Errorf("%s: line %d is %d cells: %q", name, i, w, ansi.Strip(l))
			}
		}
	}
	m := optModel(t, referenceDSL(t))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	b, _ := shiftB(t, m, true)
	check("bench", b)
	r, _ := b.Update(key("o"))
	check("review", r)
	r, _ = r.Update(key("a"))
	r, _ = shiftB(t, r, true)
	check("review+selection", r)
}

func TestOptimizer_ReviewBDependentOnly(t *testing.T) {
	m := review(t, dependentDSL)
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key(" "))
	m, msgs := shiftB(t, m, false)
	if len(msgs) != 0 || m.(Model).status != "accepted rewrites depend on rejected ones" {
		t.Errorf("status=%q msgs=%d", m.(Model).status, len(msgs))
	}
}

func TestOptimizer_LateSelectionBenchDropped(t *testing.T) {
	m := review(t, referenceDSL(t))
	m, _ = m.Update(key("a"))
	m, msgs := shiftB(t, m, false)
	if len(msgs) != 1 {
		t.Fatalf("want one msg, got %d", len(msgs))
	}
	before := m.(Model).pendingLoads
	m, _ = m.Update(key(" "))
	m, _ = m.Update(msgs[0])
	o := m.(Model)
	if o.opt.selBench != nil || o.opt.running {
		t.Errorf("late result landed: %+v", o.opt)
	}
	if o.pendingLoads != before-1 || o.loading {
		t.Errorf("pending loads unbalanced: before=%d after=%d loading=%v", before, o.pendingLoads, o.loading)
	}
}

// pressW presses w in the review and returns the write-bench messages it
// started; with deliver they are applied to the model first.
func pressW(t *testing.T, m tea.Model, deliver bool) (tea.Model, []optBenchMsg) {
	t.Helper()
	m, cmd := m.Update(key("w"))
	msgs := benchMsgs(cmd)
	if deliver {
		for _, msg := range msgs {
			m, _ = m.Update(msg)
		}
	}
	return m, msgs
}

// writeDialog accepts every rewrite of dsl and opens the write dialog.
func writeDialog(t *testing.T, dsl string, deliver bool) (tea.Model, []optBenchMsg) {
	t.Helper()
	m := review(t, dsl)
	m, _ = m.Update(key("a"))
	m, msgs := pressW(t, m, deliver)
	if len(msgs) != 1 || msgs[0].kind != optKindWrite {
		t.Fatalf("want one write msg, got %+v", msgs)
	}
	return m, msgs
}

func TestOptimizer_WDisabledWhenNoneApplied(t *testing.T) {
	m := review(t, referenceDSL(t))
	m, msgs := pressW(t, m, false)
	o := m.(Model)
	if o.opt.dialog || len(msgs) != 0 || o.status != "accept a rewrite first" {
		t.Errorf("dialog=%v msgs=%d status=%q", o.opt.dialog, len(msgs), o.status)
	}
}

func TestOptimizer_DialogGatesOnBenchmark(t *testing.T) {
	m, msgs := writeDialog(t, referenceDSL(t), false)
	out := view(m)
	for _, want := range []string{"Write optimized model?", "5 rewrites · new model version on demo", "checking answers"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if _, cmd := m.Update(key("y")); cmd != nil {
		t.Error("y wrote before the check finished")
	}
	m, _ = m.Update(msgs[0])
	out = view(m)
	if !strings.Contains(out, "Answers          all ") || !strings.Contains(out, " unchanged") {
		t.Errorf("totals missing:\n%s", out)
	}
	if !strings.Contains(out, "+++ model (optimized)") || !strings.Contains(out, "│  -  ") || !strings.Contains(out, "│  +  ") {
		t.Errorf("diff missing:\n%s", out)
	}
	if !strings.Contains(out, "y write · esc cancel") {
		t.Errorf("footer missing:\n%s", out)
	}
	if _, cmd := m.Update(key("y")); cmd == nil {
		t.Error("y did not write")
	}
}

func TestOptimizer_MismatchBlocksWrite(t *testing.T) {
	m, msgs := writeDialog(t, referenceDSL(t), false)
	msgs[0].result.Mismatches = []bench.Mismatch{{Check: bench.Check{Name: "chk-one", User: "user:a", Relation: "viewer", Object: "doc:1"}}}
	m, _ = m.Update(msgs[0])
	out := view(m)
	for _, want := range []string{"1 answer changed — not writing", "chk-one", "please report this as a bug"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if _, cmd := m.Update(key("y")); cmd != nil {
		t.Error("y wrote despite a changed answer")
	}
}

func TestOptimizer_UnstableDoesNotBlock(t *testing.T) {
	m, msgs := writeDialog(t, referenceDSL(t), false)
	msgs[0].result.Unstable = []bench.Mismatch{{Check: bench.Check{Name: "flaky"}}}
	m, _ = m.Update(msgs[0])
	if out := view(m); !strings.Contains(out, "1 unstable") {
		t.Errorf("unstable note missing:\n%s", out)
	}
	if _, cmd := m.Update(key("y")); cmd == nil {
		t.Error("unstable answers blocked the write")
	}
}

func TestOptimizer_NoGraphWarnsAndAllowsWrite(t *testing.T) {
	m, msgs := writeDialog(t, referenceDSL(t), false)
	m, _ = m.Update(optBenchMsg{origin: msgs[0].origin, kind: optKindWrite, noGraph: true})
	out := strings.Join(strings.Fields(view(m)), " ")
	// The dialog box borders break the wrapped lines; compare word by word.
	for _, w := range strings.Fields("couldn't benchmark this model (an AND or BUT NOT sits on a tuple cycle) — rewrites are proven safe by the rules but not double-checked against answers") {
		if !strings.Contains(out, w) {
			t.Fatalf("warning word %q missing:\n%s", w, view(m))
		}
	}
	if _, cmd := m.Update(key("y")); cmd == nil {
		t.Error("no-graph model could not be written")
	}
}

// twoDependentDSL is dependentDSL plus an independent copy of its type, so
// ticking the copy's rewrite and the dependent one alone gives K=2, M=1.
const twoDependentDSL = dependentDSL + "\ntype t2\n  relations\n" +
	"    define a: [user]\n    define b: [user] or a\n    define c: [user] or b\n    define d: a or c or b\n"

func TestOptimizer_WriteCountsApplied(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"authorization_model_id":"01NEW"}`))
	}))
	defer srv.Close()
	m := review(t, twoDependentDSL)
	mm := m.(Model)
	cl, err := openfga.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	mm.client = cl
	m = mm
	// Tick the independent t2 rewrite (row 1) and the dependent t rewrite
	// (row 2) but not its prerequisite: K=2 ticked, M=1 applied.
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key(" "))
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key(" "))
	if o := m.(Model).opt; len(o.accepted) != 2 || len(o.subset.Applied) != 1 {
		t.Fatalf("fixture: K=%d M=%d, want 2 and 1", len(o.accepted), len(o.subset.Applied))
	}
	m, msgs := pressW(t, m, true)
	if len(msgs) != 1 {
		t.Fatalf("want one write msg, got %d", len(msgs))
	}
	sub := m.(Model).opt.subset
	if out := view(m); !strings.Contains(out, "1 rewrite · new model version on demo") {
		t.Errorf("dialog does not count applied rewrites:\n%s", out)
	}
	_, cmd := m.Update(key("y"))
	if cmd == nil {
		t.Fatal("y did not write")
	}
	applied, ok := cmd().(modelAppliedMsg)
	if !ok || applied.err != nil || applied.optimized != len(sub.Applied) {
		t.Fatalf("msg=%+v", applied)
	}
	wantDSL, err := sub.After.DSL()
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := transformer.TransformDSLToJSON(wantDSL)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["type_definitions"], want["type_definitions"]) {
		t.Errorf("written model differs:\n got %v\nwant %v", got["type_definitions"], want["type_definitions"])
	}
}

func TestOptimizer_WriteSuccess(t *testing.T) {
	configtest.Isolate(t)
	m, msgs := writeDialog(t, referenceDSL(t), true)
	_ = msgs
	m, cmd := m.Update(key("y"))
	if cmd == nil {
		t.Fatal("y did not write")
	}
	mm := m.(Model)
	m, cmd = m.Update(modelAppliedMsg{origin: mm.mutationOrigin(mm.storeID, mm.modelID, mm.modelApplyGen), modelID: "01NEWMODELIDLONGERTHANFOURTEEN", optimized: 3})
	o := m.(Model)
	if want := "wrote model " + short("01NEWMODELIDLONGERTHANFOURTEEN") + " · 3 rewrites"; o.status != want {
		t.Errorf("status=%q want %q", o.status, want)
	}
	if o.opt.view != optNone || o.opt.dialog || cmd == nil || o.modelApplying {
		t.Errorf("view=%v dialog=%v cmd=%v applying=%v", o.opt.view, o.opt.dialog, cmd != nil, o.modelApplying)
	}
}

func TestOptimizer_WriteErrorKeepsDialog(t *testing.T) {
	m, _ := writeDialog(t, referenceDSL(t), true)
	m, _ = m.Update(key("y"))
	mm := m.(Model)
	mm.editorErr = "untouched"
	m, _ = mm.Update(modelAppliedMsg{origin: mm.mutationOrigin(mm.storeID, mm.modelID, mm.modelApplyGen), err: errors.New("boom"), optimized: 5})
	if out := view(m); !strings.Contains(out, "boom") || !m.(Model).opt.dialog {
		t.Errorf("error not shown in dialog:\n%s", out)
	}
	if m.(Model).editorErr != "untouched" {
		t.Errorf("editorErr changed: %q", m.(Model).editorErr)
	}
	m, _ = m.Update(key("esc"))
	o := m.(Model)
	if o.opt.dialog || o.opt.view != optReview || len(o.opt.accepted) != 5 {
		t.Errorf("dialog=%v view=%v accepted=%d", o.opt.dialog, o.opt.view, len(o.opt.accepted))
	}
}

func TestOptimizer_WriteWhileApplying(t *testing.T) {
	m, _ := writeDialog(t, referenceDSL(t), true)
	mm := m.(Model)
	mm.modelApplying = true
	m, cmd := mm.Update(key("y"))
	if cmd != nil || m.(Model).status != "model apply already in progress" {
		t.Errorf("cmd=%v status=%q", cmd != nil, m.(Model).status)
	}
}

func TestOptimizer_DialogEscCancelsBench(t *testing.T) {
	m, msgs := writeDialog(t, referenceDSL(t), false)
	if !m.(Model).opt.running {
		t.Fatal("bench not running")
	}
	m, _ = m.Update(key("esc"))
	o := m.(Model)
	if o.opt.running || o.opt.dialog {
		t.Errorf("running=%v dialog=%v", o.opt.running, o.opt.dialog)
	}
	m, _ = m.Update(msgs[0])
	if m.(Model).opt.writeBench != nil || m.(Model).opt.writeNoGraph {
		t.Error("late write result landed")
	}
}

func TestOptimizer_DialogScrolls(t *testing.T) {
	m, _ := writeDialog(t, referenceDSL(t), true)
	m, _ = m.Update(key("down"))
	if m.(Model).opt.dialogScroll != 1 {
		t.Errorf("scroll=%d", m.(Model).opt.dialogScroll)
	}
	m, _ = m.Update(key("k"))
	if m.(Model).opt.dialogScroll != 0 {
		t.Errorf("scroll=%d", m.(Model).opt.dialogScroll)
	}
}

func TestOptimizer_ReopenDropsOldWriteBench(t *testing.T) {
	m, first := writeDialog(t, referenceDSL(t), false)
	m, _ = m.Update(key("esc"))
	m, second := pressW(t, m, false)
	if len(second) != 1 {
		t.Fatalf("want one write msg, got %d", len(second))
	}
	before := m.(Model).pendingLoads
	m, _ = m.Update(first[0])
	o := m.(Model)
	if o.opt.writeBench != nil || !o.opt.running {
		t.Errorf("old result landed: bench=%v running=%v", o.opt.writeBench != nil, o.opt.running)
	}
	if _, cmd := m.Update(key("y")); cmd != nil {
		t.Error("y wrote on a stale result")
	}
	m, _ = m.Update(second[0])
	if out := view(m); !strings.Contains(out, "Answers          all ") {
		t.Errorf("totals missing:\n%s", out)
	}
	if got := m.(Model).pendingLoads; got != before-2 {
		t.Errorf("pending loads unbalanced: before=%d after=%d", before, got)
	}
	if m.(Model).loading {
		t.Error("spinner still on")
	}
}

func TestOptimizer_WriteSuccessKeepsEditor(t *testing.T) {
	configtest.Isolate(t)
	m, _ := writeDialog(t, referenceDSL(t), true)
	m, _ = m.Update(key("y"))
	m, _ = m.Update(key("esc"))
	m, _ = m.Update(key("e"))
	if !m.(Model).editorOpen {
		t.Fatal("editor did not open")
	}
	m, _ = m.Update(key("Z"))
	mm := m.(Model)
	m, _ = m.Update(modelAppliedMsg{origin: mm.mutationOrigin(mm.storeID, mm.modelID, mm.modelApplyGen), modelID: "01NEWMODELIDLONGERTHANFOURTEEN", optimized: 3})
	o := m.(Model)
	if !o.editorOpen || !strings.Contains(o.editor.Value(), "Z") {
		t.Errorf("editor lost: open=%v", o.editorOpen)
	}
	if want := "wrote model " + short("01NEWMODELIDLONGERTHANFOURTEEN") + " · 3 rewrites"; o.status != want {
		t.Errorf("status=%q want %q", o.status, want)
	}
}

func TestOptimizer_WriteErrorAfterEscToasts(t *testing.T) {
	m, _ := writeDialog(t, referenceDSL(t), true)
	m, _ = m.Update(key("y"))
	m, _ = m.Update(key("esc"))
	mm := m.(Model)
	m, cmd := m.Update(modelAppliedMsg{origin: mm.mutationOrigin(mm.storeID, mm.modelID, mm.modelApplyGen), err: errors.New("boom"), optimized: 5})
	o := m.(Model)
	if o.opt.dialog || cmd == nil || o.opt.writeErr != "boom" {
		t.Errorf("dialog=%v cmd=%v writeErr=%q", o.opt.dialog, cmd != nil, o.opt.writeErr)
	}
}

const adviceOnlyDSL = "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n" +
	"    define owner: [user]\n    define banned: [user]\n    define allowed: owner but not banned\n"

func TestOptimizer_AdviceOnly(t *testing.T) {
	m := review(t, adviceOnlyDSL)
	out := view(m)
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"No safe rewrites — nothing to change", "Worth a look · 1"} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Nothing to optimize") {
		t.Errorf("unexpected Nothing to optimize:\n%s", out)
	}
	m, _ = m.Update(key("down"))
	out = view(m)
	flat = strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"doc#allowed · C2 · exclusion", "cost per user type (inf = grows"} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestOptimizer_DialogFooterOnShortTerminal(t *testing.T) {
	m, msgs := writeDialog(t, referenceDSL(t), false)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
	var ms []bench.Mismatch
	for i := range 5 {
		ms = append(ms, bench.Mismatch{Check: bench.Check{Name: fmt.Sprintf("chk-%d", i), User: "user:a", Relation: "viewer", Object: "doc:1"}})
	}
	msgs[0].result.Mismatches = ms
	m, _ = m.Update(msgs[0])
	lines := strings.Split(ansi.Strip(m.(Model).writeDialogBody()), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "esc cancel") {
		t.Errorf("footer clipped:\n%s", strings.Join(lines, "\n"))
	}
}
