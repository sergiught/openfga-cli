package playground

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/style"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// manyRelationCoverage builds a synthetic coverage report with n relations
// (each with a miss), so renderWorkbenchCoverage produces more lines than fit
// in a small pane — the fixture the coverage-scroll and width-cap tests below
// need to force scrolling/wrapping deterministically, without depending on a
// real workspace's fixture size.
func manyRelationCoverage(n int) *modeltest.Coverage {
	tc := modeltest.TypeCov{Type: "document", Covered: n, Total: n * 2}
	for i := 0; i < n; i++ {
		tc.Relations = append(tc.Relations, modeltest.RelCov{
			Relation: fmt.Sprintf("relation-%02d", i),
			Covered:  1,
			Total:    2,
			Missed:   []string{"branch-x"},
		})
	}
	return &modeltest.Coverage{
		Total:   n * 2,
		Covered: n,
		Percent: 50,
		Types:   []modeltest.TypeCov{tc},
	}
}

// --- (1) up/k and down/j must scroll coverage, not an invisible tree selection ---

func TestTestResultsArrowKeysScrollCoverageInsteadOfTree(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.wb.results = failingTestResults()
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = 1
	mod.wb.coverage = manyRelationCoverage(30)
	mod.wb.showCoverage = true

	var tm tea.Model = mod
	// Shrink the pane so the 30-relation coverage report overflows it and
	// scrolling actually has somewhere to go.
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m := tm.(Model)
	if m.wb.covScroll != 0 {
		t.Fatalf("covScroll should start at 0, got %d", m.wb.covScroll)
	}

	tm, _ = tm.Update(key("down"))
	m = tm.(Model)
	if m.wb.covScroll != 1 {
		t.Fatalf("down while coverage is shown should scroll it (covScroll=1); got %d", m.wb.covScroll)
	}
	if m.wb.treeSel != 1 {
		t.Fatalf("down while coverage is shown must not move the (invisible) tree selection; treeSel=%d", m.wb.treeSel)
	}

	tm, _ = tm.Update(key("j"))
	m = tm.(Model)
	if m.wb.covScroll != 2 {
		t.Fatalf("j while coverage is shown should scroll it further; covScroll=%d", m.wb.covScroll)
	}

	tm, _ = tm.Update(key("up"))
	m = tm.(Model)
	if m.wb.covScroll != 1 {
		t.Fatalf("up while coverage is shown should scroll it back up; covScroll=%d", m.wb.covScroll)
	}

	tm, _ = tm.Update(key("k"))
	m = tm.(Model)
	if m.wb.covScroll != 0 {
		t.Fatalf("k while coverage is shown should scroll it back to 0; covScroll=%d", m.wb.covScroll)
	}
	if m.wb.treeSel != 1 {
		t.Fatalf("tree selection must remain untouched throughout; treeSel=%d", m.wb.treeSel)
	}
}

// When coverage is hidden, up/k and down/j must still move the tree
// selection as before — only the coverage-visible case changes behavior.
func TestTestResultsArrowKeysStillMoveTreeWhenCoverageHidden(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.wb.results = failingTestResults()
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = 0
	mod.wb.coverage = manyRelationCoverage(30)
	mod.wb.showCoverage = false

	var tm tea.Model = mod
	tm, _ = tm.Update(key("down"))
	m := tm.(Model)
	if m.wb.treeSel != 1 {
		t.Fatalf("down with coverage hidden should move the tree selection to 1; got %d", m.wb.treeSel)
	}
	if m.wb.covScroll != 0 {
		t.Fatalf("covScroll should be untouched when coverage isn't shown; got %d", m.wb.covScroll)
	}
}

// --- (2) clicking a visible row must select the same node keyboard nav would ---

func TestTestResultsClickSelectsRowLikeKeyboard(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.wb.results = failingTestResults()
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = 0

	var tm tea.Model = mod
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := tm.(Model)
	bx, by := m.sh.MainBodyOrigin()

	// Tree rows: 0 summary, 1 blank, 2 dir header, 3 file, 4 anne (node 1), 5
	// bob (node 2) — matches wbTreeRowNodes' fixed 3-line header.
	tm2, _ := tea.Model(m).Update(tea.MouseClickMsg{X: bx + 2, Y: by + 5, Button: tea.MouseLeft})
	clicked := tm2.(Model)
	if clicked.wb.treeSel != 2 {
		t.Fatalf("clicking bob-can-view's row should select node 2; got treeSel=%d", clicked.wb.treeSel)
	}

	// Cross-check against two keyboard "down" presses from the same start.
	tmKey, _ := tea.Model(m).Update(key("down"))
	tmKey, _ = tmKey.Update(key("down"))
	if got, want := clicked.wb.treeSel, tmKey.(Model).wb.treeSel; got != want {
		t.Fatalf("click selection (%d) should match keyboard navigation (%d) for the same row", got, want)
	}

	// Clicking the file row should select the file node.
	tm3, _ := tea.Model(m).Update(tea.MouseClickMsg{X: bx + 2, Y: by + 3, Button: tea.MouseLeft})
	if got := tm3.(Model).wb.treeSel; got != 0 {
		t.Fatalf("clicking the file row should select node 0; got %d", got)
	}
}

// A click outside the Tests pane, or while coverage/spinner occupy it, must
// not move the selection (there's nothing selectable drawn there).
func TestTestResultsClickIgnoredOverCoverageAndWhileRunning(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.wb.results = failingTestResults()
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = 0
	mod.wb.coverage = manyRelationCoverage(3)
	mod.wb.showCoverage = true

	var tm tea.Model = mod
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := tm.(Model)
	bx, by := m.sh.MainBodyOrigin()

	tm2, _ := tea.Model(m).Update(tea.MouseClickMsg{X: bx + 2, Y: by + 5, Button: tea.MouseLeft})
	if got := tm2.(Model).wb.treeSel; got != 0 {
		t.Fatalf("clicking over the coverage pane must not change the tree selection; got %d", got)
	}

	m.wb.showCoverage = false
	m.wb.running = true
	tm3, _ := tea.Model(m).Update(tea.MouseClickMsg{X: bx + 2, Y: by + 5, Button: tea.MouseLeft})
	if got := tm3.(Model).wb.treeSel; got != 0 {
		t.Fatalf("clicking while a run is in flight (spinner shown) must not change the tree selection; got %d", got)
	}
}

func TestTestResultsWheelIgnoredWhileRunning(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.running = true
	mod.wb.treeSel = 1

	got, _ := mod.handleWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if got.(Model).wb.treeSel != 1 {
		t.Fatal("wheel input must not mutate the hidden tree selection while tests run")
	}
}

// --- (3) the footer stays responsive: essential hints survive on narrower terminals ---

func TestTestResultsFooterKeepsEscAndNavAcrossWidths(t *testing.T) {
	for _, w := range []int{80, 100, 120} {
		m := Model{section: secTestResults, focus: shell.FocusPanel, width: w}
		keys := append(append([]string{}, m.statusKeys()...), "? help")
		joined := strings.Join(keys, " ")
		if !strings.Contains(joined, "esc") {
			t.Fatalf("width %d: footer must keep the esc hint; got %q", w, joined)
		}
		if !strings.Contains(joined, "select") {
			t.Fatalf("width %d: footer must keep navigation; got %q", w, joined)
		}
		// The rendered keycap row must fit within the terminal width, or
		// shell.Shell.renderStatus truncates the whole joined row from its
		// right-hand end — silently cutting the trailing esc/help hints
		// instead of just the least essential ones.
		var rendered []string
		for _, k := range keys {
			rendered = append(rendered, style.Keycap(k))
		}
		if got := lipgloss.Width(strings.Join(rendered, " ")); got >= w {
			t.Fatalf("width %d: footer keys render %d cols wide and would overflow/clip", w, got)
		}
	}
}

func TestTestResultsFooterDropsLeastEssentialHintsWhenNarrow(t *testing.T) {
	wide := Model{section: secTestResults, focus: shell.FocusPanel, width: 120}
	narrow := Model{section: secTestResults, focus: shell.FocusPanel, width: 80}
	wideFooter := strings.Join(wide.statusKeys(), " ")
	narrowFooter := strings.Join(narrow.statusKeys(), " ")

	if !strings.Contains(wideFooter, "R run file") {
		t.Fatalf("a 120-col footer should still advertise R run file: %s", wideFooter)
	}
	if strings.Contains(narrowFooter, "R run file") {
		t.Fatalf("an 80-col footer should drop the less essential R run file hint to make room: %s", narrowFooter)
	}
	if !strings.Contains(narrowFooter, "esc") || !strings.Contains(narrowFooter, "select") {
		t.Fatalf("an 80-col footer must still keep esc and navigation: %s", narrowFooter)
	}
}

// --- (4) rendering the tree must not rescan every result per file/test row ---

// buildWbModel constructs a workbench Model with nFiles test files, each
// with testsPerFile tests, and one TestResult per test — enough to exercise
// wbTreeBody's render path at a chosen scale without a real workspace on
// disk. Every file's stem is distinct so wbResultsByStem's grouping has
// nFiles buckets, each holding testsPerFile results.
func buildWbModel(nFiles, testsPerFile int) Model {
	mod := newTestModel().(Model)
	files := make([]*modeltest.TestFile, 0, nFiles)
	var results []modeltest.TestResult
	for i := 0; i < nFiles; i++ {
		stem := fmt.Sprintf("file-%05d", i)
		tf := &modeltest.TestFile{Path: stem + ".test.yaml"}
		for j := 0; j < testsPerFile; j++ {
			name := fmt.Sprintf("test-%05d", j)
			tf.Tests = append(tf.Tests, modeltest.Test{Name: name})
			results = append(results, modeltest.TestResult{Name: stem + "/" + name, Passed: j%2 == 0})
		}
		files = append(files, tf)
	}
	mod.wb.workspace = &modeltest.Workspace{TestFiles: files}
	mod.wb.files = files
	mod.wb.results = results
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	return mod
}

// TestWbTreeBodyRenderScalesRoughlyLinearlyNotQuadratically guards against
// wbTreeBody (and the helpers it calls per row) regressing back to rescanning
// every result for every file/test row — O((files+tests)*results) — instead
// of building the file→results grouping once and indexing into it —
// O(files+tests+results). Growing the workspace 4x should cost render time
// roughly 4x (linear in the total item count); a quadratic-in-results
// re-scan would cost roughly 16x. The threshold leaves a wide margin (a
// "linear" implementation would have to regress past 10x, well above the
// expected ~4x) so ordinary timing noise on a shared/loaded machine doesn't
// make this test flaky, while still catching a real re-introduction of the
// per-row rescan.
func TestWbTreeBodyRenderScalesRoughlyLinearlyNotQuadratically(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// best takes the minimum of several renders so scheduling noise on a
	// shared machine doesn't inflate the measurement (noise can only make a
	// render slower than its true cost, never faster).
	best := func(nFiles, testsPerFile int) time.Duration {
		mod := buildWbModel(nFiles, testsPerFile)
		mod.wbTreeBody(100, 40) // warm up (first call may pay for lazily-built state)
		min := time.Hour
		for i := 0; i < 7; i++ {
			start := time.Now()
			mod.wbTreeBody(100, 40)
			if d := time.Since(start); d < min {
				min = d
			}
		}
		return min
	}

	const testsPerFile = 6
	small := best(150, testsPerFile)
	large := best(600, testsPerFile) // 4x the files, 4x the tests, 4x the results

	if small <= 0 {
		t.Skip("measured duration too small to compare reliably on this machine")
	}
	ratio := float64(large) / float64(small)
	if ratio > 10 {
		t.Fatalf("rendering 4x the files/tests/results took %.1fx as long (small=%v large=%v); "+
			"want roughly linear (~4x), not quadratic-in-results (~16x) growth", ratio, small, large)
	}
}

// --- (5) every coverage-report line is width-capped consistently ---

func TestRenderWorkbenchCoverageCapsEveryLine(t *testing.T) {
	cov := manyRelationCoverage(5)
	cov.Types[0].Type = strings.Repeat("longtype", 10)
	cov.Types[0].Relations[0].Relation = strings.Repeat("very-long-relation-name-", 5)
	cov.Types[0].Relations[0].Missed = []string{strings.Repeat("missed-branch-name-", 10)}

	const width = 40
	out := renderWorkbenchCoverage(cov, width)
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("coverage line %d exceeds the %d-col cap (got %d): %q", i, width, w, line)
		}
	}
}

// --- (6) commands while a run is in flight give visible feedback ---

func TestTestResultsCommandsGiveRunningFeedbackWhileRunning(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.running = true

	for _, k := range []string{"r", "R", "c", "v", "down"} {
		var tm tea.Model = mod
		tm, cmd := tm.Update(key(k))
		if cmd == nil {
			t.Fatalf("%q while running should still return a command (the running toast)", k)
		}
		m := tm.(Model)
		if !m.wb.running {
			t.Fatalf("%q while already running must not clear m.wb.running", k)
		}
		if !m.toasts.Active() {
			t.Fatalf("%q while running should surface a toast", k)
		}
		view := stripANSIView(m.toasts.View())
		if !strings.Contains(view, "running") {
			t.Fatalf("%q while running should say tests are running; got toast: %s", k, view)
		}
	}
}

// --- (7) toggling verbose while coverage is visible must be truthful ---

func TestShowingCoverageHidesVerboseDetail(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.verbose = true
	mod.wb.coverage = manyRelationCoverage(3)

	var tm tea.Model = mod
	tm, _ = tm.Update(key("c"))
	got := tm.(Model)
	if !got.wb.showCoverage {
		t.Fatal("c should show available coverage")
	}
	if got.wb.verbose {
		t.Fatal("showing coverage should disable the hidden verbose-detail mode")
	}
}

func TestToggleVerboseWhileCoverageShownLeavesCoverageAndShowsExplanation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.wb.results = failingTestResults()
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = mod.wbNodeForTest("documents/anne-cannot-view")
	mod.wb.coverage = manyRelationCoverage(3)
	mod.wb.showCoverage = true

	var tm tea.Model = mod
	tm, cmd := tm.Update(key("v"))
	m := tm.(Model)
	if m.wb.showCoverage {
		t.Fatal("toggling verbose while coverage is shown should leave coverage so the explanation is actually visible")
	}
	if !m.wb.verbose {
		t.Fatal("v should turn verbose on")
	}
	if cmd == nil {
		t.Fatal("v should push a toast")
	}
	view := stripANSIView(m.toasts.View())
	if !strings.Contains(view, "explanation shown") {
		t.Fatalf("toast should truthfully say the explanation is now shown; got: %s", view)
	}

	body := stripANSIView(m.sectionBody())
	if strings.Contains(body, "coverage:") {
		t.Fatalf("after leaving coverage for the explanation, coverage should no longer render; got:\n%s", body)
	}
	if !strings.Contains(body, "anne-cannot-view") {
		t.Fatalf("body should show the tree/detail (naming the selected test) once coverage is left; got:\n%s", body)
	}

	// Toggling verbose back off must not resurrect coverage on its own.
	tm2, _ := tea.Model(m).Update(key("v"))
	m2 := tm2.(Model)
	if m2.wb.verbose {
		t.Fatal("a second v press should turn verbose back off")
	}
	if m2.wb.showCoverage {
		t.Fatal("turning verbose back off should not resurrect coverage on its own")
	}
}

// --- (8) verbose tree+separator+detail must never exceed an extremely short pane ---

func TestWbTreeBodyVerboseNeverExceedsShortPaneHeight(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.wb.results = failingTestResults()
	mod.section = secTestResults
	mod.focus = shell.FocusPanel
	mod.wb.treeSel = mod.wbNodeForTest("documents/anne-cannot-view")
	mod.wb.verbose = true

	for h := 1; h <= 8; h++ {
		out := mod.wbTreeBody(60, h)
		if got := strings.Count(out, "\n") + 1; got > h {
			t.Fatalf("h=%d: verbose tree+separator+detail rendered %d lines, must not exceed the pane height", h, got)
		}
	}
}

func TestWbLayoutNeverOverflowsShortHeights(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)
	wbSeedDocuments(&mod)
	mod.wb.results = failingTestResults()
	mod.wb.treeSel = mod.wbNodeForTest("documents/anne-cannot-view")
	mod.wb.verbose = true

	for h := 1; h <= 12; h++ {
		treeH, detailH := mod.wbLayout(h)
		total := treeH
		if detailH > 0 {
			total += 1 + detailH
		}
		if total > h {
			t.Fatalf("h=%d: wbLayout returned treeH=%d detailH=%d, totalling %d rows > h", h, treeH, detailH, total)
		}
	}
}

// --- (9) result grouping uses source paths, not ambiguous name splitting.

func TestWbResultsByStemMatchesNestedWorkspaceRelativeFileIdentity(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mod := newTestModel().(Model)

	root := "/repo"
	nested := &modeltest.TestFile{
		Path:  root + "/tests/nested/access.test.yaml",
		Tests: []modeltest.Test{{Name: "owner-can-view"}},
	}
	flat := &modeltest.TestFile{
		Path:  root + "/tests/access.test.yaml",
		Tests: []modeltest.Test{{Name: "owner-can-view"}},
	}
	ws := &modeltest.Workspace{Root: root, TestFiles: []*modeltest.TestFile{nested, flat}}
	mod.wb.workspace = ws
	mod.wb.files = ws.TestFiles

	nestedStem := ws.TestFileID(nested)
	flatStem := ws.TestFileID(flat)
	if nestedStem == flatStem {
		t.Fatalf("test fixture invalid: nested and flat file stems collided (%q)", nestedStem)
	}
	if !strings.Contains(nestedStem, "/") {
		t.Fatalf("test fixture invalid: nested stem %q must contain a path separator to exercise multi-segment grouping", nestedStem)
	}

	// Same test name in both files, so a stale "match by first slash" (or by
	// basename alone) would either misroute one file's result to the other or
	// merge them under a single bucket.
	mod.wb.results = []modeltest.TestResult{
		{Name: nestedStem + "/group/owner-can-view", File: "tests/nested/access.test.yaml", Passed: true},
		{Name: flatStem + "/group/owner-can-view", File: "tests/access.test.yaml", Passed: false},
	}

	if got := mod.wbFileTests(nested); len(got) != 1 || !got[0].Passed {
		t.Fatalf("nested file's tests = %+v, want exactly its own passing result", got)
	}
	if got := mod.wbFileTests(flat); len(got) != 1 || got[0].Passed {
		t.Fatalf("flat file's tests = %+v, want exactly its own failing result", got)
	}

	if pass, total := mod.wbFileStatus(nested); pass != 1 || total != 1 {
		t.Fatalf("nested file status = %d/%d, want 1/1", pass, total)
	}
	if pass, total := mod.wbFileStatus(flat); pass != 0 || total != 1 {
		t.Fatalf("flat file status = %d/%d, want 0/1", pass, total)
	}

	// The tree must show exactly the two files plus their one test each — no
	// phantom nodes from a stem's "/" being mistaken for a nesting boundary.
	if nodes := mod.wbVisibleNodes(); len(nodes) != 4 {
		t.Fatalf("visible nodes = %d, want 4 (2 files + 2 tests)", len(nodes))
	}
}
