package playground

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/modeltest"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// wbSeedDocuments points m at a single-file workspace whose stem is
// "documents", matching failingTestResults()'s "documents/..." test names —
// the Tests section now requires a workspace before it will render anything
// but its empty state.
func wbSeedDocuments(m *Model) {
	m.wb.workspace = &modeltest.Workspace{TestFiles: []*modeltest.TestFile{{Path: "documents.test.yaml"}}}
	m.wb.files = m.wb.workspace.TestFiles
}

// failingTestResults returns two results — a failed check (with an Explain
// tree + nearest-miss suggestion) and a passing one — for driving the Tests
// section.
func failingTestResults() []modeltest.TestResult {
	return []modeltest.TestResult{
		{
			Name:   "documents/anne-cannot-view",
			Passed: false,
			Assertions: []modeltest.AssertionResult{
				{
					Kind:     "check",
					Subject:  "user:anne viewer document:roadmap",
					Expected: true,
					Got:      false,
					Passed:   false,
					Explain: &modeltest.Explain{
						Verdict: false,
						Tree: &modeltest.ExplainNode{
							Label:  "document:roadmap#viewer",
							Result: false,
							Children: []*modeltest.ExplainNode{
								{Label: "owner", Result: false, Reason: "no matching tuple"},
							},
						},
						NearestMiss: "add user:anne owner document:roadmap",
					},
				},
			},
		},
		{Name: "documents/bob-can-view", Passed: true},
	}
}

func TestTestResultsBodyRendersFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newTestModel().(Model)
	wbSeedDocuments(&m)
	m.wb.results = failingTestResults()
	m.section = secTestResults
	m.wb.treeSel = m.wbNodeForTest("documents/anne-cannot-view")

	body := stripANSIView(m.sectionBody())
	if !strings.Contains(body, "anne-cannot-view") {
		t.Fatalf("body should list the failing test name; got:\n%s", body)
	}
}

func TestTestResultsEmptyState(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newTestModel().(Model)
	m.section = secTestResults

	body := stripANSIView(m.sectionBody())
	if strings.TrimSpace(body) == "" {
		t.Fatal("empty test results should render a friendly empty state, not blank")
	}
}

func TestDigit9JumpsToTestResults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.wb.results = failingTestResults()
	mod.wb.treeSel = mod.wbNodeForTest("documents/anne-cannot-view")

	// From the sidebar (default focus), digit '9' must reach the 9th section.
	var tm tea.Model = mod
	tm, _ = tm.Update(key("9"))
	m := tm.(Model)
	if m.section != secTestResults {
		t.Fatalf("digit 9 should jump to Test Results (section %d); got section %d", secTestResults, m.section)
	}

	body := stripANSIView(m.sectionBody())
	if !strings.Contains(body, "anne-cannot-view") {
		t.Fatalf("after jumping the body should list the failing test; got:\n%s", body)
	}
}

func TestDigit9JumpsToTestResultsFromPanel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	mod.wb.results = failingTestResults()
	mod.focus = shell.FocusPanel

	var tm tea.Model = mod
	tm, _ = tm.Update(key("9"))
	if got := tm.(Model).section; got != secTestResults {
		t.Fatalf("digit 9 with the panel focused should jump to Test Results (section %d); got %d", secTestResults, got)
	}
}

func TestFirstFailedTestIndex(t *testing.T) {
	results := []modeltest.TestResult{
		{Name: "a", Passed: true},
		{Name: "b", Passed: false},
		{Name: "c", Passed: false},
	}
	if got := firstFailedTest(results); got != 1 {
		t.Fatalf("firstFailedTest = %d, want 1 (first failure)", got)
	}
	allPass := []modeltest.TestResult{{Name: "a", Passed: true}, {Name: "b", Passed: true}}
	if got := firstFailedTest(allPass); got != 0 {
		t.Fatalf("firstFailedTest with no failures = %d, want 0", got)
	}
}

func TestTestResultsSelectionMovesDown(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.wb.results = failingTestResults()
	mod.section = secTestResults
	mod.focus = shell.FocusPanel

	// Tree nodes (file expanded by default): [file, test anne, test bob].
	var tm tea.Model = mod
	tm, _ = tm.Update(key("down"))
	if got := tm.(Model).wb.treeSel; got != 1 {
		t.Fatalf("down should move the cursor to 1; got %d", got)
	}
	tm, _ = tm.Update(key("up"))
	if got := tm.(Model).wb.treeSel; got != 0 {
		t.Fatalf("up should move the cursor back to 0; got %d", got)
	}
	// The cursor must not run past the ends.
	tm, _ = tm.Update(key("up"))
	if got := tm.(Model).wb.treeSel; got != 0 {
		t.Fatalf("up at the top should stay at 0; got %d", got)
	}
}
