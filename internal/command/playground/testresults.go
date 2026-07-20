package playground

import (
	"bytes"
	"strings"

	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/style"
)

// firstFailedTest returns the index of the first failed test result, or 0 when
// none failed — so the Test Results panel opens on the first failure when there
// is one, and on the first result otherwise.
func firstFailedTest(results []modeltest.TestResult) int {
	for i, r := range results {
		if !r.Passed {
			return i
		}
	}
	return 0
}

// testResultsBody renders the Tests section as a file-navigator tree: the
// workspace's test files with their tests nested under them (wbTreeBody).
func (m Model) testResultsBody() string {
	if m.wb.workspace == nil || len(m.wb.files) == 0 {
		return style.Faint.Render("No test workspace loaded. Run `ofga model test --playground` to run a\nworkspace's tests and explore its files here.")
	}
	if m.wb.running {
		return m.spinner.View() + " running tests…"
	}
	if m.wb.showCoverage && m.wb.coverage != nil {
		w, h := m.contentSize()
		return capLinesAt(renderWorkbenchCoverage(m.wb.coverage, w), m.wb.covScroll, h)
	}
	w, h := m.contentSize()
	return m.wbTreeBody(w, h)
}

// capLines trims body to at most h rows, replacing the last row with a hint
// when content was cut, so a coverage report taller than the pane doesn't push
// the status bar off-screen or silently drop its bottom rows.
func capLines(body string, h int) string {
	lines := strings.Split(body, "\n")
	if len(lines) <= h {
		return body
	}
	if h > 1 {
		lines = lines[:h-1]
		lines = append(lines, style.Faint.Render("  ⋯ more — enlarge the window to see it"))
	} else {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// capLinesAt renders at most h rows of body starting at scroll offset off
// (clamped into range), marking a hidden top and/or bottom edge with a faint
// hint so a pane taller than h can be wheel-scrolled without clipping content
// out of reach. It backs the two scrollable capLines panes in the Tests section
// (the coverage report and the verbose detail card); off == 0 with content
// below reproduces capLines' bottom "⋯ more" hint.
func capLinesAt(body string, off, h int) string {
	lines := strings.Split(body, "\n")
	if h <= 0 || len(lines) <= h {
		return body
	}
	if h == 1 {
		return lines[0]
	}
	max := len(lines) - h
	if off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	win := append([]string(nil), lines[off:off+h]...)
	if off > 0 {
		win[0] = style.Faint.Render("  ⋯ more above")
	}
	if off < max {
		win[h-1] = style.Faint.Render("  ⋯ more — scroll or enlarge the window")
	}
	return strings.Join(win, "\n")
}

// clampScroll adjusts a pane's wheel-scroll offset by one step (up decrements,
// toward the top), clamped so it can never scroll past body's first or last
// line given a visible window of h rows. Shared by the coverage report and the
// verbose detail card in the Tests section (see handleWheel).
func clampScroll(off int, up bool, body string, h int) int {
	if up {
		off--
	} else {
		off++
	}
	max := strings.Count(body, "\n") + 1 - h
	if max < 0 {
		max = 0
	}
	if off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	return off
}

// testResultDetail renders a single test result's detail pane: for a failed
// test, each failed assertion's subject plus its RenderExplain narrative; for
// a passing test, a short confirmation.
func (m Model) testResultDetail(r modeltest.TestResult) (string, string) {
	if r.Passed {
		return safeText(r.Name), style.Success.Render(style.IconCheck + " passed")
	}

	var b strings.Builder
	for _, a := range r.Assertions {
		if a.Passed {
			continue
		}
		b.WriteString(style.Failure.Render(style.IconCross+" "+safeText(a.Subject)) + "\n")
		var buf bytes.Buffer
		modeltest.RenderExplain(&buf, a)
		b.WriteString(safeMultiline(strings.TrimRight(buf.String(), "\n")) + "\n\n")
	}
	if b.Len() == 0 {
		// A failed test with no per-assertion detail (shouldn't normally happen).
		return safeText(r.Name), style.Failure.Render(style.IconCross + " failed")
	}
	return safeText(r.Name), strings.TrimRight(b.String(), "\n")
}
