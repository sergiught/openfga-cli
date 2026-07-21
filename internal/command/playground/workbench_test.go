package playground

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

func TestSeededModelHoldsWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ws, err := modeltest.LoadWorkspace("../../modeltest/testdata/docs")
	if err != nil {
		t.Fatal(err)
	}

	cl, _ := openfga.NewClient("http://127.0.0.1:1")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newSeededModel(context.Background(), a, SeedOptions{
		Client:    cl,
		StoreID:   "s",
		ModelID:   "mdl",
		Endpoint:  "http://127.0.0.1:1",
		Workspace: ws,
	})

	if m.wb.workspace == nil {
		t.Fatal("workspace not stored")
	}
	if len(m.wb.files) != len(ws.TestFiles) {
		t.Fatalf("want %d files, got %d", len(ws.TestFiles), len(m.wb.files))
	}
}

// TestWorkbenchListsFilesWithStatus drives the Tests section's file-list view
// (the docs workspace has 1 test file, "documents", with 2 tests) and checks
// it shows the file's stem alongside its last-run pass count.
func TestWorkbenchListsFilesWithStatus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ws, err := modeltest.LoadWorkspace("../../modeltest/testdata/docs")
	if err != nil {
		t.Fatal(err)
	}

	m := newTestModel().(Model)
	m.wb.workspace = ws
	m.wb.files = ws.TestFiles
	m.wb.results = []modeltest.TestResult{
		{Name: "documents/owner-is-viewer", Passed: true},
		{Name: "documents/stranger-denied", Passed: true},
	}
	m.section = secTestResults

	body := stripANSIView(m.sectionBody())
	if !strings.Contains(body, "documents") {
		t.Fatalf("body should list the test file's stem 'documents'; got:\n%s", body)
	}
	if !strings.Contains(body, "2/2") {
		t.Fatalf("body should show the file's 2/2 pass status; got:\n%s", body)
	}
}

// TestWorkbenchFilesEmptyWorkspace covers the friendly empty state when the
// section has no workspace to show (e.g. an ordinary, non-seeded playground
// session).
func TestWorkbenchFilesEmptyWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := newTestModel().(Model)
	m.section = secTestResults

	body := stripANSIView(m.sectionBody())
	if strings.TrimSpace(body) == "" {
		t.Fatal("an empty workspace should render a friendly message, not blank")
	}
}

// TestWorkbenchFilesNotRunYet covers a loaded workspace with no results yet
// (never run this session): the file list should still render, with a
// "press r to run" hint rather than a bogus 0/0 status.
func TestWorkbenchFilesNotRunYet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ws, err := modeltest.LoadWorkspace("../../modeltest/testdata/docs")
	if err != nil {
		t.Fatal(err)
	}

	m := newTestModel().(Model)
	m.wb.workspace = ws
	m.wb.files = ws.TestFiles
	m.section = secTestResults

	body := stripANSIView(m.sectionBody())
	if !strings.Contains(body, "documents") {
		t.Fatalf("body should still list the file; got:\n%s", body)
	}
	if !strings.Contains(body, "press r to run") {
		t.Fatalf("body should hint that the workspace hasn't been run yet; got:\n%s", body)
	}
}

// TestSeededModelOpensOnFirstFailure covers newSeededModel's Tests-section
// entry point: with a failure present, the navigator's cursor should land on
// the failing test's node (not just the first file). Uses a 2-file workspace
// where the failure lives in the second file, so a fix that landed on the
// wrong node would fail this.
func TestSeededModelOpensOnFirstFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ws := &modeltest.Workspace{
		TestFiles: []*modeltest.TestFile{
			{Path: "alpha.test.yaml", Tests: []modeltest.Test{{Name: "one"}}},
			{Path: "beta.test.yaml", Tests: []modeltest.Test{{Name: "two"}}},
		},
	}
	results := []modeltest.TestResult{
		{Name: "alpha/one", Passed: true},
		{Name: "beta/two", Passed: false},
	}

	cl, _ := openfga.NewClient("http://127.0.0.1:1")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newSeededModel(context.Background(), a, SeedOptions{
		Client:    cl,
		StoreID:   "s",
		ModelID:   "mdl",
		Endpoint:  "http://127.0.0.1:1",
		Workspace: ws,
		Results:   results,
	})

	node, ok := m.wbSelectedNode()
	if !ok {
		t.Fatal("seeded model should have a selected node")
	}
	if node.kind != wbNodeTest {
		t.Fatalf("want the cursor on a test node, got kind %v", node.kind)
	}
	tf, _, _ := m.wbSelectedFile()
	tests := m.wbFileTests(tf)
	if got := tests[node.testIdx]; got.Name != "beta/two" || got.Passed {
		t.Fatalf("want the failing beta/two test selected, got %+v", got)
	}
}

// TestSeededModelOpensOnFileListWhenAllPass covers newSeededModel's Tests-
// section entry point when the whole suite passed: the cursor should rest on
// the first node (the first file) rather than any failure.
func TestSeededModelOpensOnFileListWhenAllPass(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ws := &modeltest.Workspace{
		TestFiles: []*modeltest.TestFile{
			{Path: "alpha.test.yaml", Tests: []modeltest.Test{{Name: "one"}}},
		},
	}
	results := []modeltest.TestResult{
		{Name: "alpha/one", Passed: true},
	}

	cl, _ := openfga.NewClient("http://127.0.0.1:1")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newSeededModel(context.Background(), a, SeedOptions{
		Client:    cl,
		StoreID:   "s",
		ModelID:   "mdl",
		Endpoint:  "http://127.0.0.1:1",
		Workspace: ws,
		Results:   results,
	})

	if m.wb.treeSel != 0 {
		t.Fatalf("want wbTreeSel 0 when the suite passed, got %d", m.wb.treeSel)
	}
	node, ok := m.wbSelectedNode()
	if !ok || node.kind != wbNodeFile || node.fileIdx != 0 {
		t.Fatalf("want the cursor on the first file node, got %+v (ok=%v)", node, ok)
	}
}

// seedTreeModel returns a Tests-section model over the docs workspace with one
// passing and one failing result, panel-focused — the common fixture for the
// navigator-tree tests.
func seedTreeModel(t *testing.T) Model {
	t.Helper()
	// Isolate config so constructing the model can never clobber the user's real
	// config.toml (the documented repo gotcha) — enforced here so no caller can
	// forget it.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ws, err := modeltest.LoadWorkspace("../../modeltest/testdata/docs")
	if err != nil {
		t.Fatal(err)
	}
	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.wb.results = []modeltest.TestResult{
		{Name: "documents/owner-is-viewer", Passed: true},
		{Name: "documents/stranger-denied", Passed: false},
	}
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	return mod
}

// TestWorkbenchTreeShowsFilesAndTests asserts the navigator renders the
// tests-dir header, the test file, AND its tests nested underneath — all
// visible at once, not behind a drill-down.
func TestWorkbenchTreeShowsFilesAndTests(t *testing.T) {
	m := seedTreeModel(t)
	body := stripANSIView(m.sectionBody())
	if !strings.Contains(body, "tests/") {
		t.Fatalf("body should show the tests-dir header; got:\n%s", body)
	}
	if !strings.Contains(body, "documents.test.yaml") {
		t.Fatalf("body should list the test file; got:\n%s", body)
	}
	if !strings.Contains(body, "owner-is-viewer") || !strings.Contains(body, "stranger-denied") {
		t.Fatalf("body should nest the file's tests underneath it; got:\n%s", body)
	}
}

// TestWorkbenchTreeNav moves the cursor onto the failing test node and
// asserts the tree renders full-width, with no detail pane alongside it.
func TestWorkbenchTreeNav(t *testing.T) {
	mod := seedTreeModel(t)

	// Nodes: [file, test owner-is-viewer, test stranger-denied] -> down twice
	// lands on the failing test.
	var tm tea.Model = mod
	tm, _ = tm.Update(key("down"))
	tm, _ = tm.Update(key("down"))
	m := tm.(Model)
	node, ok := m.wbSelectedNode()
	if !ok || node.kind != wbNodeTest {
		t.Fatalf("cursor should be on a test node, got %+v (ok=%v)", node, ok)
	}
	body := stripANSIView(m.sectionBody())
	if !strings.Contains(body, "stranger-denied") {
		t.Fatalf("body should still list the selected test; got:\n%s", body)
	}
	w, _ := m.contentSize()
	for _, line := range strings.Split(body, "\n") {
		if len([]rune(line)) > w {
			t.Fatalf("tree line exceeds content width %d, got %d: %q", w, len([]rune(line)), line)
		}
	}
}

// TestWorkbenchVerboseToggleShowsExplanation asserts "v" toggles a detail
// pane alongside the tree, showing the selected failing test's explanation,
// and that pressing "v" again hides it again (tree-only).
func TestWorkbenchVerboseToggleShowsExplanation(t *testing.T) {
	mod := seedTreeModel(t)
	// Give the failing result an explain tree so the detail pane has content.
	mod.wb.results[1].Assertions = []modeltest.AssertionResult{{
		Kind: "check", Subject: "user:eve viewer document:roadmap", Passed: false,
		Explain: &modeltest.Explain{
			Verdict:     false,
			Tree:        &modeltest.ExplainNode{Label: "document:roadmap#viewer", Result: false},
			NearestMiss: "add user:eve owner document:roadmap",
		},
	}}

	// Nodes: [file, test owner-is-viewer, test stranger-denied] -> down twice
	// lands on the failing test.
	var tm tea.Model = mod
	tm, _ = tm.Update(key("down"))
	tm, _ = tm.Update(key("down"))
	m := tm.(Model)
	body := stripANSIView(m.sectionBody())
	if strings.Contains(body, "nearest miss") {
		t.Fatalf("explanation should be hidden by default; got:\n%s", body)
	}

	tm, _ = m.Update(key("v"))
	m = tm.(Model)
	if !m.wb.verbose {
		t.Fatalf("v should turn wbVerbose on")
	}
	body = stripANSIView(m.sectionBody())
	if !strings.Contains(body, "nearest miss") {
		t.Fatalf("body should render the failed assertion's nearest-miss explanation; got:\n%s", body)
	}
	if !strings.Contains(body, "document:roadmap#viewer") {
		t.Fatalf("body should render the failed assertion's resolution tree; got:\n%s", body)
	}
	treeIdx := strings.Index(body, "stranger-denied") // the selected test's row in the tree
	explainIdx := strings.Index(body, "nearest miss")
	if treeIdx < 0 || explainIdx < 0 || treeIdx >= explainIdx {
		t.Fatalf("explanation should render BELOW the tree (tree at %d, explanation at %d); got:\n%s", treeIdx, explainIdx, body)
	}

	tm, _ = m.Update(key("v"))
	m = tm.(Model)
	if m.wb.verbose {
		t.Fatalf("v should turn wbVerbose back off")
	}
	body = stripANSIView(m.sectionBody())
	if strings.Contains(body, "nearest miss") {
		t.Fatalf("explanation should be hidden after toggling v again; got:\n%s", body)
	}
}

// TestWorkbenchTreeCollapse drives enter/space on a file node: it hides the
// file's tests, then shows them again.
func TestWorkbenchTreeCollapse(t *testing.T) {
	mod := seedTreeModel(t) // cursor starts on the file node (index 0)

	var tm tea.Model = mod
	tm, _ = tm.Update(key("enter"))
	m := tm.(Model)
	body := stripANSIView(m.sectionBody())
	if strings.Contains(body, "owner-is-viewer") {
		t.Fatalf("collapsing the file should hide its tests; got:\n%s", body)
	}
	if len(m.wbVisibleNodes()) != 1 {
		t.Fatalf("collapsed file should leave only the file node visible, got %d", len(m.wbVisibleNodes()))
	}

	tm, _ = tm.Update(key(" ")) // space re-expands
	m = tm.(Model)
	body = stripANSIView(m.sectionBody())
	if !strings.Contains(body, "owner-is-viewer") {
		t.Fatalf("re-expanding the file should show its tests again; got:\n%s", body)
	}
}

// TestWorkbenchEnterOnTestTogglesExplanation covers "enter"/"space" drilling
// into a selected TEST node: it should toggle wbVerbose (the same thing "v"
// does) rather than no-op, while the same keys on a FILE node keep collapsing
// it instead of touching wbVerbose.
func TestWorkbenchEnterOnTestTogglesExplanation(t *testing.T) {
	mod := seedTreeModel(t)
	// Give the failing result an explain tree so the detail pane has content.
	mod.wb.results[1].Assertions = []modeltest.AssertionResult{{
		Kind: "check", Subject: "user:eve viewer document:roadmap", Passed: false,
		Explain: &modeltest.Explain{
			Verdict:     false,
			Tree:        &modeltest.ExplainNode{Label: "document:roadmap#viewer", Result: false},
			NearestMiss: "add user:eve owner document:roadmap",
		},
	}}

	// Nodes: [file, test owner-is-viewer, test stranger-denied] -> down twice
	// lands on the failing test.
	var tm tea.Model = mod
	tm, _ = tm.Update(key("down"))
	tm, _ = tm.Update(key("down"))
	m := tm.(Model)
	if node, ok := m.wbSelectedNode(); !ok || node.kind != wbNodeTest {
		t.Fatalf("cursor should be on a test node, got %+v (ok=%v)", node, ok)
	}

	tm, _ = m.Update(key("enter"))
	m = tm.(Model)
	if !m.wb.verbose {
		t.Fatal("enter on a test node should turn wbVerbose on")
	}
	body := stripANSIView(m.sectionBody())
	if !strings.Contains(body, "nearest miss") {
		t.Fatalf("body should render the explanation after enter; got:\n%s", body)
	}

	tm, _ = m.Update(key("enter"))
	m = tm.(Model)
	if m.wb.verbose {
		t.Fatal("enter again on a test node should turn wbVerbose back off")
	}
	body = stripANSIView(m.sectionBody())
	if strings.Contains(body, "nearest miss") {
		t.Fatalf("explanation should be hidden after toggling enter again; got:\n%s", body)
	}

	// Sanity: the same keys on a FILE node still collapse/expand, not verbose.
	fileModel := seedTreeModel(t) // cursor starts on the file node (index 0)
	var ftm tea.Model = fileModel
	ftm, _ = ftm.Update(key("enter"))
	fm := ftm.(Model)
	if fm.wb.verbose {
		t.Fatal("enter on a file node must not touch wbVerbose")
	}
	fbody := stripANSIView(fm.sectionBody())
	if strings.Contains(fbody, "owner-is-viewer") {
		t.Fatalf("enter on a file node should collapse its tests; got:\n%s", fbody)
	}
}

// TestWorkbenchTreeTestNodeTargetsOwningFile covers R/e/d acting on the FILE
// that owns the selected node: with a test node selected, they must operate on
// its owning file, not no-op.
func TestWorkbenchTreeTestNodeTargetsOwningFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("EDITOR", "true") // so "e" resolves an editor and launches ExecProcess

	root := copyDocsWorkspace(t)
	ws, err := modeltest.LoadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.wb.results = []modeltest.TestResult{
		{Name: "documents/owner-is-viewer", Passed: true},
		{Name: "documents/stranger-denied", Passed: false},
	}
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	filePath := ws.TestFiles[0].Path

	// Move onto a test node (index 1), then confirm each file action targets
	// the owning file.
	var tm tea.Model = mod
	tm, _ = tm.Update(key("down"))
	if node, ok := tm.(Model).wbSelectedNode(); !ok || node.kind != wbNodeTest {
		t.Fatalf("cursor should be on a test node, got %+v (ok=%v)", node, ok)
	}

	// e: launches $EDITOR on the owning file (via ExecProcess) — a non-nil cmd.
	_, ecmd := tm.Update(key("e"))
	if ecmd == nil {
		t.Fatal("e on a test node should launch the editor on the owning file")
	}

	// R: runs a filter scoped to the owning file's stem.
	rm, cmd := tm.Update(key("R"))
	if !rm.(Model).wb.running || cmd == nil {
		t.Fatal("R on a test node should kick off a scoped run of the owning file")
	}

	// d: opens a delete confirmation naming the owning file.
	dm, _ := tm.Update(key("d"))
	c := dm.(Model).confirm
	if c == nil || c.subject != filepath.Base(filePath) {
		t.Fatalf("d on a test node should confirm deleting the owning file, got %+v", c)
	}
}

// TestWorkbenchRunPopulatesResults drives the "r" key: it should kick off a
// hermetic run against a fresh embedded engine (never the seeded/live
// server) and, once the resulting command's message is fed back in, populate
// m.wb.results from it. The docs workspace has 1 file with 2 tests, both
// passing.
func TestWorkbenchRunPopulatesResults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ws, err := modeltest.LoadWorkspace("../../modeltest/testdata/docs")
	if err != nil {
		t.Fatal(err)
	}

	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.section = secTestResults
	mod.focus = shell.FocusPanel

	var tm tea.Model = mod
	tm, cmd := tm.Update(key("r"))
	m := tm.(Model)
	if !m.wb.running {
		t.Fatal("pressing r should set testsRunning before the run completes")
	}
	if cmd == nil {
		t.Fatal("pressing r should dispatch a run command")
	}

	msg := cmd()
	ranMsg, ok := msg.(testsRanMsg)
	if !ok {
		t.Fatalf("want testsRanMsg, got %#v", msg)
	}
	if ranMsg.err != nil {
		t.Fatalf("run failed: %v", ranMsg.err)
	}

	tm, _ = tm.Update(ranMsg)
	m = tm.(Model)
	if m.wb.running {
		t.Fatal("testsRunning should clear once the run's result lands")
	}
	if len(m.wb.results) != 2 {
		t.Fatalf("want 2 test results, got %d: %+v", len(m.wb.results), m.wb.results)
	}
	for _, r := range m.wb.results {
		if !r.Passed {
			t.Fatalf("want all tests to pass, got %+v", r)
		}
	}
}

// TestWorkbenchRunFileFiltersToSelectedFile drives "R": it should run only
// the currently selected file, via a filter derived from its stem.
func TestWorkbenchRunFileFiltersToSelectedFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ws, err := modeltest.LoadWorkspace("../../modeltest/testdata/docs")
	if err != nil {
		t.Fatal(err)
	}

	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.wb.treeSel = 0
	mod.section = secTestResults
	mod.focus = shell.FocusPanel

	var tm tea.Model = mod
	tm, cmd := tm.Update(key("R"))
	m := tm.(Model)
	if !m.wb.running {
		t.Fatal("pressing R should set testsRunning before the run completes")
	}
	if cmd == nil {
		t.Fatal("pressing R should dispatch a run command")
	}

	msg := cmd()
	ranMsg, ok := msg.(testsRanMsg)
	if !ok {
		t.Fatalf("want testsRanMsg, got %#v", msg)
	}
	if ranMsg.err != nil {
		t.Fatalf("run failed: %v", ranMsg.err)
	}
	if len(ranMsg.results.Tests) != 2 {
		t.Fatalf("want 2 test results for the documents file, got %d", len(ranMsg.results.Tests))
	}
}

// TestWorkbenchCoverageToggle drives "r" (populating m.wb.coverage via
// testsRanMsg) then "c": the section body should show the coverage report
// (a type name plus a percent), and pressing "c" again should hide it.
func TestWorkbenchCoverageToggle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ws, err := modeltest.LoadWorkspace("../../modeltest/testdata/docs")
	if err != nil {
		t.Fatal(err)
	}

	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.section = secTestResults
	mod.focus = shell.FocusPanel

	var tm tea.Model = mod
	tm, cmd := tm.Update(key("r"))
	if cmd == nil {
		t.Fatal("pressing r should dispatch a run command")
	}
	msg := cmd()
	ranMsg, ok := msg.(testsRanMsg)
	if !ok {
		t.Fatalf("want testsRanMsg, got %#v", msg)
	}
	if ranMsg.err != nil {
		t.Fatalf("run failed: %v", ranMsg.err)
	}
	tm, _ = tm.Update(ranMsg)
	m := tm.(Model)
	if m.wb.coverage == nil {
		t.Fatal("lastCoverage should be populated after a run")
	}

	tm, _ = tm.Update(key("c"))
	m = tm.(Model)
	if !m.wb.showCoverage {
		t.Fatal("pressing c should toggle wbShowCoverage on")
	}
	body := stripANSIView(m.sectionBody())
	if !strings.Contains(body, "document") {
		t.Fatalf("coverage body should mention the 'document' type; got:\n%s", body)
	}
	if !strings.Contains(body, "%") {
		t.Fatalf("coverage body should show a percent; got:\n%s", body)
	}

	tm, _ = tm.Update(key("c"))
	m = tm.(Model)
	if m.wb.showCoverage {
		t.Fatal("pressing c again should toggle wbShowCoverage off")
	}
	body = stripANSIView(m.sectionBody())
	if strings.Contains(body, "coverage:") {
		t.Fatalf("coverage should be hidden after the second toggle; got:\n%s", body)
	}
}

// TestWorkbenchCoverageBeforeRunHint covers pressing "c" before any run this
// session: it must not crash, and must not turn coverage on since there is
// nothing to show yet.
func TestWorkbenchCoverageBeforeRunHint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ws, err := modeltest.LoadWorkspace("../../modeltest/testdata/docs")
	if err != nil {
		t.Fatal(err)
	}

	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.section = secTestResults
	mod.focus = shell.FocusPanel

	var tm tea.Model = mod
	tm, _ = tm.Update(key("c"))
	m := tm.(Model)
	if m.wb.showCoverage {
		t.Fatal("c before any run should not turn coverage on")
	}
	body := stripANSIView(m.sectionBody())
	if strings.TrimSpace(body) == "" {
		t.Fatal("body should still render (the file list), not blank")
	}
}

// TestWorkbenchNewEmptyNameToast covers the new-file prompt's "name required"
// dead-end: submitting a blank name must surface a visible toast (not merely set
// m.status, which the Tests section's footer never renders), so the user sees
// why the prompt didn't advance.
func TestWorkbenchNewEmptyNameToast(t *testing.T) {
	t.Setenv("EDITOR", "true")

	mod := seedWorkbenchModel(t, copyDocsWorkspace(t))

	var tm tea.Model = mod
	tm, _ = tm.Update(key("n")) // open the prompt
	tm, cmd := tm.Update(key("enter"))
	m := tm.(Model)

	if cmd == nil {
		t.Fatal("submitting a blank name should return a toast command, not nil")
	}
	if !m.toasts.Active() {
		t.Fatal("a blank filename should raise a visible toast, not just set m.status")
	}
	if view := stripANSIView(m.toasts.View()); !strings.Contains(view, "filename cannot be empty") {
		t.Fatalf("toast should explain the empty name; got:\n%s", view)
	}
}

// TestWorkbenchCoverageBeforeRunToast covers pressing "c" before any run: the
// coverage-unavailable hint must ride on a visible toast, not just m.status.
func TestWorkbenchCoverageBeforeRunToast(t *testing.T) {
	mod := seedWorkbenchModel(t, copyDocsWorkspace(t))

	var tm tea.Model = mod
	tm, cmd := tm.Update(key("c"))
	m := tm.(Model)

	if cmd == nil {
		t.Fatal("pressing c before a run should return a toast command, not nil")
	}
	if !m.toasts.Active() {
		t.Fatal("the coverage-before-run hint should raise a visible toast")
	}
	if view := stripANSIView(m.toasts.View()); !strings.Contains(view, "run tests first") {
		t.Fatalf("toast should hint to run tests first; got:\n%s", view)
	}
}

// TestWorkbenchMutatingKeysGatedWhileRunning covers fix: "e", "n" and "d" must
// not act while a suite run is in flight (which would race the async result and
// repaint stale state). Each should no-op the mutation and surface a "tests
// running" toast instead.
func TestWorkbenchMutatingKeysGatedWhileRunning(t *testing.T) {
	t.Setenv("EDITOR", "true")

	for _, k := range []string{"e", "n", "d"} {
		mod := seedWorkbenchModel(t, copyDocsWorkspace(t))
		mod.wb.running = true

		var tm tea.Model = mod
		tm, _ = tm.Update(key(k))
		m := tm.(Model)

		if m.wb.newPromptOpen {
			t.Fatalf("%q must not open the new-file prompt while running", k)
		}
		if m.confirm != nil {
			t.Fatalf("%q must not open a confirmation while running", k)
		}
		if !m.toasts.Active() {
			t.Fatalf("%q while running should raise a 'tests running' toast", k)
		}
	}
}

// TestWorkbenchDetailCardTruncates covers fix: a long RenderExplain narrative in
// the verbose detail pane is capped with a "⋯ more" hint (like the coverage
// view) rather than silently clipped. Builds a failing test with many failed
// assertions so the card overflows its share of the pane.
func TestWorkbenchDetailCardTruncates(t *testing.T) {
	mod := seedTreeModel(t)
	var many []modeltest.AssertionResult
	for i := 0; i < 15; i++ {
		many = append(many, modeltest.AssertionResult{
			Kind: "check", Subject: "user:eve viewer document:roadmap", Passed: false,
			Explain: &modeltest.Explain{
				Verdict:     false,
				Tree:        &modeltest.ExplainNode{Label: "document:roadmap#viewer", Result: false},
				NearestMiss: "add user:eve owner document:roadmap",
			},
		})
	}
	mod.wb.results[1].Assertions = many

	// Nodes: [file, owner-is-viewer, stranger-denied] -> down twice to the failing
	// test, then "v" to reveal its detail pane.
	var tm tea.Model = mod
	tm, _ = tm.Update(key("down"))
	tm, _ = tm.Update(key("down"))
	tm, _ = tm.Update(key("v"))
	m := tm.(Model)

	body := stripANSIView(m.sectionBody())
	if !strings.Contains(body, "⋯ more") {
		t.Fatalf("an overflowing detail card should show the '⋯ more' hint; got:\n%s", body)
	}
}

// writeMultiModelWorkspaceDir writes a workspace whose two test files override
// `model:` to two distinct models (so --coverage has no single model to
// enumerate against) and returns its directory. Both tests pass.
func writeMultiModelWorkspaceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	modelA := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model-a.fga"), []byte(modelA), 0o600); err != nil {
		t.Fatal(err)
	}
	modelB := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define editor: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model-b.fga"), []byte(modelB), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\nmodel: ./model-a.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	testA := "model: ../model-a.fga\ntests:\n  - name: a-test\n    tuples:\n      - user: user:anne\n        relation: viewer\n        object: document:1\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: true}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "a.test.yaml"), []byte(testA), 0o600); err != nil {
		t.Fatal(err)
	}
	testB := "model: ../model-b.fga\ntests:\n  - name: b-test\n    tuples:\n      - user: user:bob\n        relation: editor\n        object: document:1\n    check:\n      - user: user:bob\n        object: document:1\n        assertions: {editor: true}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "b.test.yaml"), []byte(testB), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

// TestWorkbenchRunMultiModelSucceedsWithoutCoverage covers the regression fix
// from the TUI's side: a multi-model workspace can't produce a coverage report,
// but the run must still succeed (results populated, not "run failed"), leave
// lastCoverage nil, and — on "c" — surface the coverage-unavailable reason
// rather than the generic "run first" hint.
func TestWorkbenchRunMultiModelSucceedsWithoutCoverage(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ws, err := modeltest.LoadWorkspace(writeMultiModelWorkspaceDir(t))
	if err != nil {
		t.Fatal(err)
	}

	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.section = secTestResults
	mod.focus = shell.FocusPanel

	var tm tea.Model = mod
	tm, cmd := tm.Update(key("r"))
	if cmd == nil {
		t.Fatal("pressing r should dispatch a run command")
	}
	msg := cmd()
	ranMsg, ok := msg.(testsRanMsg)
	if !ok {
		t.Fatalf("want testsRanMsg, got %#v", msg)
	}
	if ranMsg.err != nil {
		t.Fatalf("run should not fail for a multi-model workspace, got: %v", ranMsg.err)
	}
	if ranMsg.results.CoverageError == "" {
		t.Fatal("want a CoverageError on the results for a multi-model workspace")
	}

	tm, _ = tm.Update(ranMsg)
	m := tm.(Model)
	if len(m.wb.results) != 2 {
		t.Fatalf("want 2 test results (run succeeded), got %d", len(m.wb.results))
	}
	if m.wb.coverage != nil {
		t.Fatalf("want nil lastCoverage for a multi-model workspace, got %+v", m.wb.coverage)
	}

	tm, _ = tm.Update(key("c"))
	m = tm.(Model)
	if m.wb.showCoverage {
		t.Fatal("c should not turn coverage on when there is no coverage")
	}
	if !strings.Contains(m.status, "coverage unavailable") {
		t.Fatalf("want a coverage-unavailable hint, got status: %q", m.status)
	}
}

// TestWorkbenchRunBareTestFileDoesNotPanic covers a workspace loaded from a
// bare *.test.yaml file with no ofga.yaml anywhere above it, which yields a
// Workspace with a nil Manifest. runSuiteCmd must guard that nil deref
// rather than panic when the run is kicked off from the workbench.
func TestWorkbenchRunBareTestFileDoesNotPanic(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dir := t.TempDir()
	bare := "fixtures: [core-users]\ntests:\n  - name: owner-is-viewer\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: true, owner: true}\n  - name: stranger-denied\n    check:\n      - user: user:bob\n        object: document:1\n        assertions: {viewer: false}\n"
	testFile := filepath.Join(dir, "documents.test.yaml")
	if err := os.WriteFile(testFile, []byte(bare), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := modeltest.LoadWorkspace(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Manifest != nil {
		t.Fatal("expected bare test file workspace to have a nil Manifest")
	}

	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.section = secTestResults
	mod.focus = shell.FocusPanel

	var tm tea.Model = mod
	tm, cmd := tm.Update(key("r"))
	m := tm.(Model)
	if !m.wb.running {
		t.Fatal("pressing r should set testsRunning before the run completes")
	}
	if cmd == nil {
		t.Fatal("pressing r should dispatch a run command")
	}

	msg := cmd()

	tm, _ = tm.Update(msg)
	m = tm.(Model)
	if m.wb.running {
		t.Fatal("testsRunning should clear once the run's result lands")
	}
}

// copyDocsWorkspace copies the docs testdata workspace into a fresh temp dir so
// tests that write to a test file don't mutate the repo's fixtures. It returns
// the temp workspace root.
func copyDocsWorkspace(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := "../../modeltest/testdata/docs"
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// seedWorkbenchModel loads root into a fresh model, in the Tests section with
// panel focus and the first file selected — the common starting point for the
// edit/new flows exercised below.
func seedWorkbenchModel(t *testing.T, root string) Model {
	t.Helper()
	// Isolate config so constructing the model can never clobber the user's real
	// config.toml (the documented repo gotcha) — enforced here so no caller can
	// forget it.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ws, err := modeltest.LoadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = 0
	return mod
}

// TestEditorCommandResolvesEnv checks the $VISUAL/$EDITOR resolution and the
// fallback path. It stays robust to CI by asserting the env path directly and
// only asserting that the fallback doesn't panic (it returns whatever is on
// PATH, or "" when nothing suitable is found).
func TestEditorCommandResolvesEnv(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myed --wait")
	bin, args := editorCommand()
	if bin != "myed" {
		t.Fatalf("EDITOR should resolve to myed, got %q", bin)
	}
	if len(args) != 1 || args[0] != "--wait" {
		t.Fatalf("editor args should be [--wait], got %v", args)
	}

	t.Setenv("VISUAL", "vised")
	if bin, _ := editorCommand(); bin != "vised" {
		t.Fatalf("VISUAL should take precedence, got %q", bin)
	}

	// Neither env set: fallback to a PATH lookup. Just assert it doesn't panic
	// and returns a string (possibly "" on a bare CI box with no editor).
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	_, _ = editorCommand()
}

// TestWorkbenchEditLaunchesEditor presses "e" on the selected file and asserts
// it returns a command (the ExecProcess that runs $EDITOR) rather than opening
// an in-TUI editor.
func TestWorkbenchEditLaunchesEditor(t *testing.T) {
	t.Setenv("EDITOR", "true")

	mod := seedWorkbenchModel(t, copyDocsWorkspace(t))
	var tm tea.Model = mod
	tm, cmd := tm.Update(key("e"))
	if cmd == nil {
		t.Fatal("e should launch the editor (return an ExecProcess command)")
	}
}

// TestWorkbenchEditNoEditorHints covers the no-editor case: with $VISUAL and
// $EDITOR empty and no fallback binary on PATH, "e" should surface a status
// hint instead of launching anything.
func TestWorkbenchEditNoEditorHints(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	t.Setenv("PATH", "") // no vim/vi/nano reachable

	mod := seedWorkbenchModel(t, copyDocsWorkspace(t))
	var tm tea.Model = mod
	tm, _ = tm.Update(key("e"))
	m := tm.(Model)
	// The no-editor branch surfaces a toast rather than launching an ExecProcess;
	// the "no editor" status/toast proves the launch path wasn't taken.
	if !strings.Contains(m.status, "no editor") {
		t.Fatalf("want a no-editor status hint, got %q", m.status)
	}
	if !m.toasts.Active() {
		t.Fatal("the no-editor case should raise a visible toast")
	}
}

// TestWorkbenchEditedMsgValidReloadsAndReruns simulates the editor writing new
// valid content, then feeds testFileEditedMsg{path, nil}: the workspace should
// reload (file still present in m.wb.files) and a re-run should be triggered.
func TestWorkbenchEditedMsgValidReloadsAndReruns(t *testing.T) {
	root := copyDocsWorkspace(t)
	mod := seedWorkbenchModel(t, root)
	path := mod.wb.files[0].Path

	newContent := "tests:\n  - name: renamed-test\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: true}\n"
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var tm tea.Model = mod
	tm, cmd := tm.Update(testFileEditedMsg{path: path})
	m := tm.(Model)

	if cmd == nil || !m.wb.running {
		t.Fatal("a valid edit should trigger a re-run (cmd + testsRunning)")
	}
	found := false
	for _, tf := range m.wb.files {
		if tf.Path == path {
			found = true
		}
	}
	if !found {
		t.Fatalf("edited file should still appear in m.wb.files after reload; got %+v", m.wb.files)
	}
}

// TestWorkbenchEditedMsgInvalidWarnsNoBlock writes INVALID content to the file,
// then feeds the edited msg: the model must warn (status mentions "saved with
// errors") without panicking, and still attempt the reload/re-run.
func TestWorkbenchEditedMsgInvalidWarnsNoBlock(t *testing.T) {
	root := copyDocsWorkspace(t)
	mod := seedWorkbenchModel(t, root)
	path := mod.wb.files[0].Path

	if err := os.WriteFile(path, []byte("tests: [\n"), 0o644); err != nil { // invalid YAML
		t.Fatal(err)
	}

	var tm tea.Model = mod
	tm, _ = tm.Update(testFileEditedMsg{path: path})
	m := tm.(Model)

	if !strings.Contains(m.status, "saved with errors") {
		t.Fatalf("an invalid edit should warn 'saved with errors', got %q", m.status)
	}
}

// typeText feeds each rune of s into the model as a separate key press,
// mirroring how the confirm-modal's typed-input tests drive freeform text
// entry.
func typeText(t *testing.T, tm tea.Model, s string) tea.Model {
	t.Helper()
	for _, r := range s {
		tm, _ = tm.Update(key(string(r)))
	}
	return tm
}

// TestWorkbenchNewWritesScaffoldThenEdits drives "n" from the Tests file list,
// types a filename, and presses enter: the scaffold file should be written to
// disk immediately (so $EDITOR has a valid file to open) with the wbNewTemplate
// content, and submit should return a command (the ExecProcess launch).
func TestWorkbenchNewWritesScaffoldThenEdits(t *testing.T) {
	t.Setenv("EDITOR", "true")

	root := copyDocsWorkspace(t)
	mod := seedWorkbenchModel(t, root)

	var tm tea.Model = mod
	tm, _ = tm.Update(key("n"))
	m := tm.(Model)
	if !m.wb.newPromptOpen {
		t.Fatal("n should open the new-file filename prompt")
	}

	tm = typeText(t, tm, "my-new")
	tm, cmd := tm.Update(key("enter"))
	m = tm.(Model)

	if m.wb.newPromptOpen {
		t.Fatal("submitting the prompt should close it")
	}
	if cmd == nil {
		t.Fatal("submitting the prompt should launch the editor (return a command)")
	}
	path := filepath.Join(root, "tests", "my-new.test.yaml")
	on, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("scaffold file should exist on disk before editing: %v", err)
	}
	if string(on) != wbNewTemplate {
		t.Fatalf("scaffold on disk should equal the template;\nwant:\n%s\ngot:\n%s", wbNewTemplate, on)
	}
}

// TestWorkbenchNewRejectsExisting covers typing the name of a test file that
// already exists: no editor is launched, and the prompt stays up with an error
// status.
func TestWorkbenchNewRejectsExisting(t *testing.T) {
	t.Setenv("EDITOR", "true")

	root := copyDocsWorkspace(t)
	mod := seedWorkbenchModel(t, root)

	var tm tea.Model = mod
	tm, _ = tm.Update(key("n"))
	tm = typeText(t, tm, "documents")
	tm, _ = tm.Update(key("enter"))
	m := tm.(Model)

	// On rejection the prompt stays open (launch would close it), and the reason
	// rides on a visible toast.
	if !m.wb.newPromptOpen {
		t.Fatal("an existing filename must not launch the editor (prompt should stay open)")
	}
	if !strings.Contains(m.status, "already exists") {
		t.Fatalf("want an 'already exists' status, got %q", m.status)
	}
	if !m.toasts.Active() {
		t.Fatal("the 'already exists' rejection should raise a visible toast")
	}
}

// TestWorkbenchNewRejectsTraversal covers a filename that would escape the
// workspace root: no editor is launched, no file is written, and an error
// status is shown.
func TestWorkbenchNewRejectsTraversal(t *testing.T) {
	t.Setenv("EDITOR", "true")

	root := copyDocsWorkspace(t)
	mod := seedWorkbenchModel(t, root)

	var tm tea.Model = mod
	tm, _ = tm.Update(key("n"))
	tm = typeText(t, tm, "../../escape")
	tm, _ = tm.Update(key("enter"))
	m := tm.(Model)

	// On rejection the prompt stays open (launch would close it).
	if !m.wb.newPromptOpen {
		t.Fatal("a traversal filename must not launch the editor (prompt should stay open)")
	}
	if m.status == "" {
		t.Fatal("want an error status for a traversal filename")
	}
	if _, err := os.Stat(filepath.Join(root, "..", "escape.test.yaml")); err == nil {
		t.Fatal("a traversal filename must not write any file")
	}
}

// TestWorkbenchEditPathTraversalBlocked is a direct table-driven unit test of
// withinRoot, the path-safety guard before creating/deleting a workbench test
// file: a relative ".." escape, a deeper multi-level relative escape, an
// absolute path outside the root, and a sibling directory that merely shares
// the root's name as a string prefix (e.g. "/root" vs "/rootX") must all be
// rejected, while a path nested inside the root is accepted. The
// sibling-prefix case in particular guards against a naive
// strings.HasPrefix(path, root) implementation, which would wrongly accept
// it; filepath.Rel-based comparison must not.
func TestWorkbenchEditPathTraversalBlocked(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	inside := filepath.Join(root, "tests", "a.test.yaml")

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"nested path inside root accepted", inside, true},
		{"relative dotdot escape rejected", filepath.Join(root, "..", "escape.yaml"), false},
		{"deeper relative traversal rejected", filepath.Join(root, "..", "..", "etc", "passwd"), false},
		{"absolute path outside root rejected", filepath.Join(parent, "etc", "passwd"), false},
		{"sibling directory sharing root as a name prefix rejected", filepath.Join(root+"X", "evil.yaml"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withinRoot(root, tc.path); got != tc.want {
				t.Errorf("withinRoot(%q, %q) = %v, want %v", root, tc.path, got, tc.want)
			}
		})
	}

	// An empty root must be rejected outright: it would otherwise clean to "."
	// and admit paths relative to the working directory.
	if withinRoot("", inside) {
		t.Error(`withinRoot("", …) = true, want false for an empty root`)
	}
}

// wbFileIndexByBase returns the index into files whose Path's base name is
// base, failing the test if none matches. writeMultiModelWorkspaceDir's glob
// order isn't guaranteed, so delete tests locate "a.test.yaml"/"b.test.yaml"
// by name rather than assuming an index.
func wbFileIndexByBase(t *testing.T, files []*modeltest.TestFile, base string) int {
	t.Helper()
	for i, tf := range files {
		if filepath.Base(tf.Path) == base {
			return i
		}
	}
	t.Fatalf("no test file named %q in %+v", base, files)
	return -1
}

// TestWorkbenchDeleteRemovesFile drives "d" then "y" on a 2-file workspace's
// file list: the selected file should be removed from disk and from
// m.wb.files, the workspace reload should leave the other file intact, and
// a re-run should be dispatched (since tests remain).
func TestWorkbenchDeleteRemovesFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dir := writeMultiModelWorkspaceDir(t)
	ws, err := modeltest.LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = wbFileIndexByBase(t, ws.TestFiles, "a.test.yaml")

	aPath := ws.TestFiles[mod.wb.treeSel].Path
	bIdx := wbFileIndexByBase(t, ws.TestFiles, "b.test.yaml")
	bPath := ws.TestFiles[bIdx].Path

	var tm tea.Model = mod
	tm, _ = tm.Update(key("d"))
	m := tm.(Model)
	if m.confirm == nil {
		t.Fatal("d should open a confirmation modal")
	}

	tm, cmd := tm.Update(key("y"))
	m = tm.(Model)
	if m.confirm != nil {
		t.Fatal("y should close the confirmation modal")
	}
	if cmd == nil {
		t.Fatal("deleting a file with tests remaining should dispatch a re-run command")
	}

	if _, err := os.Stat(aPath); !os.IsNotExist(err) {
		t.Fatalf("deleted file should no longer exist on disk, stat err: %v", err)
	}
	for _, tf := range m.wb.files {
		if tf.Path == aPath {
			t.Fatal("deleted file should no longer be in m.wb.files")
		}
	}
	found := false
	for _, tf := range m.wb.files {
		if tf.Path == bPath {
			found = true
		}
	}
	if !found {
		t.Fatalf("the other file should remain in m.wb.files; got %+v", m.wb.files)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Fatalf("the other file should remain on disk: %v", err)
	}
}

// TestWorkbenchDeleteCancel drives "d" then "n": the file must survive on
// disk and in m.wb.files.
func TestWorkbenchDeleteCancel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dir := writeMultiModelWorkspaceDir(t)
	ws, err := modeltest.LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = wbFileIndexByBase(t, ws.TestFiles, "a.test.yaml")
	aPath := ws.TestFiles[mod.wb.treeSel].Path

	var tm tea.Model = mod
	tm, _ = tm.Update(key("d"))
	tm, _ = tm.Update(key("n"))
	m := tm.(Model)

	if m.confirm != nil {
		t.Fatal("n should close the confirmation modal")
	}
	if _, err := os.Stat(aPath); err != nil {
		t.Fatalf("cancelling should leave the file on disk: %v", err)
	}
	found := false
	for _, tf := range m.wb.files {
		if tf.Path == aPath {
			found = true
		}
	}
	if !found {
		t.Fatal("cancelling should leave the file in m.wb.files")
	}
}

// TestWorkbenchDeleteWrongSelectionSafe verifies the confirm's run closure
// captures the specific *modeltest.TestFile selected at "d"-time, not
// whatever the cursor points at when "y" lands: it simulates a selection
// change in between (e.g. a race) and asserts the originally-selected file
// is the one deleted.
func TestWorkbenchDeleteWrongSelectionSafe(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dir := writeMultiModelWorkspaceDir(t)
	ws, err := modeltest.LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	aIdx := wbFileIndexByBase(t, ws.TestFiles, "a.test.yaml")
	bIdx := wbFileIndexByBase(t, ws.TestFiles, "b.test.yaml")
	aPath := ws.TestFiles[aIdx].Path
	bPath := ws.TestFiles[bIdx].Path

	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = aIdx

	var tm tea.Model = mod
	tm, _ = tm.Update(key("d"))
	m := tm.(Model)
	if m.confirm == nil {
		t.Fatal("d should open a confirmation modal")
	}

	// Simulate the selection moving to the other file before the confirmation
	// is answered (the confirm modal itself blocks navigation keys, so this
	// stands in for any other path that could otherwise mutate the cursor first).
	m.wb.treeSel = bIdx

	var tm2 tea.Model = m
	tm2, _ = tm2.Update(key("y"))
	final := tm2.(Model)

	if _, err := os.Stat(aPath); !os.IsNotExist(err) {
		t.Fatalf("the originally-selected file (a) should be deleted, stat err: %v", err)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Fatalf("the file selected after the fact (b) must NOT be deleted: %v", err)
	}
	found := false
	for _, tf := range final.wb.files {
		if tf.Path == bPath {
			found = true
		}
	}
	if !found {
		t.Fatal("b should remain in m.wb.files")
	}
}

// TestWorkbenchDeleteLastFileClearsList covers deleting the last remaining
// test file: the workspace reload must not crash, m.wb.files should end up
// empty, and no re-run should be dispatched (nothing left to run).
func TestWorkbenchDeleteLastFileClearsList(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := copyDocsWorkspace(t) // single test file: tests/documents.test.yaml
	ws, err := modeltest.LoadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.TestFiles) != 1 {
		t.Fatalf("want exactly 1 test file in the docs workspace, got %d", len(ws.TestFiles))
	}
	path := ws.TestFiles[0].Path

	mod := newTestModel().(Model)
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = 0

	var tm tea.Model = mod
	tm, _ = tm.Update(key("d"))
	tm, _ = tm.Update(key("y"))
	m := tm.(Model)

	// Deleting the last file surfaces a success toast but must NOT dispatch a
	// re-run (nothing left to run), which runSuite would signal via wb.running.
	if m.wb.running {
		t.Fatal("deleting the last file should not dispatch a re-run")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("deleted file should no longer exist on disk, stat err: %v", err)
	}
	if len(m.wb.files) != 0 {
		t.Fatalf("m.wb.files should be empty after deleting the last file, got %+v", m.wb.files)
	}
	body := stripANSIView(m.sectionBody())
	if strings.TrimSpace(body) == "" {
		t.Fatal("the empty-workspace body should still render a friendly message, not blank")
	}
}

// TestWorkbenchDeleteSurfacesSuccessToast covers the delete-success feedback:
// the Tests footer renders no status text, so a completed delete must confirm
// with a visible toast rather than a silent status line.
func TestWorkbenchDeleteSurfacesSuccessToast(t *testing.T) {
	mod := seedWorkbenchModel(t, copyDocsWorkspace(t)) // single test file; XDG isolated by the helper
	mod.wb.treeSel = 0

	var tm tea.Model = mod
	tm, _ = tm.Update(key("d"))
	if tm.(Model).confirm == nil {
		t.Fatal("d should open a confirmation modal")
	}
	tm, cmd := tm.Update(key("y"))
	m := tm.(Model)
	if cmd == nil {
		t.Fatal("confirming the delete should return a command (the success toast)")
	}
	if !m.toasts.Active() {
		t.Fatal("deleting a test file should surface a visible success toast, not just set m.status")
	}
	if view := stripANSIView(m.toasts.View()); !strings.Contains(view, "deleted") {
		t.Fatalf("delete toast should confirm the deletion; got:\n%s", view)
	}
}

func TestTestResultsHelpAndFooterAdvertiseKeys(t *testing.T) {
	m := Model{section: secTestResults, width: 120}
	help := stripANSIView(m.helpBody())
	for _, want := range []string{"run suite", "run selected file", "edit test", "new test", "delete test", "toggle coverage"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help overlay should describe %q:\n%s", want, help)
		}
	}
	m.focus = shell.FocusPanel
	footer := strings.Join(m.statusKeys(), " ")
	for _, want := range []string{"r run", "R run file", "e edit", "n new", "d delete", "c coverage", "esc"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer should advertise %q: %s", want, footer)
		}
	}
}

func TestTestResultsFooterWhileNewPromptOpen(t *testing.T) {
	m := Model{section: secTestResults, focus: shell.FocusPanel, wb: workbench{newPromptOpen: true}}
	footer := strings.Join(m.statusKeys(), " ")
	if !strings.Contains(footer, "create") || !strings.Contains(footer, "cancel") {
		t.Fatalf("footer should show create/cancel while the new-file prompt is open: %s", footer)
	}
}

func TestHandleWheelNoopsWhileNewPromptOpen(t *testing.T) {
	sh := shell.New()
	sh.SetSize(80, 24)
	m := Model{sh: sh, section: secTestResults, wb: workbench{newPromptOpen: true}}
	_, cmd := m.handleWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if cmd != nil {
		t.Fatal("wheel scroll should no-op while the new-file prompt is open")
	}
}

func TestSplitCommandLine(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"vim", []string{"vim"}},
		{"code --wait", []string{"code", "--wait"}},
		{"  nvim   -u  none  ", []string{"nvim", "-u", "none"}},
		{`"/Applications/Sublime Text.app/Contents/SharedSupport/bin/subl" -w`,
			[]string{"/Applications/Sublime Text.app/Contents/SharedSupport/bin/subl", "-w"}},
		{`'my editor' --flag`, []string{"my editor", "--flag"}},
		{`emacs\ w`, []string{"emacs w"}},
		{"", nil},
	}
	for _, tc := range cases {
		got := splitCommandLine(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitCommandLine(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitCommandLine(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestWindowLines(t *testing.T) {
	lines := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}

	// Fits entirely: returned unchanged.
	if got := windowLines(lines[:3], 2, 5); len(got) != 3 {
		t.Errorf("small slice should be returned whole, got %v", got)
	}

	// Selection near the end must be visible in the window.
	win := windowLines(lines, 9, 4)
	if len(win) != 4 {
		t.Fatalf("want 4 lines, got %d", len(win))
	}
	if win[len(win)-1] != "9" {
		t.Errorf("selected last line must be visible, window = %v", win)
	}

	// Selection at the top keeps the header (line 0) visible.
	win = windowLines(lines, 0, 4)
	if win[0] != "0" {
		t.Errorf("top selection should keep first line, window = %v", win)
	}

	// Selection in the middle stays within the returned window.
	win = windowLines(lines, 5, 4)
	found := false
	for _, l := range win {
		if l == "5" {
			found = true
		}
	}
	if !found {
		t.Errorf("selected line 5 must be within window %v", win)
	}
}
