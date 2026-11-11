package playground

import (
	"image/color"
	"strconv"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/dsl"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/vimedit"
)

// editorCellCache holds the highlighted lines of one window of one editor
// revision, so cursor moves and redraws that don't scroll or edit never
// re-highlight.
type editorCellCache struct {
	valid    bool
	rev      uint64
	from, to int
	lines    [][]dsl.Cell
}

// highlightLookback caps how far above the window highlighting starts when no
// top-level declaration is near, bounding the work per redraw.
const highlightLookback = 200

// editorLines returns the highlighted lines [top, top+height) of the buffer.
// Highlighting tracks bracket nesting across lines, which every top-level
// declaration resets, so only the window is lexed, starting from the nearest
// declaration above it: the cost follows the screen, not the model's size.
func (m Model) editorLines(top, height int) [][]dsl.Cell {
	to := min(top+height, m.editor.LineCount())
	rev := m.editor.Revision()
	if c := m.editorCells; c != nil && c.valid && c.rev == rev && c.from == top && c.to == to {
		return c.lines
	}
	from := top
	for from > 0 && top-from < highlightLookback && !declStart(m.editor.Line(from)) {
		from--
	}
	var b strings.Builder
	for i := from; i < to; i++ {
		if i > from {
			b.WriteByte('\n')
		}
		b.WriteString(string(m.editor.Line(i)))
	}
	lines := splitCellLines(dsl.Cells(b.String()))
	lines = lines[min(top-from, len(lines)):]
	if c := m.editorCells; c != nil {
		c.valid, c.rev, c.from, c.to, c.lines = true, rev, top, to, lines
	}
	return lines
}

// declStart reports whether line opens a top-level declaration, where
// highlighting can start without state from the lines above.
func declStart(line []rune) bool {
	for _, kw := range []string{"model", "type ", "condition ", "extend ", "module "} {
		if len(line) >= len(kw) && string(line[:len(kw)]) == kw {
			return true
		}
	}
	return false
}

// editorPane renders the DSL editor as a single syntax-colored pane, windowed
// to the visible rows starting at m.editorTop, with a line-number gutter (red
// ▸ marker on diagnostic lines), a reverse-video cursor block, the visual
// selection, and the diagnostic column styled red. Lines don't wrap: the pane
// scrolls sideways to keep the cursor in view.
func (m Model) editorPane(width, height int) string {
	lines := m.editorLines(m.editorTop, height)

	diagByLine := make(map[int]dsl.Diagnostic, len(m.editorDiags))
	for _, d := range m.editorDiags {
		if _, seen := diagByLine[d.Line]; !seen {
			diagByLine[d.Line] = d
		}
	}

	gutterW := len(strconv.Itoa(m.editor.LineCount()))
	// gutter marker(1) + number(gutterW) + separator(1).
	textW := max(1, width-gutterW-2)

	cur := m.editor.Cursor()
	// Scroll sideways just far enough that the cursor's column is on screen.
	xoff := max(0, cur.Col-textW+1)
	sel, hasSel := m.editor.Selection()

	rows := make([]string, 0, height)
	for i, cells := range lines {
		row := m.editorTop + i
		lr := lineRender{width: textW, xoff: xoff, cursorCol: -1, errCol: -1, selFrom: -1, selTo: -1}
		if row == cur.Row {
			lr.cursorCol = cur.Col
		}
		d, hasDiag := diagByLine[row]
		if hasDiag {
			lr.errCol = d.Col
		}
		if hasSel {
			lr.selFrom, lr.selTo = selectionCols(sel, row, len(cells))
		}
		gutter := renderGutter(row+1, gutterW, hasDiag)
		rows = append(rows, gutter+" "+lr.render(cells))
	}

	return strings.Join(rows, "\n")
}

// selectionCols is the inclusive column span of sel on row, or (-1, -1) when
// the row is outside it. An empty selected line shows one selected cell.
func selectionCols(sel vimedit.Selection, row, lineLen int) (int, int) {
	if row < sel.Start.Row || row > sel.End.Row {
		return -1, -1
	}
	from, to := 0, max(0, lineLen-1)
	if !sel.Linewise {
		if row == sel.Start.Row {
			from = sel.Start.Col
		}
		if row == sel.End.Row {
			to = sel.End.Col
		}
	}
	return from, to
}

// splitCellLines splits a cell stream into logical lines, dropping newline
// cells. Always returns at least one (possibly empty) line.
func splitCellLines(cells []dsl.Cell) [][]dsl.Cell {
	lines := [][]dsl.Cell{{}}
	for _, c := range cells {
		if c.R == '\n' {
			lines = append(lines, []dsl.Cell{})
			continue
		}
		lines[len(lines)-1] = append(lines[len(lines)-1], c)
	}
	return lines
}

// renderGutter renders a line number, right-aligned in numW, prefixed by a red
// marker on diagnostic lines (a space otherwise) so columns stay aligned.
func renderGutter(lineNum, numW int, hasDiag bool) string {
	num := strconv.Itoa(lineNum)
	if pad := numW - len(num); pad > 0 {
		num = strings.Repeat(" ", pad) + num
	}
	if hasDiag {
		return style.Failure.Render("▸" + num)
	}
	return " " + lipgloss.NewStyle().Foreground(style.Muted).Render(num)
}

// lineRender is how one buffer line is drawn: width visible columns starting
// at column xoff, with the cursor, error and selection columns marked (-1 for
// none; the selection span is inclusive).
type lineRender struct {
	width, xoff    int
	cursorCol      int
	errCol         int
	selFrom, selTo int
}

// render draws cells, one column per rune, coloring each and marking the
// cursor (a block past the last rune when it sits at end of line), error and
// selection columns. Runs of cells that look the same share one escape
// sequence: styling every rune separately dominated the redraw of a large
// model.
func (lr lineRender) render(cells []dsl.Cell) string {
	var b strings.Builder
	var run strings.Builder
	var runSGR string
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if runSGR == "" {
			b.WriteString(run.String())
		} else {
			b.WriteString(runSGR + run.String() + ansi.ResetStyle)
		}
		run.Reset()
	}
	put := func(sgr string, r rune) {
		if sgr != runSGR {
			flush()
			runSGR = sgr
		}
		run.WriteRune(r)
	}
	drawn := 0
	for j := lr.xoff; j < len(cells) && drawn < lr.width; j++ {
		put(lr.sgr(j, cells[j].Color), cellRune(cells[j].R))
		drawn++
	}
	if drawn < lr.width {
		switch {
		case lr.cursorCol >= len(cells):
			put(lr.sgr(lr.cursorCol, nil), ' ')
		case len(cells) == 0 && lr.selFrom == 0:
			put(lr.sgr(0, nil), ' ')
		}
	}
	flush()
	return b.String()
}

// sgr is the escape sequence for column j drawn in color fg ("" for plain).
func (lr lineRender) sgr(j int, fg color.Color) string {
	var st ansi.Style
	switch j {
	case lr.cursorCol:
		st = st.Reverse(true)
	case lr.errCol:
		st = st.ForegroundColor(style.Red).Underline(true)
	default:
		if fg != nil {
			st = st.ForegroundColor(fg)
		}
		if j >= lr.selFrom && j <= lr.selTo {
			st = st.BackgroundColor(style.BgHighlight)
		}
	}
	if len(st) == 0 {
		return ""
	}
	return st.String()
}

// cellRune is the rune drawn for r: a space for a tab, a control rune or
// anything else the terminal sanitizer drops, so every rune keeps its column.
func cellRune(r rune) rune {
	if r >= 0x20 && r < 0x7f {
		return r
	}
	if r == '\t' || style.SanitizeTerminal(string(r)) == "" {
		return ' '
	}
	return r
}
