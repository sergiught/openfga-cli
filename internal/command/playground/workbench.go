package playground

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// wbNodeKind distinguishes the two selectable row kinds in the Tests
// navigator tree: a test file, or one of its tests nested under it.
type wbNodeKind int

const (
	wbNodeFile wbNodeKind = iota
	wbNodeTest
)

// wbNode is a single visible, selectable row in the navigator tree.
// fileIdx indexes m.wb.files; for a test node, testIdx indexes that file's
// results (wbFileTests order).
type wbNode struct {
	kind    wbNodeKind
	fileIdx int
	testIdx int
}

// wbVisibleNodes flattens the navigator tree into the ordered list of
// selectable rows: each test file, followed (when not collapsed) by its
// tests. The tests-dir header rendered above is a non-selectable label and is
// not represented here. m.wb.treeSel indexes into this list.
func (m Model) wbVisibleNodes() []wbNode {
	var nodes []wbNode
	for fi, tf := range m.wb.files {
		nodes = append(nodes, wbNode{kind: wbNodeFile, fileIdx: fi})
		if m.wb.collapsed[tf.Path] {
			continue
		}
		for ti := range m.wbFileTests(tf) {
			nodes = append(nodes, wbNode{kind: wbNodeTest, fileIdx: fi, testIdx: ti})
		}
	}
	return nodes
}

// clampWbTreeSel keeps m.wb.treeSel within the visible-node list's bounds.
func (m *Model) clampWbTreeSel() {
	n := len(m.wbVisibleNodes())
	if n == 0 {
		m.wb.treeSel = 0
		return
	}
	if m.wb.treeSel < 0 {
		m.wb.treeSel = 0
	}
	if m.wb.treeSel >= n {
		m.wb.treeSel = n - 1
	}
}

// wbSelectedNode returns the currently selected tree node, or false when the
// tree is empty / the cursor is out of range.
func (m Model) wbSelectedNode() (wbNode, bool) {
	nodes := m.wbVisibleNodes()
	if m.wb.treeSel < 0 || m.wb.treeSel >= len(nodes) {
		return wbNode{}, false
	}
	return nodes[m.wb.treeSel], true
}

// wbSelectedFile returns the test file the selected node belongs to — the file
// itself for a file node, or the owning file for a test node — with its index
// into m.wb.files. Used by R (run file), e (edit) and d (delete), which all
// operate on a whole file regardless of which row within it is selected.
func (m Model) wbSelectedFile() (*modeltest.TestFile, int, bool) {
	node, ok := m.wbSelectedNode()
	if !ok || node.fileIdx < 0 || node.fileIdx >= len(m.wb.files) {
		return nil, 0, false
	}
	return m.wb.files[node.fileIdx], node.fileIdx, true
}

// wbToggleCollapse flips whether path's tests are hidden, lazily initialising
// the map. Files are expanded by default (absent key == not collapsed).
func (m *Model) wbToggleCollapse(path string) {
	if m.wb.collapsed == nil {
		m.wb.collapsed = map[string]bool{}
	}
	m.wb.collapsed[path] = !m.wb.collapsed[path]
}

// wbNodeForTest returns the visible-node index of the test whose result name
// is resultName, or 0 (the first node) when no such visible test exists.
func (m Model) wbNodeForTest(resultName string) int {
	for i, n := range m.wbVisibleNodes() {
		if n.kind != wbNodeTest {
			continue
		}
		tests := m.wbFileTests(m.wb.files[n.fileIdx])
		if n.testIdx < len(tests) && tests[n.testIdx].Name == resultName {
			return i
		}
	}
	return 0
}

// wbFileStem derives a test file's logical name from its path — its base name
// minus the .test.yaml/.test.yml suffix — mirroring the "<file-stem>/<test-name>"
// naming modeltest.Run gives each TestResult.
func wbFileStem(path string) string {
	return modeltest.FileStem(path)
}

// wbFileTests returns tf's results from m.wb.results (those named
// "<tf's stem>/..."), in the order they appear in m.wb.results.
func (m Model) wbFileTests(tf *modeltest.TestFile) []modeltest.TestResult {
	prefix := wbFileStem(tf.Path) + "/"
	var out []modeltest.TestResult
	for _, r := range m.wb.results {
		if strings.HasPrefix(r.Name, prefix) {
			out = append(out, r)
		}
	}
	return out
}

// wbFileStatus reports how many of tf's tests passed vs. how many ran in the
// last run, from m.wb.results. Both are 0 when the file hasn't been run yet.
func (m Model) wbFileStatus(tf *modeltest.TestFile) (pass, total int) {
	for _, r := range m.wbFileTests(tf) {
		total++
		if r.Passed {
			pass++
		}
	}
	return pass, total
}

// runSuite starts a hermetic run — a fresh embedded engine each time, never
// the seeded/live server this playground session is otherwise connected to
// (see runSuiteCmd) — filtered by filter (modeltest.Options.Run): "" runs the
// whole workspace, "<file stem>/*" runs a single file. No-ops when there's no
// workspace loaded, or a run is already in flight.
func (m *Model) runSuite(filter string) (tea.Model, tea.Cmd) {
	if m.wb.workspace == nil {
		m.status = "no workspace"
		return *m, nil
	}
	if m.wb.running {
		return *m, nil
	}
	m.wb.running = true
	m.beginLoad()
	m.status = "running tests…"
	return *m, runSuiteCmd(m.ctx, m.wb.workspace, filter)
}

// wbStatus sets the status line and raises a matching toast. The Tests section's
// footer renders no status text (sectionStatus returns "" there), so a dead-end
// path that only set m.status would leave the user with no visible feedback —
// every such path routes through here so the message actually surfaces.
func (m *Model) wbStatus(level toast.Level, text string) tea.Cmd {
	m.status = text
	return m.toasts.Push(level, text)
}

// toggleVerbose flips whether the Tests section stacks the selected node's
// explanation below the navigator tree, surfacing the new state as a toast
// (the Tests footer renders no status text). Shared by the "v" key and
// enter/space on a test node, which both mean the same thing.
func (m *Model) toggleVerbose() tea.Cmd {
	m.wb.verbose = !m.wb.verbose
	if m.wb.verbose {
		return m.wbStatus(toast.Info, "explanation shown — v to hide")
	}
	return m.wbStatus(toast.Info, "explanation hidden")
}

// splitCommandLine splits a command string into words, honoring single and
// double quotes and backslash escapes (shell-style), so a $EDITOR whose binary
// path contains spaces still parses correctly when quoted, e.g.
// `"/Applications/Sublime Text.app/Contents/SharedSupport/bin/subl" -w`.
// Unquoted whitespace still separates words, matching shell semantics.
func splitCommandLine(s string) []string {
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		quote   rune // 0, '\'' or '"'
		escaped bool
	)
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			inWord = true
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
			inWord = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return words
}

// editorCommand resolves the user's terminal editor for opening a test file:
// $VISUAL, then $EDITOR, then the first of vim/vi/nano found on PATH. It
// returns the resolved binary and any leading arguments (the file path is
// appended by the caller). The binary is "" when nothing suitable is found, so
// the caller can surface a status hint rather than launching an empty command.
func editorCommand() (string, []string) {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			fields := splitCommandLine(v)
			if len(fields) == 0 {
				continue
			}
			return fields[0], fields[1:]
		}
	}
	for _, cand := range []string{"vim", "vi", "nano"} {
		if _, err := exec.LookPath(cand); err == nil {
			return cand, nil
		}
	}
	return "", nil
}

// handleTestFileEdited resumes after the external editor exits: it surfaces any
// launch error, then (regardless of whether the saved file is valid) reloads
// the workspace and re-runs the suite so the tree, results and coverage reflect
// the edit. An invalid file is warned about but NOT blocked — the user's edit
// is already on disk, and the re-run surfaces the failing/erroring test.
func (m *Model) handleTestFileEdited(msg testFileEditedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = "editor: " + msg.err.Error()
		return *m, nil
	}
	status := "saved " + filepath.Base(msg.path)
	var warn string // a durable toast for a bad save, which the async re-run's status would otherwise bury
	if buf, err := os.ReadFile(msg.path); err != nil {
		status = "cannot read " + filepath.Base(msg.path) + ": " + err.Error()
		warn = status
	} else if err := modeltest.ValidateTestFile(buf); err != nil {
		status = "saved with errors: " + err.Error()
		warn = status
	}
	// Reload the workspace so the parsed test files reflect the edit (a new file
	// now exists, or an existing one changed), re-clamping the selection in case
	// the file set changed.
	if m.wb.workspace != nil {
		if ws, err := modeltest.LoadWorkspace(m.wb.workspace.Root); err == nil {
			m.wb.workspace = ws
			m.wb.files = ws.TestFiles
			m.clampWbTreeSel()
		}
	}
	// Re-run so the tree/results/coverage reflect the edit. runSuite overwrites
	// m.status with a "running…" line, so restore our save status after it.
	_, cmd := m.runSuite("")
	m.status = status
	// A bad save also raises a durable toast: the async re-run's completion status
	// ("N/N passed") would otherwise bury the transient status line, and an
	// invalid reload is skipped — so without the toast the only signal that the
	// save was rejected would vanish and leave stale-but-green results on screen.
	if warn != "" {
		return *m, tea.Batch(cmd, m.toasts.Push(toast.Error, warn))
	}
	return *m, cmd
}

// openWorkbenchEditor launches the user's real terminal editor ($VISUAL /
// $EDITOR, falling back to vim/vi/nano) on the currently selected test file via
// tea.ExecProcess, which suspends the TUI, runs the editor full-screen, and
// resumes on exit — delivering a testFileEditedMsg so the workspace can be
// re-validated, reloaded and re-run. It is a no-op (with a status hint) when
// there is no workspace, no file selected, or no editor available.
func (m Model) openWorkbenchEditor() (tea.Model, tea.Cmd) {
	tf, _, ok := m.wbSelectedFile()
	if m.wb.workspace == nil || !ok {
		m.status = "no test file to edit"
		return m, nil
	}
	editor, args := editorCommand()
	if editor == "" {
		return m, m.wbStatus(toast.Info, "no editor found — set $EDITOR")
	}
	path := tf.Path
	c := exec.Command(editor, append(args, path)...)
	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		return testFileEditedMsg{path: path, err: err}
	})
}

// wbNewTemplate is the scaffold written to disk when creating a new test file
// via "n" — a minimal file that already passes modeltest.ValidateTestFile, so
// it remains valid even if the user quits the editor without changes.
const wbNewTemplate = `tests:
  - name: my-test
    check:
      - user: user:anne
        object: document:1
        assertions:
          viewer: true
`

// globBaseDir returns the literal leading directory portion of a doublestar
// glob pattern: every path segment up to (but not including) the first one
// containing a glob metacharacter. "tests/**/*.test.yaml" -> "tests".
func globBaseDir(pattern string) string {
	segments := strings.Split(pattern, "/")
	var base []string
	for _, seg := range segments {
		if strings.ContainsAny(seg, "*?[]") {
			break
		}
		base = append(base, seg)
	}
	return strings.Join(base, "/")
}

// wbTestsDir resolves the directory (relative to the workspace root) new test
// files are created under: the literal directory portion of the manifest's
// first tests: glob, or "tests" when there is no manifest, no glob entries, or
// the glob has no literal directory portion.
func wbTestsDir(ws *modeltest.Workspace) string {
	if ws != nil && ws.Manifest != nil && len(ws.Manifest.Tests) > 0 {
		if dir := globBaseDir(ws.Manifest.Tests[0]); dir != "" {
			return dir
		}
	}
	return "tests"
}

// openWorkbenchNewPrompt opens the filename prompt for creating a new test
// file, resolving the target directory from the workspace manifest. No-op
// (with a status hint) when there is no workspace to create a file in.
func (m Model) openWorkbenchNewPrompt() (tea.Model, tea.Cmd) {
	if m.wb.workspace == nil {
		return m, m.wbStatus(toast.Info, "no workspace to create a test file in")
	}
	m.wb.newPromptOpen = true
	m.wb.newPromptInput = ""
	m.wb.newPromptDir = wbTestsDir(m.wb.workspace)
	return m, nil
}

// wbNewFileName ensures name ends with the .test.yaml suffix every test file
// in the workspace uses, appending it when missing.
func wbNewFileName(name string) string {
	if strings.HasSuffix(name, ".test.yaml") {
		return name
	}
	return name + ".test.yaml"
}

// submitWorkbenchNewPrompt resolves and validates the typed filename against
// the workspace root, writes the wbNewTemplate scaffold to disk (so the real
// editor has a valid file to open), then launches the user's editor on it via
// tea.ExecProcess — the same path taken by editing an existing file. Rejects a
// blank name, a path that escapes the workspace root, and a name that already
// exists on disk; the prompt stays open on rejection so the user can correct
// it. If the user quits the editor without changes, the valid scaffold remains.
func (m Model) submitWorkbenchNewPrompt() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.wb.newPromptInput)
	if name == "" {
		return m, m.wbStatus(toast.Info, "filename cannot be empty")
	}
	path := filepath.Join(m.wb.workspace.Root, m.wb.newPromptDir, wbNewFileName(name))
	if !withinRoot(m.wb.workspace.Root, path) {
		return m, m.wbStatus(toast.Error, "refusing to create a file outside the workspace")
	}
	if _, err := os.Stat(path); err == nil {
		return m, m.wbStatus(toast.Info, "file already exists: "+filepath.Base(path))
	}
	editor, args := editorCommand()
	if editor == "" {
		return m, m.wbStatus(toast.Info, "no editor found — set $EDITOR")
	}
	// The tests directory may not exist yet; create it, then write the scaffold
	// so the editor opens a valid file rather than a blank buffer.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return m, m.wbStatus(toast.Error, "cannot create "+filepath.Base(path)+": "+err.Error())
	}
	if err := os.WriteFile(path, []byte(wbNewTemplate), 0o644); err != nil {
		return m, m.wbStatus(toast.Error, "cannot create "+filepath.Base(path)+": "+err.Error())
	}
	m.wb.newPromptOpen = false
	m.wb.newPromptInput = ""
	c := exec.Command(editor, append(args, path)...)
	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		return testFileEditedMsg{path: path, err: err}
	})
}

// withinRoot reports whether path resolves to a location inside root — the
// path-safety guard before creating or deleting a test file, so a crafted
// filename can never escape the workspace directory.
func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// passStyle picks the style for a pass/total tally: faint when nothing ran
// (total == 0), green when everything passed, red otherwise. Shared by every
// place that colours a test tally (summary line, file rows, detail pane).
func passStyle(pass, total int) lipgloss.Style {
	switch {
	case total == 0:
		return style.Faint
	case pass == total:
		return style.Success
	default:
		return style.Failure
	}
}

// wbSummaryLine renders the file list's top line: the overall pass tally
// across every result in m.wb.results, or a "not run yet" hint when the
// workspace hasn't been run this session.
func (m Model) wbSummaryLine() string {
	if len(m.wb.results) == 0 {
		return style.Faint.Render("No results yet — press r to run.")
	}
	pass := 0
	for _, r := range m.wb.results {
		if r.Passed {
			pass++
		}
	}
	total := len(m.wb.results)
	line := itoa(pass) + "/" + itoa(total) + " tests passed"
	return passStyle(pass, total).Render(line)
}

// wbTestLabel is a test's display name within the navigator — the bare test
// name with its owning file's "<stem>/" prefix stripped, since the file is
// already named on the row above it.
func wbTestLabel(name string) string {
	if i := strings.Index(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// wbDetailShareNum/wbDetailShareDen cap the verbose detail pane at 2/5 (40%) of
// the pane height, so the navigator tree above always keeps the majority.
const (
	wbDetailShareNum = 2
	wbDetailShareDen = 5
)

// wbDetailHeight picks how many rows of h the verbose detail pane in
// wbTreeBody gets: enough for card's own line count, capped at ~40% of h so
// the tree above always keeps the majority of the space. 0 when card is
// empty (nothing to show below the tree).
func wbDetailHeight(card string, h int) int {
	if card == "" {
		return 0
	}
	maxShare := h * wbDetailShareNum / wbDetailShareDen
	if maxShare < 1 {
		maxShare = 1
	}
	if lines := strings.Count(card, "\n") + 1; lines < maxShare {
		return lines
	}
	return maxShare
}

// wbTreeBody renders the Tests section as a file-navigator tree: the overall
// summary line, the tests directory as a faint header, then each test file
// (with a ▾/▸ collapse indicator and its pass/total status) and — for
// expanded files — its tests nested underneath with ✓/✗ markers. The selected
// node is highlighted, and the tree scrolls to keep it in view when there are
// more rows than fit. When m.wb.verbose is on, the tree is stacked above the
// selected node's detail pane (wbDetail): tree on top, a section-header
// separator naming the node, then the detail body below, all full-width.
func (m Model) wbTreeBody(w, h int) string {
	lines := []string{m.wbSummaryLine(), "", style.Faint.Render(safeText(wbTestsDir(m.wb.workspace)) + "/")}
	selLine := len(lines) // fall back to the header if nothing is selected

	for i, n := range m.wbVisibleNodes() {
		selected := i == m.wb.treeSel
		var row string
		switch n.kind {
		case wbNodeFile:
			row = m.wbTreeFileRow(m.wb.files[n.fileIdx], selected)
		case wbNodeTest:
			tf := m.wb.files[n.fileIdx]
			tests := m.wbFileTests(tf)
			if n.testIdx >= len(tests) {
				continue
			}
			row = wbTreeTestRow(tests[n.testIdx], selected)
		default:
			continue
		}
		if selected {
			selLine = len(lines)
		}
		lines = append(lines, row)
	}

	render := func(height int) string {
		body := strings.Join(windowLines(lines, selLine, height), "\n")
		return lipgloss.NewStyle().Width(w).Height(height).Render(body)
	}

	if !m.wb.verbose {
		return render(h)
	}

	title, card := m.wbDetail()
	detailH := wbDetailHeight(card, h)
	if detailH == 0 {
		return render(h)
	}

	treeH := h - detailH - 1 // -1 for the separator line
	if treeH < 1 {
		treeH = 1
	}
	sep := style.SectionHeader(title, w)
	// Cap the card the same way the coverage view is capped so a long RenderExplain
	// narrative that overflows its share of the pane shows a "⋯ more" hint instead
	// of being silently clipped — and can be wheel-scrolled (detailScroll) to reach
	// the rest without resizing the terminal.
	detail := lipgloss.NewStyle().Width(w).Height(detailH).Render(capLinesAt(card, m.wb.detailScroll, detailH))
	return lipgloss.JoinVertical(lipgloss.Left, render(treeH), sep, detail)
}

// windowLines returns at most h lines from lines, scrolled so index sel stays
// visible — the viewport windowing the Tests tree and coverage view need so a
// cursor (or content) past the fold isn't clipped out of reach.
func windowLines(lines []string, sel, h int) []string {
	if h <= 0 || len(lines) <= h {
		return lines
	}
	top := sel - h + 1 // scrolling down: keep sel on the last visible row
	if top > sel {
		top = sel
	}
	if top+h > len(lines) {
		top = len(lines) - h
	}
	if top < 0 {
		top = 0
	}
	return lines[top : top+h]
}

// wbDetail renders the detail pane for the selected node (shown below the
// tree when m.wb.verbose is on): a test node shows its explanation
// (testResultDetail); a file node shows a short path + pass/total summary.
// Empty when nothing is selected.
func (m Model) wbDetail() (string, string) {
	node, ok := m.wbSelectedNode()
	if !ok {
		return "", ""
	}
	tf := m.wb.files[node.fileIdx]
	switch node.kind {
	case wbNodeTest:
		tests := m.wbFileTests(tf)
		if node.testIdx < 0 || node.testIdx >= len(tests) {
			return "", ""
		}
		return m.testResultDetail(tests[node.testIdx])
	default: // wbNodeFile
		pass, total := m.wbFileStatus(tf)
		if total == 0 {
			return safeText(filepath.Base(tf.Path)), passStyle(pass, total).Render("Not run yet — press r to run.")
		}
		line := itoa(pass) + "/" + itoa(total) + " passed"
		return safeText(filepath.Base(tf.Path)), passStyle(pass, total).Render(line)
	}
}

// wbTreeFileRow renders a file node: a ▾ (expanded) / ▸ (collapsed) indicator,
// the file's base name, and its last-run pass/total status (green all-pass,
// red partial, faint not-run). Indented two columns, or prefixed "❯ " when
// selected.
func (m Model) wbTreeFileRow(tf *modeltest.TestFile, selected bool) string {
	indicator := "▾"
	if m.wb.collapsed[tf.Path] {
		indicator = "▸"
	}
	name := filepath.Base(tf.Path)
	pass, total := m.wbFileStatus(tf)
	frac := "not run"
	if total > 0 {
		frac = itoa(pass) + "/" + itoa(total)
	}
	status := passStyle(pass, total).Render(frac)
	if selected {
		return style.Heading.Render("❯ ") + indicator + " " + style.Heading.Render(safeText(name)) + "  " + status
	}
	return "  " + indicator + " " + safeText(name) + "  " + status
}

// wbTreeTestRow renders a test node nested under its file: a ✓/✗ marker and the
// bare test name, indented six columns, or prefixed "❯ " when selected.
func wbTreeTestRow(r modeltest.TestResult, selected bool) string {
	marker := style.Success.Render(style.IconCheck)
	if !r.Passed {
		marker = style.Failure.Render(style.IconCross)
	}
	label := wbTestLabel(r.Name)
	if selected {
		return style.Heading.Render("❯ ") + "    " + marker + " " + style.Heading.Render(safeText(label))
	}
	return "      " + marker + " " + safeText(label)
}

// padRight pads s with spaces to display-width w, for aligning the coverage
// table's plain-text columns.
func padRight(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// renderWorkbenchCoverage renders cov as a plain-text block for the Tests
// section pane (toggled with "c"): a per-type covered/total/percent table,
// a bold total row, and — for every relation with a miss — a line naming the
// uncovered branches. w bounds those lines so they wrap within the pane
// instead of running off it. Mirrors the shape of the CLI's `model test`
// renderCoverage, but built as a string rather than written to a Writer,
// since the pane isn't a real terminal.
func renderWorkbenchCoverage(cov *modeltest.Coverage, w int) string {
	var b strings.Builder
	b.WriteString(style.Heading.Render("coverage:") + "\n\n")

	typeW := lipgloss.Width("total")
	for _, tc := range cov.Types {
		if tw := lipgloss.Width(safeText(tc.Type)); tw > typeW {
			typeW = tw
		}
	}
	header := padRight("TYPE", typeW) + "  " + padRight("COVERED", 7) + "  " + padRight("TOTAL", 5) + "  PERCENT"
	b.WriteString(style.Faint.Render(header) + "\n")
	for _, tc := range cov.Types {
		pct := modeltest.Percent(tc.Covered, tc.Total)
		row := padRight(safeText(tc.Type), typeW) + "  " +
			padRight(itoa(tc.Covered), 7) + "  " +
			padRight(itoa(tc.Total), 5) + "  " +
			style.PercentColor(pct).Render(modeltest.FormatPercent(pct))
		b.WriteString(row + "\n")
	}
	total := padRight("total", typeW) + "  " +
		padRight(itoa(cov.Covered), 7) + "  " +
		padRight(itoa(cov.Total), 5) + "  " +
		modeltest.FormatPercent(cov.Percent)
	b.WriteString(style.Bold.Render(total) + "\n\n")

	for _, tc := range cov.Types {
		for _, rc := range tc.Relations {
			fracStyle := style.Success
			if rc.Covered < rc.Total {
				fracStyle = style.Failure
			}
			line := "  " + safeText(tc.Type) + "." + safeText(rc.Relation) + "  " +
				fracStyle.Render(itoa(rc.Covered)+"/"+itoa(rc.Total))
			if len(rc.Missed) > 0 {
				line += "  " + style.Warn.Render("MISSED: "+strings.Join(rc.Missed, ", "))
			}
			if w > 0 {
				line = lipgloss.NewStyle().Width(w).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}
	if len(cov.Unreachable) > 0 {
		b.WriteString(style.Faint.Render("unreachable: "+strings.Join(cov.Unreachable, ", ")) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
